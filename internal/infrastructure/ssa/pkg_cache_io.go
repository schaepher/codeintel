package ssa

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/schaepher/codeintel/internal/domain"
	"golang.org/x/tools/go/packages"
)

// LoadAnalyzerMarker 读全局 marker；无 marker 返回空。
func LoadAnalyzerMarker(repoDir string) string {
	data, err := os.ReadFile(analyzerMarkerPath(repoDir))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// SaveAnalyzerMarker 写全局 marker（目录自动创建；失败返回错误——调用方
// 决定是否阻塞）。
func SaveAnalyzerMarker(repoDir string) error {
	path := analyzerMarkerPath(repoDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(analyzerVersionHash()), 0o644)
}

// pkgCachePath 缓存文件路径（.codeintel/cache/<sha256(pkgPath)>.json）。
func pkgCachePath(repoDir, pkgPath string) string {
	sum := sha256.Sum256([]byte(pkgPath))
	return filepath.Join(repoDir, ".codeintel", "cache", hex.EncodeToString(sum[:])+".json")
}

// hashFilePool 复用分块读取缓冲（todo9-1）：原来每文件 os.ReadFile 一次
// 整文件副本（go2o 约 17MB 分配），改为 32KB 分块写入 hash。
var hashFilePool = sync.Pool{New: func() any { b := make([]byte, 32<<10); return &b }}

// hashFile 把文件内容 + 分隔符 0x00 写入 h（与 os.ReadFile + Write 逐字节等价）。
// todo9-2：缓存准备阶段检查适配器 ctx（取消后不再继续读文件/算 hash）。
func hashFile(ctx context.Context, h hash.Hash, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	// 包一层隐藏 *os.File 的 WriteTo（否则 io.CopyBuffer 忽略我们的
	// 缓冲，改用 WriteTo 内部自建缓冲 → 每文件多次分配）。
	buf := hashFilePool.Get().(*[]byte)
	_, err = io.CopyBuffer(h, struct{ io.Reader }{f}, *buf)
	hashFilePool.Put(buf)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err = h.Write([]byte{0})
	return err
}

// pkgContentHash 包源码内容 hash（CompiledGoFiles 拼接 sha256）。
func pkgContentHash(ctx context.Context, files []string) (string, error) {
	h := sha256.New()
	for _, f := range files {
		if err := hashFile(ctx, h, f); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// pkgCacheKeyHash Q213：本包缓存键 = 本包源码 hash + 直接依赖包源码
// hash 列表（按包路径排序拼接保证确定性）。depMemo 复用依赖包 hash
// （每包重读依赖文件是 O(包数×依赖文件总量)，memo 后降为每包一次）。
func pkgCacheKeyHash(ctx context.Context, pkg *packages.Package, depMemo map[string]string) (string, error) {
	h := sha256.New()
	files := pkg.CompiledGoFiles
	if len(files) == 0 {
		files = pkg.GoFiles
	}
	for _, f := range files {
		if err := hashFile(ctx, h, f); err != nil {
			return "", err
		}
	}
	deps := make([]string, 0, len(pkg.Imports))
	for path := range pkg.Imports {
		deps = append(deps, path)
	}
	sort.Strings(deps)
	for _, path := range deps {
		dp := pkg.Imports[path]
		if dp == nil {
			continue
		}
		dh, ok := depMemo[path]
		if !ok {
			dfiles := dp.CompiledGoFiles
			if len(dfiles) == 0 {
				dfiles = dp.GoFiles
			}
			var err error
			dh, err = pkgContentHash(ctx, dfiles)
			if err != nil {
				return "", err
			}
			depMemo[path] = dh
		}
		h.Write([]byte(path))
		h.Write([]byte{0})
		h.Write([]byte(dh))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// pkgCollect 单包产物收集器（Q246：块产物按块序号收集，最后一块到达
// 即整包写缓存并释放——原实现把全量包产物留到构建尾部统一写，go2o
// 实测占了峰值堆近 1GB）。
type pkgCollect struct {
	path    string // 缓存文件路径（hash 为空时仍写出但不会被命中）
	hash    string // 包缓存键（空 = 不做缓存）
	blocks  [][]*domain.CodeEntity
	facts   [][]*domain.Fact
	fd      []map[domain.CanonicalID]*funcData
	total   int
	done    int
	aborted bool // 取消/失败：禁止落盘半包缓存
}

// pkgCollectSet 包产物收集器集合（并发访问由调用方持 collectMu）。
type pkgCollectSet struct {
	repoPath   string
	blockCount map[string]int    // 包 → 块数（最后一块到达判定）
	hashes     map[string]string // 包 → 缓存键（空 = 不缓存）
	cols       map[string]*pkgCollect
}

func newPkgCollectSet(repoPath string, blockCount map[string]int, hashes map[string]string) *pkgCollectSet {
	return &pkgCollectSet{repoPath: repoPath, blockCount: blockCount, hashes: hashes,
		cols: map[string]*pkgCollect{}}
}

// get 取包收集器（首次懒建，按块数预分配槽位）。
func (s *pkgCollectSet) get(pkgPath string) *pkgCollect {
	pc := s.cols[pkgPath]
	if pc == nil {
		n := s.blockCount[pkgPath]
		pc = &pkgCollect{path: pkgCachePath(s.repoPath, pkgPath), hash: s.hashes[pkgPath],
			blocks: make([][]*domain.CodeEntity, n), facts: make([][]*domain.Fact, n),
			fd: make([]map[domain.CanonicalID]*funcData, n), total: n}
		s.cols[pkgPath] = pc
	}
	return pc
}

// release 删出集合（整包已写完——释放引用）。
func (s *pkgCollectSet) release(pkgPath string) { delete(s.cols, pkgPath) }

// abort 标记包产物不完整（取消/worker 失败）——后续不再落盘缓存。
func (s *pkgCollectSet) abort(pkgPath string) {
	if pc := s.cols[pkgPath]; pc != nil {
		pc.aborted = true
	}
}

// save 按块序号拼接产物并落盘缓存（并发下写文件不持收集锁）。Q?：节点/
// 边块**直接交给分块写入器**（避免先拼接整包指针切片）；摘要仍按追加语义
// 合并后分块。
func (pc *pkgCollect) save() {
	if pc == nil || pc.hash == "" || pc.aborted {
		return
	}
	// Q?：复用第一个非空块 map 作为合并目标（避免再分配一个整包 map）；
	// 块在 save 后即弃，可安全复用。块间仍按追加语义合并。
	var fd map[domain.CanonicalID]*funcData
	var fdMu sync.Mutex
	for _, m := range pc.fd {
		if fd == nil {
			fd = m
			continue
		}
		for id, d := range m {
			mergeFuncData(&fdMu, fd, id, d)
		}
	}
	if fd == nil {
		fd = map[domain.CanonicalID]*funcData{}
	}
	savePkgCacheBlocks(pc.path, pc.hash, pc.blocks, pc.facts, fd)
}

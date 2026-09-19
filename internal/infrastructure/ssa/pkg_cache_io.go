package ssa

import (
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
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

// pkgContentHash 包源码内容 hash（CompiledGoFiles 拼接 sha256）。
func pkgContentHash(files []string) (string, error) {
	h := sha256.New()
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return "", err
		}
		h.Write(data)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// pkgCacheKeyHash Q213：本包缓存键 = 本包源码 hash + 直接依赖包源码
// hash 列表（按包路径排序拼接保证确定性）。depMemo 复用依赖包 hash
// （每包重读依赖文件是 O(包数×依赖文件总量)，memo 后降为每包一次）。
func pkgCacheKeyHash(pkg *packages.Package, depMemo map[string]string) (string, error) {
	h := sha256.New()
	files := pkg.CompiledGoFiles
	if len(files) == 0 {
		files = pkg.GoFiles
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return "", err
		}
		h.Write(data)
		h.Write([]byte{0})
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
			dh, err = pkgContentHash(dfiles)
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

// loadPkgCache 读缓存并校验 hash；未命中（缺文件/版本/analyzer/hash 不符/
// 解码失败）返回 nil。Q181：Analyzer 是二进制内容 hash——分析逻辑变化后
// 旧缓存自动失效（确定机制，无需手动清理）。Q252：gob 编码，**头先读**
// （hash 不符时不反序列化大载荷）；旧 JSON 缓存解码即失败 → 自动 miss。
func loadPkgCache(path, wantHash string) *pkgCacheFile {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	dec := gob.NewDecoder(f)
	var hdr pkgCacheHeader
	if err := dec.Decode(&hdr); err != nil {
		return nil
	}
	if hdr.Version != pkgCacheFormat || hdr.Analyzer != analyzerVersionHash() || hdr.PkgHash != wantHash {
		return nil
	}
	var payload pkgCachePayload
	if err := dec.Decode(&payload); err != nil {
		return nil
	}
	return &pkgCacheFile{
		Version: hdr.Version, Analyzer: hdr.Analyzer, PkgHash: hdr.PkgHash,
		Nodes: payload.Nodes, Facts: payload.Facts, FuncData: payload.FuncData,
	}
}

// pkgCacheHeader 缓存头（先写先读：版本/hash 不符时直接拒绝，**不做**
// 大载荷反序列化——旧实现把 88MB JSON 全解完才发现 hash 不符）。
type pkgCacheHeader struct {
	Version  int
	Analyzer string
	PkgHash  string
}

// pkgCachePayload 缓存载荷（nodes/facts/funcData 三段）。
type pkgCachePayload struct {
	Nodes    []*domain.CodeEntity
	Facts    []*domain.Fact
	FuncData map[string]*cachedFuncData
}

// savePkgCache 写缓存（Q252：gob 编码 + 临时文件原子替换；失败不阻塞构建
// ——缓存是加速非必需）。
//
// 换 gob 的动机（实测）：JSON 序列化占构建分配 15.6%（jsonwire.AppendQuote
// 316MB flat）/ CPU 7.2%，缓存体积 ana 42MB、go2o 88MB；gob 是 stdlib
// 自描述二进制格式，体积与 CPU 双双下降，且读侧可"先头后身"。
// 原子替换：写入期间被并发读或写中断都不会留下半个文件（原先直接
// WriteFile，进程被杀会留截断 JSON——解码失败虽会被当作 miss，但白读盘）。
func savePkgCache(path, hash string, nodes []*domain.CodeEntity, facts []*domain.Fact,
	fd map[domain.CanonicalID]*funcData) {
	c := &pkgCacheFile{
		Version:  pkgCacheFormat,
		Analyzer: analyzerVersionHash(),
		PkgHash:  hash,
		Nodes:    nodes,
		Facts:    facts,
		FuncData: map[string]*cachedFuncData{},
	}
	for id, f := range fd {
		c.FuncData[string(id)] = toCachedFD(f)
	}
	writePkgCacheFile(path, c)
}

// writePkgCacheFile 按内容写缓存文件（供 savePkgCache 与测试共用）。
func writePkgCacheFile(path string, c *pkgCacheFile) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	// Q252：先写临时文件再 rename（同目录 rename 原子）
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return
	}
	enc := gob.NewEncoder(f)
	err = enc.Encode(pkgCacheHeader{Version: c.Version, Analyzer: c.Analyzer, PkgHash: c.PkgHash})
	if err == nil {
		err = enc.Encode(pkgCachePayload{Nodes: c.Nodes, Facts: c.Facts, FuncData: c.FuncData})
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp) // 不留下半截文件
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
	}
}

// pkgCollect 单包产物收集器（Q246：块产物按块序号收集，最后一块到达
// 即整包写缓存并释放——原实现把全量包产物留到构建尾部统一写，go2o
// 实测占了峰值堆近 1GB）。
type pkgCollect struct {
	path   string // 缓存文件路径（hash 为空时仍写出但不会被命中）
	hash   string // 包缓存键（空 = 不做缓存）
	blocks [][]*domain.CodeEntity
	facts  [][]*domain.Fact
	fd     []map[domain.CanonicalID]*funcData
	total  int
	done   int
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

// save 按块序号拼接产物并落盘缓存（并发下写文件不持收集锁）。
func (pc *pkgCollect) save() {
	if pc == nil || pc.hash == "" {
		return
	}
	var nodes []*domain.CodeEntity
	for _, b := range pc.blocks {
		nodes = append(nodes, b...)
	}
	var facts []*domain.Fact
	for _, f := range pc.facts {
		facts = append(facts, f...)
	}
	fd := map[domain.CanonicalID]*funcData{}
	// Q252：块间也按追加语义合并（`fd[id] = d` 覆盖会丢同 owner 的其它块贡献）
	var fdMu sync.Mutex
	for _, m := range pc.fd {
		for id, d := range m {
			mergeFuncData(&fdMu, fd, id, d)
		}
	}
	savePkgCache(pc.path, pc.hash, nodes, facts, fd)
}

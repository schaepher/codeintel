package ssa

// 包缓存分块协议（v4，从 pkg_cache_io.go 拆出——行数治理）：头 → 节点块 →
// 边块 → 摘要块 → 独立结束标记；解码拒绝截断/缺结束标记/结束标记带数据/
// 尾随消息。读侧先读头（hash 不符不反序列化大载荷）。

import (
	"encoding/gob"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/schaepher/codeintel/internal/domain"
)

// loadPkgCache 读缓存并校验 hash；未命中（缺文件/版本/analyzer/hash 不符/
// 解码失败/缺结束标记/尾随消息）返回 nil。Q252：gob 编码，**头先读**
// （hash 不符时不反序列化大载荷）。v4：分块读取、独立结束标记。
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
	nodes, facts, fd, ok := readPkgCacheChunks(dec)
	if !ok {
		return nil
	}
	return &pkgCacheFile{
		Version: hdr.Version, Analyzer: hdr.Analyzer, PkgHash: hdr.PkgHash,
		Nodes: nodes, Facts: facts, FuncData: fd,
	}
}

// readPkgCacheChunks 读取分块段直到结束标记；截断/缺结束标记/结束标记
// 带数据/尾随消息/未知块 → ok=false。
func readPkgCacheChunks(dec *gob.Decoder) (nodes []*domain.CodeEntity, facts []*domain.Fact, fd map[string]*cachedFuncData, ok bool) {
	fd = map[string]*cachedFuncData{}
	for {
		var ch pkgCacheChunk
		if err := dec.Decode(&ch); err != nil {
			return nil, nil, nil, false // 截断/缺结束标记
		}
		switch ch.Kind {
		case chunkNodes:
			nodes = append(nodes, ch.Nodes...)
		case chunkFacts:
			facts = append(facts, ch.Facts...)
		case chunkFD:
			for k, v := range ch.FuncData {
				fd[k] = v
			}
		case chunkEnd:
			if len(ch.Nodes) > 0 || len(ch.Facts) > 0 || len(ch.FuncData) > 0 {
				return nil, nil, nil, false // 结束标记带数据
			}
			var extra pkgCacheChunk
			if err := dec.Decode(&extra); err != io.EOF {
				return nil, nil, nil, false // 尾随消息
			}
			return nodes, facts, fd, true
		default:
			return nil, nil, nil, false // 未知块类型（损坏/旧格式）
		}
	}
}

// pkgCacheHeader 缓存头（先写先读：版本/hash 不符时直接拒绝，**不做**
// 大载荷反序列化——旧实现把 88MB JSON 全解完才发现 hash 不符）。
type pkgCacheHeader struct {
	Version  int
	Analyzer string
	PkgHash  string
}

// pkgCacheChunk 分块消息（v4）：Kind 区分节点/边/函数摘要/结束标记；
// 结束标记 Kind=chunkEnd 且其余字段为空。
type pkgCacheChunk struct {
	Kind     int
	Nodes    []*domain.CodeEntity
	Facts    []*domain.Fact
	FuncData map[string]*cachedFuncData
}

// 分块协议常量。
const (
	chunkEnd   = 0 // 结束标记
	chunkNodes = 1
	chunkFacts = 2
	chunkFD    = 3
	// chunkSize 每块条数（实测 200 优于 1000：分配/耗时，见 field_trace）。
	chunkSize = 200
)

// pkgCachePayload 缓存载荷（nodes/facts/funcData 三段）。
type pkgCachePayload struct {
	Nodes    []*domain.CodeEntity
	Facts    []*domain.Fact
	FuncData map[string]*cachedFuncData
}

// savePkgCache 写缓存（Q252：gob 编码 + 临时文件原子替换；失败不阻塞构建
// ——缓存是加速非必需）。v4：分块编码（内部按 chunkSize 切块）。
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

// savePkgCacheBlocks 用**已分块**的节点/边产物直接写缓存（包收集器路径
// ——避免先拼接整包节点、边指针切片；摘要仍合并后分块）。
func savePkgCacheBlocks(path, hash string, nodeBlocks [][]*domain.CodeEntity,
	factBlocks [][]*domain.Fact, fd map[domain.CanonicalID]*funcData) {
	fdOut := make(map[string]*cachedFuncData, len(fd))
	for id, f := range fd {
		fdOut[string(id)] = toCachedFD(f)
	}
	writePkgCacheBlocks(path, pkgCacheHeader{Version: pkgCacheFormat, Analyzer: analyzerVersionHash(), PkgHash: hash},
		nodeBlocks, factBlocks, fdOut)
}

// writePkgCacheFile 按内容写缓存文件（供 savePkgCache 与测试共用）：
// 直接按 chunkSize 切**平铺切片**编码——不再先建 nodeBlocks/factBlocks
// 切片头数组（减少编码期临时容器，todo2-2；协议不变）。
func writePkgCacheFile(path string, c *pkgCacheFile) {
	writePkgCacheHeaderAndSections(path,
		pkgCacheHeader{Version: c.Version, Analyzer: c.Analyzer, PkgHash: c.PkgHash},
		func(enc *gob.Encoder) error {
			return encodeFlatSections(enc, c.Nodes, c.Facts, c.FuncData)
		})
}

// writePkgCacheHeaderAndSections 写头 → 调用 sections 编码 → 尾（临时文件 +
// 关闭错误检查 + 原子替换）。
func writePkgCacheHeaderAndSections(path string, hdr pkgCacheHeader, sections func(*gob.Encoder) error) {
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
	err = enc.Encode(hdr)
	if err == nil {
		err = sections(enc)
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

// writePkgCacheBlocks 分块写入：头 → 节点块 → 边块 → 摘要块 → 结束标记；
// 临时文件 + 关闭错误检查 + 原子替换。
func writePkgCacheBlocks(path string, hdr pkgCacheHeader, nodeBlocks [][]*domain.CodeEntity,
	factBlocks [][]*domain.Fact, fd map[string]*cachedFuncData) {
	writePkgCacheHeaderAndSections(path, hdr, func(enc *gob.Encoder) error {
		return encodeChunkSections(enc, nodeBlocks, factBlocks, fd)
	})
}

// encodeFlatSections 平铺切片直接分块编码（不建切片头数组）。
func encodeFlatSections(enc *gob.Encoder, nodes []*domain.CodeEntity, facts []*domain.Fact,
	fd map[string]*cachedFuncData) error {
	for i := 0; i < len(nodes); i += chunkSize {
		end := i + chunkSize
		if end > len(nodes) {
			end = len(nodes)
		}
		if err := enc.Encode(pkgCacheChunk{Kind: chunkNodes, Nodes: nodes[i:end]}); err != nil {
			return err
		}
	}
	for i := 0; i < len(facts); i += chunkSize {
		end := i + chunkSize
		if end > len(facts) {
			end = len(facts)
		}
		if err := enc.Encode(pkgCacheChunk{Kind: chunkFacts, Facts: facts[i:end]}); err != nil {
			return err
		}
	}
	return encodeFDSections(enc, fd)
}

// encodeChunkSections 依次编码节点块/边块/摘要块/结束标记。
// 传入块可能超过 chunkSize（包收集器块大小与缓存块大小无关）——**再次按
// chunkSize 切分**，保证单条 gob 消息有界（todo3-2）。
func encodeChunkSections(enc *gob.Encoder, nodeBlocks [][]*domain.CodeEntity,
	factBlocks [][]*domain.Fact, fd map[string]*cachedFuncData) error {
	for _, b := range nodeBlocks {
		for i := 0; i < len(b); i += chunkSize {
			end := i + chunkSize
			if end > len(b) {
				end = len(b)
			}
			if err := enc.Encode(pkgCacheChunk{Kind: chunkNodes, Nodes: b[i:end]}); err != nil {
				return err
			}
		}
	}
	for _, b := range factBlocks {
		for i := 0; i < len(b); i += chunkSize {
			end := i + chunkSize
			if end > len(b) {
				end = len(b)
			}
			if err := enc.Encode(pkgCacheChunk{Kind: chunkFacts, Facts: b[i:end]}); err != nil {
				return err
			}
		}
	}
	return encodeFDSections(enc, fd)
}

// encodeFDSections 摘要段编码（键排序后每 chunkSize 一条消息）。
// 复用同一个 map 容器（每块 clear 后重用）——避免每 chunk 新分配 map。
func encodeFDSections(enc *gob.Encoder, fd map[string]*cachedFuncData) error {
	keys := make([]string, 0, len(fd))
	for k := range fd {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	// todo4-2：预分配容量按 min(chunkSize, 实际摘要数)——不足一块时不分配
	// 完整块容量（协议不变；map 复用仍保留）。
	capHint := chunkSize
	if len(keys) < capHint {
		capHint = len(keys)
	}
	m := make(map[string]*cachedFuncData, capHint)
	for i := 0; i < len(keys); i += chunkSize {
		end := i + chunkSize
		if end > len(keys) {
			end = len(keys)
		}
		clear(m)
		for _, k := range keys[i:end] {
			m[k] = fd[k]
		}
		if err := enc.Encode(pkgCacheChunk{Kind: chunkFD, FuncData: m}); err != nil {
			return err
		}
	}
	return enc.Encode(pkgCacheChunk{Kind: chunkEnd})
}

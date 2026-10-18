package ssa

// 分块规模上限 / 中途截断拒绝（从 pkg_cache_gob_test.go 拆出——行数治理）。

import (
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// seedChunkedCache 写一个跨多块的缓存文件，返回路径。
func seedChunkedCache(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "c.json")
	var nodes []*domain.CodeEntity
	for i := 0; i < chunkSize*2+3; i++ {
		nodes = append(nodes, &domain.CodeEntity{
			ID: domain.CanonicalID(fmt.Sprintf("symbol:go:m:f%d", i)), Kind: domain.KindFunction, Name: fmt.Sprintf("f%d", i),
		})
	}
	var facts []*domain.Fact
	for i := 0; i < chunkSize+7; i++ {
		facts = append(facts, &domain.Fact{
			SourceID: domain.CanonicalID(fmt.Sprintf("symbol:go:m:a%d", i)), TargetID: domain.CanonicalID(fmt.Sprintf("symbol:go:m:b%d", i)),
			Kind: domain.FactCalls, ToolSource: domain.ToolSSA, Confidence: 0.8,
		})
	}
	fd := map[string]*cachedFuncData{}
	for i := 0; i < chunkSize+1; i++ {
		fd[fmt.Sprintf("symbol:go:m:f%d", i)] = &cachedFuncData{Calls: []cachedCallInfo{{CalleeID: "symbol:go:m:g", CallLine: i}}}
	}
	writePkgCacheFile(path, &pkgCacheFile{Version: pkgCacheFormat, Analyzer: analyzerVersionHash(), PkgHash: "h",
		Nodes: nodes, Facts: facts, FuncData: fd})
	return path
}

// TestPkgCacheChunkSizeLimit：每块节点/边/摘要条数不得超过 chunkSize
// （todo2-5：限制单块规模）。
func TestPkgCacheChunkSizeLimit(t *testing.T) {
	path := seedChunkedCache(t)
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	dec := gob.NewDecoder(f)
	var hdr pkgCacheHeader
	if err := dec.Decode(&hdr); err != nil {
		t.Fatal(err)
	}
	blocks := 0
	for {
		var ch pkgCacheChunk
		if err := dec.Decode(&ch); err != nil {
			t.Fatalf("解码失败: %v", err)
		}
		if ch.Kind == chunkEnd {
			break
		}
		blocks++
		if len(ch.Nodes) > chunkSize {
			t.Errorf("节点块 %d 条 > chunkSize %d", len(ch.Nodes), chunkSize)
		}
		if len(ch.Facts) > chunkSize {
			t.Errorf("边块 %d 条 > chunkSize %d", len(ch.Facts), chunkSize)
		}
		if len(ch.FuncData) > chunkSize {
			t.Errorf("摘要块 %d 条 > chunkSize %d", len(ch.FuncData), chunkSize)
		}
	}
	if blocks == 0 {
		t.Fatal("应有多块")
	}
}

// TestPkgCacheTruncatedMidStreamRejected：流中途截断（半个 gob 消息）必须
// 拒绝，不得重放不完整产物（todo2-5）。
func TestPkgCacheTruncatedMidStreamRejected(t *testing.T) {
	path := seedChunkedCache(t)
	if got := loadPkgCache(path, "h"); got == nil {
		t.Fatal("完整缓存应命中")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// 截掉尾部 40%（落在最后一个块/结束标记中间）
	cut := len(data) * 6 / 10
	if err := os.WriteFile(path, data[:cut], 0o644); err != nil {
		t.Fatal(err)
	}
	if got := loadPkgCache(path, "h"); got != nil {
		t.Errorf("中途截断的缓存应被拒绝，got %d nodes", len(got.Nodes))
	}
}

// TestPkgCacheOversizedBlocksSplit（todo3-2）：调用方传入的节点/边块超过
// chunkSize 时，编码前**再次切分**——单条 gob 消息条数有界，内容往返不变。
func TestPkgCacheOversizedBlocksSplit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.json")
	var nodes []*domain.CodeEntity
	for i := 0; i < chunkSize*3+5; i++ {
		nodes = append(nodes, &domain.CodeEntity{
			ID: domain.CanonicalID(fmt.Sprintf("symbol:go:m:f%d", i)), Kind: domain.KindFunction, Name: fmt.Sprintf("f%d", i),
		})
	}
	var facts []*domain.Fact
	for i := 0; i < chunkSize*2+3; i++ {
		facts = append(facts, &domain.Fact{
			SourceID: domain.CanonicalID(fmt.Sprintf("symbol:go:m:a%d", i)), TargetID: domain.CanonicalID(fmt.Sprintf("symbol:go:m:b%d", i)),
			Kind: domain.FactCalls, ToolSource: domain.ToolSSA, Confidence: 0.8,
		})
	}
	// 单块（远超 chunkSize）
	writePkgCacheBlocks(path, pkgCacheHeader{Version: pkgCacheFormat, Analyzer: analyzerVersionHash(), PkgHash: "h"},
		[][]*domain.CodeEntity{nodes}, [][]*domain.Fact{facts}, map[string]*cachedFuncData{})

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	dec := gob.NewDecoder(f)
	var hdr pkgCacheHeader
	if err := dec.Decode(&hdr); err != nil {
		t.Fatal(err)
	}
	blocks := 0
	for {
		var ch pkgCacheChunk
		if err := dec.Decode(&ch); err != nil {
			t.Fatalf("解码失败: %v", err)
		}
		if ch.Kind == chunkEnd {
			break
		}
		blocks++
		if len(ch.Nodes) > chunkSize || len(ch.Facts) > chunkSize {
			t.Errorf("块超限: nodes=%d facts=%d (chunkSize=%d)", len(ch.Nodes), len(ch.Facts), chunkSize)
		}
	}
	f.Close()
	if blocks == 0 {
		t.Fatal("应有多块")
	}
	got := loadPkgCache(path, "h")
	if got == nil {
		t.Fatal("超大块缓存应可命中")
	}
	if !reflect.DeepEqual(nodes, got.Nodes) {
		t.Errorf("Nodes 往返不一致（%d 项）", len(nodes))
	}
	if !reflect.DeepEqual(facts, got.Facts) {
		t.Errorf("Facts 往返不一致（%d 项）", len(facts))
	}
}

// TestPkgCacheSmallSummaryChunkRoundTrip（todo4-2）：摘要数不足一块时
// （预分配容量 = 实际条数）分块协议与内容不变。
func TestPkgCacheSmallSummaryChunkRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "small.json")
	fd := map[string]*cachedFuncData{}
	for i := 0; i < 3; i++ {
		fd[fmt.Sprintf("symbol:go:m:f%d", i)] = &cachedFuncData{
			Calls: []cachedCallInfo{{CalleeID: "symbol:go:m:g", CallLine: i, CallPos: i * 10}},
		}
	}
	writePkgCacheFile(path, &pkgCacheFile{Version: pkgCacheFormat, Analyzer: analyzerVersionHash(), PkgHash: "h", FuncData: fd})
	got := loadPkgCache(path, "h")
	if got == nil {
		t.Fatal("小摘要数缓存应命中")
	}
	if !reflect.DeepEqual(fd, got.FuncData) {
		t.Errorf("摘要内容往返不一致（%d 项）", len(fd))
	}
}

// TestPkgCacheCorruptedInputs（todo4-3）：损坏头 / 多点截断一律拒绝
// （流式读取不得重放半截产物）。
func TestPkgCacheCorruptedInputs(t *testing.T) {
	good := seedChunkedCache(t)
	data, err := os.ReadFile(good)
	if err != nil {
		t.Fatal(err)
	}
	// 损坏头
	badPath := filepath.Join(t.TempDir(), "bad.json")
	buf := append([]byte(nil), data...)
	for i := 0; i < 16 && i < len(buf); i++ {
		buf[i] = 0xFF
	}
	if err := os.WriteFile(badPath, buf, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := loadPkgCache(badPath, "h"); got != nil {
		t.Errorf("损坏头应拒绝，got %d nodes", len(got.Nodes))
	}
	// 多点截断（含块边界前的截断）
	for _, pct := range []int{5, 20, 40, 60, 80, 95} {
		cut := len(data) * pct / 100
		p := filepath.Join(t.TempDir(), fmt.Sprintf("cut%d.json", pct))
		if err := os.WriteFile(p, data[:cut], 0o644); err != nil {
			t.Fatal(err)
		}
		if got := loadPkgCache(p, "h"); got != nil {
			t.Errorf("%d%% 截断应拒绝，got %d nodes", pct, len(got.Nodes))
		}
	}
}

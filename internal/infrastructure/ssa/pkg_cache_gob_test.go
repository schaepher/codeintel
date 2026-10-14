package ssa

// Q252：包缓存序列化格式（JSON → gob）的**往返深比较**测试。
//
// 为什么必须锁：gob 是自描述但**静默丢字段**的格式——未导出字段、未注册
// 的接口具体类型、不支持的形状都会在编码/解码时悄悄消失（或报错），而缓存
// 一旦丢字段，产物就会静默缺内容（Q248/Q251 都吃过"静默"的亏）。
// 断言两条：
//  1. 手工构造的全形状载荷（含 map[string]any 的各类具体类型）往返后
//     DeepEqual；
//  2. **真实产物**（fixture 构建出的 nodes/facts/funcData）往返后 DeepEqual。

import (
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// 手工全形状载荷：覆盖 properties/metadata 里出现过的具体类型。
func TestPkgCacheGobRoundTripShapes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")
	in := &pkgCacheFile{
		Version:  pkgCacheFormat,
		Analyzer: analyzerVersionHash(),
		PkgHash:  "hash-1",
		Nodes: []*domain.CodeEntity{{
			ID: "symbol:go:m:f", Kind: domain.KindFieldAccess, Name: "f",
			FilePath: "a.go", LineStart: 3, LineEnd: 4,
			Properties: map[string]any{
				"sig": "func f()", "line": 42, "flag": true, "conf": 0.8,
				"fields": []string{"a", "b"}, "nothing": nil,
			},
		}},
		Facts: []*domain.Fact{{
			SourceID: "symbol:go:m:a", TargetID: "symbol:go:m:b", Kind: domain.FactCalls,
			ToolSource: domain.ToolSSA, Confidence: 0.8,
			Metadata: map[string]any{"interface_method": "Save", "origin": "enum", "confidence": 0.7, "list": []string{"x"}},
		}},
		FuncData: map[string]*cachedFuncData{
			"symbol:go:m:f": {
				DirectReads:    []cachedFieldEntry{{FieldPath: "m.T.X", InstancePath: "m.x", Line: 9, Snippet: "s", CallLine: 11, CallArg: "p"}},
				DirectWrites:   []cachedFieldEntry{{FieldPath: "m.T.Y", InstancePath: "m.y", Line: 10}},
				IndirectWrites: []cachedFieldEntry{{FieldPath: "m.T.Z", InstancePath: "m.z", Line: 12}},
				Calls:          []cachedCallInfo{{CalleeID: "symbol:go:m:g", ArgStructPaths: []string{"m.T"}, CallLine: 13, ArgNames: []string{"a", "b"}}},
			},
		},
	}
	writePkgCacheFile(path, in)

	got := loadPkgCache(path, "hash-1")
	if got == nil {
		t.Fatal("往返后应命中（版本/analyzer/hash 匹配）")
	}
	if !reflect.DeepEqual(in.Nodes, got.Nodes) {
		t.Errorf("Nodes 往返不相等：\n in=%+v\nout=%+v", in.Nodes[0], got.Nodes[0])
	}
	if !reflect.DeepEqual(in.Facts, got.Facts) {
		t.Errorf("Facts 往返不相等：\n in=%+v\nout=%+v", in.Facts[0], got.Facts[0])
	}
	if !reflect.DeepEqual(in.FuncData, got.FuncData) {
		t.Errorf("FuncData 往返不相等：\n in=%+v\nout=%+v", in.FuncData, got.FuncData)
	}
}

// 真实产物往返：用 fixture 构建出的 nodes/facts/funcData（含真实 properties
// 形状）编解码后深比较。
func TestPkgCacheGobRoundTripRealArtifacts(t *testing.T) {
	nodes, facts, _, _ := indexFixtureFullOrigins(t, map[string]string{
		"go.mod":     determinismFixture,
		"types/t.go": determinismTypes,
		"impl/i.go":  determinismImpl,
		"main.go":    determinismMain,
	})
	if len(nodes) == 0 || len(facts) == 0 {
		t.Fatalf("fixture 未产出（nodes=%d facts=%d）", len(nodes), len(facts))
	}
	funcData := map[string]*cachedFuncData{
		"symbol:go:example.com/mtest/impl:(sqlStore).Load": {
			DirectReads: []cachedFieldEntry{{FieldPath: "impl.sqlStore.rows", InstancePath: "s.rows", Line: 11}},
			Calls:       []cachedCallInfo{{CalleeID: "symbol:go:example.com/mtest/impl:NewStore", CallLine: 11}},
		},
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "c.json")
	in := &pkgCacheFile{Version: pkgCacheFormat, Analyzer: analyzerVersionHash(), PkgHash: "h",
		Nodes: nodes, Facts: facts, FuncData: funcData}
	writePkgCacheFile(path, in)

	got := loadPkgCache(path, "h")
	if got == nil {
		t.Fatal("真实产物往返应命中")
	}
	if !reflect.DeepEqual(in.Nodes, got.Nodes) {
		t.Errorf("真实产物 Nodes 往返不相等（%d 项）", len(nodes))
		for i := range in.Nodes {
			if i < len(got.Nodes) && !reflect.DeepEqual(in.Nodes[i], got.Nodes[i]) {
				t.Errorf("首个差异 [%d]：\n in=%+v\nout=%+v", i, in.Nodes[i], got.Nodes[i])
				break
			}
		}
	}
	if !reflect.DeepEqual(in.Facts, got.Facts) {
		t.Errorf("真实产物 Facts 往返不相等（%d 项）", len(facts))
	}
}

// 版本/分析器/hash 不匹配必须拒绝（且不因解码大载荷而浪费时间：头先读）。
func TestPkgCacheGobRejectsStale(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.json")
	savePkgCache(path, "h1", []*domain.CodeEntity{{ID: "x", Kind: domain.KindFunction, Name: "x"}}, nil,
		map[domain.CanonicalID]*funcData{})
	if got := loadPkgCache(path, "h2"); got != nil {
		t.Error("pkg_hash 不匹配应返回 nil")
	}
	if got := loadPkgCache(path, "h1"); got == nil {
		t.Error("匹配的 hash 应命中")
	}
	// 非缓存文件（旧 JSON 格式/垃圾内容）→ nil，不 panic
	bad := filepath.Join(dir, "bad.json")
	writeFile(t, bad, `{"version":1,"analyzer":"x","pkg_hash":"h"}`)
	if got := loadPkgCache(bad, "h"); got != nil {
		t.Error("非 gob 内容应返回 nil（旧 JSON 缓存自动失效）")
	}
}

// TestPkgCacheChunkedMultiBlock：节点/边/摘要都超过 chunkSize 时跨块往返
// 深比较（分块协议不丢条、不乱序）。
func TestPkgCacheChunkedMultiBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.json")
	var nodes []*domain.CodeEntity
	for i := 0; i < chunkSize*2+3; i++ {
		nodes = append(nodes, &domain.CodeEntity{
			ID: domain.CanonicalID(fmt.Sprintf("symbol:go:m:f%d", i)), Kind: domain.KindFunction, Name: fmt.Sprintf("f%d", i),
			Properties: map[string]any{"i": i},
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
	in := &pkgCacheFile{Version: pkgCacheFormat, Analyzer: analyzerVersionHash(), PkgHash: "h", Nodes: nodes, Facts: facts, FuncData: fd}
	writePkgCacheFile(path, in)

	got := loadPkgCache(path, "h")
	if got == nil {
		t.Fatal("跨块缓存应命中")
	}
	if !reflect.DeepEqual(nodes, got.Nodes) {
		t.Errorf("跨块 Nodes 不相等（%d 项）", len(nodes))
	}
	if !reflect.DeepEqual(facts, got.Facts) {
		t.Errorf("跨块 Facts 不相等（%d 项）", len(facts))
	}
	if !reflect.DeepEqual(fd, got.FuncData) {
		t.Errorf("跨块 FuncData 不相等（%d 项）", len(fd))
	}
}

// TestPkgCacheChunkIntegrity：缺结束标记/结束标记带数据/尾随消息/旧版本
// 一律拒绝（避免重放不完整产物）。
func TestPkgCacheChunkIntegrity(t *testing.T) {
	dir := t.TempDir()
	writeCache := func(name string, fn func(enc *gob.Encoder)) string {
		p := filepath.Join(dir, name)
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		fn(gob.NewEncoder(f))
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		return p
	}
	hdr := pkgCacheHeader{Version: pkgCacheFormat, Analyzer: analyzerVersionHash(), PkgHash: "h"}
	oneNode := []*domain.CodeEntity{{ID: "x", Kind: domain.KindFunction, Name: "x"}}

	noEnd := writeCache("noend", func(enc *gob.Encoder) {
		_ = enc.Encode(hdr)
		_ = enc.Encode(pkgCacheChunk{Kind: chunkNodes, Nodes: oneNode})
	})
	if got := loadPkgCache(noEnd, "h"); got != nil {
		t.Error("缺结束标记应拒绝")
	}
	endData := writeCache("enddata", func(enc *gob.Encoder) {
		_ = enc.Encode(hdr)
		_ = enc.Encode(pkgCacheChunk{Kind: chunkEnd, Nodes: oneNode})
	})
	if got := loadPkgCache(endData, "h"); got != nil {
		t.Error("结束标记带数据应拒绝")
	}
	trailing := writeCache("trailing", func(enc *gob.Encoder) {
		_ = enc.Encode(hdr)
		_ = enc.Encode(pkgCacheChunk{Kind: chunkEnd})
		_ = enc.Encode(pkgCacheChunk{Kind: chunkNodes, Nodes: oneNode})
	})
	if got := loadPkgCache(trailing, "h"); got != nil {
		t.Error("尾随消息应拒绝")
	}
	old := writeCache("old", func(enc *gob.Encoder) {
		_ = enc.Encode(pkgCacheHeader{Version: 3, Analyzer: analyzerVersionHash(), PkgHash: "h"})
		_ = enc.Encode(pkgCacheChunk{Kind: chunkEnd})
	})
	if got := loadPkgCache(old, "h"); got != nil {
		t.Error("旧版本（v3）应拒绝")
	}

}

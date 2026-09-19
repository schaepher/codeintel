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
	"bytes"
	"encoding/gob"
	"encoding/json"
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

// BenchmarkPkgCacheSerialization：缓存编解码的 CPU/分配对比（gob vs JSON）。
// 数据规模取真实缓存文件量级（1.75 万节点 + 2 万边 + 400 funcData）。
// 跑法：go test ./internal/infrastructure/ssa/ -run XXX -bench PkgCacheSerialization
func BenchmarkPkgCacheSerialization(b *testing.B) {
	payload := benchCachePayload(17500, 19600, 400)
	b.Run("gob-encode", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			var buf bytes.Buffer
			enc := gob.NewEncoder(&buf)
			_ = enc.Encode(pkgCacheHeader{Version: pkgCacheFormat, Analyzer: "a", PkgHash: "h"})
			_ = enc.Encode(payload)
		}
	})
	var gobBuf bytes.Buffer
	enc := gob.NewEncoder(&gobBuf)
	_ = enc.Encode(pkgCacheHeader{Version: pkgCacheFormat, Analyzer: "a", PkgHash: "h"})
	_ = enc.Encode(payload)
	b.Run("gob-decode", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			dec := gob.NewDecoder(bytes.NewReader(gobBuf.Bytes()))
			var hdr pkgCacheHeader
			var p pkgCachePayload
			_ = dec.Decode(&hdr)
			_ = dec.Decode(&p)
		}
	})
	b.Run("json-encode(对照)", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = json.Marshal(payload)
		}
	})
	js, _ := json.Marshal(payload)
	b.Run("json-decode(对照)", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			var p pkgCachePayload
			_ = json.Unmarshal(js, &p)
		}
	})
	b.Logf("载荷大小：gob=%dKB json=%dKB", gobBuf.Len()>>10, len(js)>>10)
}

func benchCachePayload(nNodes, nFacts, nFD int) pkgCachePayload {
	p := pkgCachePayload{
		Nodes:    make([]*domain.CodeEntity, 0, nNodes),
		Facts:    make([]*domain.Fact, 0, nFacts),
		FuncData: map[string]*cachedFuncData{},
	}
	for i := 0; i < nNodes; i++ {
		p.Nodes = append(p.Nodes, &domain.CodeEntity{
			ID: domain.CanonicalID("symbol:go:example.com/m/pkg" + itoaPad(i%50) + ":Func" + itoaPad(i%500) + "#t" + itoaPad(i%20)), Kind: domain.KindSSAValue,
			Name: "field" + itoaPad(i%100), FilePath: "internal/pkg/file" + itoaPad(i%200) + ".go",
			LineStart: i % 500, LineEnd: i%500 + 2,
			Properties: map[string]any{
				"func_id":     "symbol:go:example.com/m/pkg:Func" + itoaPad(i%500),
				"type_string": "*example.com/m/pkg.T" + itoaPad(i%100),
				"origin_kind": "local", "ssa_op": "alloc",
			},
		})
	}
	for i := 0; i < nFacts; i++ {
		p.Facts = append(p.Facts, &domain.Fact{
			SourceID: domain.CanonicalID("symbol:go:example.com/m/pkg:A" + itoaPad(i%500)),
			TargetID: domain.CanonicalID("symbol:go:example.com/m/pkg:B" + itoaPad(i%700)),
			Kind:     domain.FactDataFlowsTo, ToolSource: domain.ToolSSA, Confidence: 0.8,
			Metadata: map[string]any{"line": i % 500},
		})
	}
	for i := 0; i < nFD; i++ {
		id := "symbol:go:example.com/m/pkg:Func" + itoaPad(i)
		p.FuncData[id] = &cachedFuncData{
			DirectReads:  []cachedFieldEntry{{FieldPath: "pkg.T.X", InstancePath: "t.x", Line: i % 500, Snippet: "x := t.X"}},
			DirectWrites: []cachedFieldEntry{{FieldPath: "pkg.T.Y", InstancePath: "t.y", Line: i % 499}},
			Calls:        []cachedCallInfo{{CalleeID: "symbol:go:example.com/m/pkg:G", CallLine: i % 300, ArgNames: []string{"a", "b"}}},
		}
	}
	return p
}

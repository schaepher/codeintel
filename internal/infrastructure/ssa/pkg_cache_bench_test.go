package ssa

// 包缓存编解码基准（从 pkg_cache_gob_test.go 拆出——行数治理）：整包 gob /
// 分块 gob（生产路径）/ JSON 对照。

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// BenchmarkPkgCacheSerialization：缓存编解码的 CPU/分配对比（整包 gob vs
// 分块 gob（生产路径）vs JSON 对照）。数据规模取真实缓存文件量级
// （1.75 万节点 + 2 万边 + 400 funcData）。
// 跑法：go test ./internal/infrastructure/ssa/ -run XXX -bench PkgCacheSerialization
func BenchmarkPkgCacheSerialization(b *testing.B) {
	payload := benchCachePayload(17500, 19600, 400)
	nodeBlocks := chunkSlices(payload.Nodes, chunkSize)
	factBlocks := chunkSlices(payload.Facts, chunkSize)
	hdr := pkgCacheHeader{Version: pkgCacheFormat, Analyzer: "a", PkgHash: "h"}
	b.Run("chunked-encode", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			var buf bytes.Buffer
			enc := gob.NewEncoder(&buf)
			_ = enc.Encode(hdr)
			_ = encodeChunkSections(enc, nodeBlocks, factBlocks, payload.FuncData)
		}
	})
	b.Run("gob-encode(整包对照)", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			var buf bytes.Buffer
			enc := gob.NewEncoder(&buf)
			_ = enc.Encode(hdr)
			_ = enc.Encode(payload)
		}
	})
	var chunkBuf bytes.Buffer
	cenc := gob.NewEncoder(&chunkBuf)
	_ = cenc.Encode(hdr)
	_ = encodeChunkSections(cenc, nodeBlocks, factBlocks, payload.FuncData)
	b.Run("chunked-decode", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			dec := gob.NewDecoder(bytes.NewReader(chunkBuf.Bytes()))
			var h pkgCacheHeader
			_ = dec.Decode(&h)
			_, _, _, _ = readPkgCacheChunks(dec)
		}
	})
	var gobBuf bytes.Buffer
	enc := gob.NewEncoder(&gobBuf)
	_ = enc.Encode(hdr)
	_ = enc.Encode(payload)
	b.Run("gob-decode(对照)", func(b *testing.B) {
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
	b.Logf("载荷大小：chunked-gob=%dKB 整包-gob=%dKB json=%dKB", chunkBuf.Len()>>10, gobBuf.Len()>>10, len(js)>>10)
}

// chunkSlices 把切片切成 size 大小的块。
func chunkSlices[T any](in []T, size int) [][]T {
	var out [][]T
	for i := 0; i < len(in); i += size {
		end := i + size
		if end > len(in) {
			end = len(in)
		}
		out = append(out, in[i:end])
	}
	return out
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

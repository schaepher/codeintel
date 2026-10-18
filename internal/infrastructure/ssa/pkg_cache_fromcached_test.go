package ssa

import (
	"reflect"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestFromCachedFDRestoresCallPos（todo3-5）：CallPos 必须从缓存恢复——
// 否则暖重放会把"同一行不同调用点"重新合并，间接写实参元数据漂移。
func TestFromCachedFDRestoresCallPos(t *testing.T) {
	c := &cachedFuncData{Calls: []cachedCallInfo{
		{CalleeID: "symbol:go:m:g", ArgStructPaths: []string{"m.T"}, CallLine: 7, CallPos: 100, ArgNames: []string{"a"}},
		{CalleeID: "symbol:go:m:g", ArgStructPaths: []string{"m.T"}, CallLine: 7, CallPos: 140, ArgNames: []string{"b"}},
	}}
	fd := fromCachedFD(c)
	if len(fd.calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(fd.calls))
	}
	if fd.calls[0].callPos != 100 || fd.calls[1].callPos != 140 {
		t.Errorf("CallPos 未恢复: %+v", fd.calls)
	}
	// 只比 Calls（空 Field 切片的 nil/非 nil 语义由 toCachedFD 决定，与
	// CallPos 无关）
	if back := toCachedFD(fd); !reflect.DeepEqual(c.Calls, back.Calls) {
		t.Errorf("CallPos 往返不一致:\n got %+v\nwant %+v", back.Calls, c.Calls)
	}
}

// TestFromCachedFDNilAndContent（todo3-6）：空输入保持 nil 切片语义；
// 非空输入预分配后内容与原文等价。
func TestFromCachedFDNilAndContent(t *testing.T) {
	fd := fromCachedFD(&cachedFuncData{})
	if fd.directReads != nil || fd.directWrites != nil || fd.indirectWrites != nil || fd.calls != nil {
		t.Errorf("空输入应保持 nil 切片：%+v", fd)
	}

	in := &funcData{
		directReads:    []fieldEntry{{fieldPath: "m.T.A", instancePath: "t.A", line: 1, snippet: "x"}},
		directWrites:   []fieldEntry{{fieldPath: "m.T.B", instancePath: "t.B", line: 2, snippet: "y"}},
		indirectWrites: []fieldEntry{{fieldPath: "m.T.C", instancePath: "u.C", line: 3, snippet: "z", callLine: 9, callArg: "u"}},
		calls: []callInfo{{
			calleeID:       domain.CanonicalID("symbol:go:m:g"),
			argStructPaths: []string{"m.T"},
			callLine:       9, callPos: 42, argNames: []string{"u"},
		}},
	}
	got := fromCachedFD(toCachedFD(in))
	if !reflect.DeepEqual(in, got) {
		t.Errorf("往返不一致:\n got %+v\nwant %+v", got, in)
	}
}

// BenchmarkFromCachedFDPrealloc（todo3-6）：已知条数预分配的局部基准
// （不宣称完整构建/GC 扫描同比改善）。
func BenchmarkFromCachedFDPrealloc(b *testing.B) {
	c := &cachedFuncData{}
	for i := 0; i < 2000; i++ {
		c.Calls = append(c.Calls, cachedCallInfo{
			CalleeID: "symbol:go:m:g", ArgStructPaths: []string{"m.T"},
			CallLine: i, CallPos: i * 10, ArgNames: []string{"a"},
		})
		c.DirectReads = append(c.DirectReads, cachedFieldEntry{FieldPath: "m.T.A", Line: i})
		c.DirectWrites = append(c.DirectWrites, cachedFieldEntry{FieldPath: "m.T.B", Line: i})
		c.IndirectWrites = append(c.IndirectWrites, cachedFieldEntry{FieldPath: "m.T.C", Line: i})
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = fromCachedFD(c)
	}
}

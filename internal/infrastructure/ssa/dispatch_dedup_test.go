package ssa

import (
	"go/token"
	"go/types"
	"reflect"
	"testing"
)

func testNamed(t testing.TB, name string) *types.Named {
	t.Helper()
	pkg := types.NewPackage("example.com/t", "t")
	return types.NewNamed(types.NewTypeName(token.NoPos, pkg, name, nil), types.NewStruct(nil, nil), nil)
}

// naiveAppend 旧实现（线性去重）——测试参照。
func naiveAppend(regs dispatchReg, iface *types.Named, dyn, file string, line int) {
	if regs[iface] == nil {
		regs[iface] = map[string][]dispatchSite{}
	}
	for _, old := range regs[iface][dyn] {
		if old.file == file && old.line == line {
			return
		}
	}
	regs[iface][dyn] = append(regs[iface][dyn], dispatchSite{file: file, line: line})
}

// TestDispatchDedupMatchesNaive（todo8-1）：去重结果/顺序与旧线性实现逐项
// 一致（空、重复、跨文件同行、阈值前后、多分组）。
func TestDispatchDedupMatchesNaive(t *testing.T) {
	i1 := testNamed(t, "I1")
	i2 := testNamed(t, "I2")
	type step struct {
		iface *types.Named
		dyn   string
		file  string
		line  int
	}
	var seq []step
	// 31 个不同位置（阈值前）
	for i := 0; i < 31; i++ {
		seq = append(seq, step{i1, "dynA", "a.go", i + 1})
	}
	// 阈值边界：第 32 个 + 重复
	seq = append(seq, step{i1, "dynA", "a.go", 32})
	seq = append(seq, step{i1, "dynA", "a.go", 32}) // 重复
	seq = append(seq, step{i1, "dynA", "b.go", 32}) // 跨文件同一行号
	seq = append(seq, step{i1, "dynA", "a.go", 1})  // 旧位置重复
	// 第二/第三分组
	seq = append(seq, step{i1, "dynB", "a.go", 1}, step{i2, "dynA", "c.go", 7})
	seq = append(seq, step{i2, "dynA", "c.go", 7})

	naive := dispatchReg{}
	naiveRegs := dispatchReg{}
	var dd dispatchDedup
	for _, s := range seq {
		naiveAppend(naive, s.iface, s.dyn, s.file, s.line)
		dd.append(naiveRegs, s.iface, s.dyn, s.file, s.line)
	}
	if !reflect.DeepEqual(naive, naiveRegs) {
		t.Errorf("去重结果与线性实现不一致:\n naive=%+v\n got=%+v", naive, naiveRegs)
	}
	// 顺序：dynA 的第 32 个位置之后应保留 b.go:32（跨文件），a.go:1 不重复
	got := naiveRegs[i1]["dynA"]
	if got[31].file != "a.go" || got[31].line != 32 {
		t.Errorf("第 32 个位置 = %+v", got[31])
	}
	if got[32].file != "b.go" || got[32].line != 32 {
		t.Errorf("跨文件同号位置 = %+v", got[32])
	}
	if len(got) != 33 {
		t.Errorf("dynA 位置数 = %d, want 33（重复被去掉）", len(got))
	}
}

// TestDispatchDedupEmpty：空输入不创建集合/编号表（稀疏约束）。
func TestDispatchDedupEmpty(t *testing.T) {
	var dd dispatchDedup
	regs := dispatchReg{}
	if dd.dense != nil || dd.fileIDs != nil {
		t.Error("空输入不应创建集合/编号表")
	}
	if len(regs) != 0 {
		t.Error("空输入不应有分组")
	}
}

// BenchmarkDispatchDedupSparse 稀疏（8 个位置）重复追加最后一项。
func BenchmarkDispatchDedupSparse(b *testing.B) {
	iface := testNamed(b, "I")
	var dd dispatchDedup
	regs := dispatchReg{}
	for i := 0; i < 8; i++ {
		dd.append(regs, iface, "dyn", "a.go", i+1)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dd.append(regs, iface, "dyn", "a.go", 8)
	}
}

// BenchmarkDispatchDedupDenseLinear 稠密（64 个位置）旧线性去重对照。
func BenchmarkDispatchDedupDenseLinear(b *testing.B) {
	iface := testNamed(b, "I")
	regs := dispatchReg{}
	for i := 0; i < 256; i++ {
		naiveAppend(regs, iface, "dyn", "a.go", i+1)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		naiveAppend(regs, iface, "dyn", "a.go", 256)
	}
}

// BenchmarkDispatchDedupDense 稠密（64 个位置，集合路径）重复追加最后一项。
func BenchmarkDispatchDedupDense(b *testing.B) {
	iface := testNamed(b, "I")
	var dd dispatchDedup
	regs := dispatchReg{}
	for i := 0; i < 256; i++ {
		dd.append(regs, iface, "dyn", "a.go", i+1)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dd.append(regs, iface, "dyn", "a.go", 256)
	}
}

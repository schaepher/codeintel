package ssa

import (
	"reflect"
	"testing"

	"golang.org/x/tools/go/ssa"
)

func fixtureValues(s *funcSnapshot) []ssa.Value {
	var out []ssa.Value
	for _, fn := range s.all() {
		out = append(out, fn)
		for _, p := range fn.Params {
			out = append(out, p)
		}
		for _, fv := range fn.FreeVars {
			out = append(out, fv)
		}
		for _, b := range fn.Blocks {
			for _, instr := range b.Instrs {
				for _, op := range instr.Operands(nil) {
					if op != nil {
						out = append(out, *op)
					}
				}
			}
		}
	}
	return out
}

// TestSortedValuesKeyPrecompute（todo8-2）：排序结果与"比较器现算键"的参照
// 实现一致；输入切片不变；空/单元素；重复调用确定。
func TestSortedValuesKeyPrecompute(t *testing.T) {
	s, _ := testFuncSnapshotFilesAndProg(t, dispatchReuseFixture())
	vs := fixtureValues(s)
	if len(vs) < 5 {
		t.Fatalf("fixture 值太少: %d", len(vs))
	}
	input := append([]ssa.Value(nil), vs...)
	got := sortedValues(vs)
	if !reflect.DeepEqual(input, vs) {
		t.Error("sortedValues 修改了输入切片")
	}
	// 参照：比较器现算 key（旧实现）
	want := append([]ssa.Value(nil), vs...)
	sortValuesByKeyRef(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("排序结果与参照不一致: %d vs %d", len(got), len(want))
	}
	for i := 1; i < len(got); i++ {
		if valueOrderKey(got[i-1]) > valueOrderKey(got[i]) {
			t.Fatalf("未按确定键升序: [%d] > [%d]", i-1, i)
		}
	}
	if again := sortedValues(vs); !reflect.DeepEqual(got, again) {
		t.Error("重复排序结果不一致")
	}
	if got := sortedValues(nil); len(got) != 0 {
		t.Errorf("nil 输入应返回空: %d", len(got))
	}
	if got := sortedValues(vs[:1]); len(got) != 1 || got[0] != vs[0] {
		t.Error("单元素排序错误")
	}
}

// sortValuesByKeyRef 旧实现（比较器每次现算 key）。
func sortValuesByKeyRef(vs []ssa.Value) {
	for i := 1; i < len(vs); i++ {
		for j := i; j > 0 && valueOrderKey(vs[j]) < valueOrderKey(vs[j-1]); j-- {
			vs[j], vs[j-1] = vs[j-1], vs[j]
		}
	}
}

// TestSortedValueKeysSameOrder：map 键排序与 sortedValues(复制) 一致。
func TestSortedValueKeysSameOrder(t *testing.T) {
	s, _ := testFuncSnapshotFilesAndProg(t, dispatchReuseFixture())
	m := map[ssa.Value]bool{}
	for _, v := range fixtureValues(s) {
		m[v] = true
	}
	got := sortedValueKeys(m)
	want := sortedValues(fixtureValues(s))
	// 去重后比较：m 的键集合
	if len(got) != len(m) {
		t.Fatalf("sortedValueKeys = %d, want %d", len(got), len(m))
	}
	for i := 1; i < len(got); i++ {
		if valueOrderKey(got[i-1]) > valueOrderKey(got[i]) {
			t.Fatalf("sortedValueKeys 未按确定键升序")
		}
	}
	_ = want
}

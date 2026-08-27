package ssa

// R100 待办9：fallback 明细去重——相同 函数:路径 多次失败只打一行
// （×N 聚合）；写访问明细补全（S1 只记读——newFieldAccess 不加明细）。

import (
	"bytes"
	"strings"
	"testing"
)

// TestFallbackAggDedup：聚合器单元——同 key 多次 add 只出一条 ×N，
// 不同 key 独立；total 全量计数。
func TestFallbackAggDedup(t *testing.T) {
	a := newFallbackAgg()
	a.add("f", "x.A", 3)
	a.add("f", "x.A", 4)
	a.add("f", "x.A", 5)
	a.add("f", "y.B", 9)
	a.add("g", "x.A", 1)
	if n := a.total(); n != 5 {
		t.Errorf("total = %d; want 5", n)
	}
	ds := a.dump()
	if len(ds) != 3 {
		t.Fatalf("唯一明细 = %d 条; want 3: %v", len(ds), ds)
	}
	if !strings.Contains(ds[0], "f: x.A") || !strings.Contains(ds[0], "（共 3 次）") || !strings.Contains(ds[0], "行 3") {
		t.Errorf("x.A 应聚合为一行 ×3（首行 3）: %s", ds[0])
	}
	if !strings.Contains(ds[1], "f: y.B") || !strings.Contains(ds[1], "（共 1 次）") {
		t.Errorf("y.B 独立一行: %s", ds[1])
	}
	if !strings.Contains(ds[2], "g: x.A") {
		t.Errorf("跨函数同路径独立: %s", ds[2])
	}
}

// TestFallbackDetailDedup：端到端——匿名 struct 字段访问（写×2 + 读×1
// 同路径 + 另一路径）→ 明细去重打印（每路径一行 ×N），汇总警告保留。
func TestFallbackDetailDedup(t *testing.T) {
	var buf bytes.Buffer
	old := stderr
	stderr = &buf
	defer func() { stderr = old }()
	indexFixture(t, map[string]string{
		"go.mod": moduleGoMod,
		"main.go": `package m

func f() {
	x := struct{ A int }{}
	x.A = 1
	x.A = 2
	_ = x.A
	y := struct{ B int }{}
	y.B = 1
}
`,
	})
	out := buf.String()
	if !strings.Contains(out, "4 个字段访问静态类型解析失败") {
		t.Errorf("汇总警告缺失:\n%s", out)
	}
	// 写访问也进明细（S1 只记读的缺口补全）
	if !strings.Contains(out, "f: x.A") || !strings.Contains(out, "（共 3 次）") {
		t.Errorf("x.A 应聚合去重为一行 ×3（写+读）:\n%s", out)
	}
	if !strings.Contains(out, "f: y.B") || !strings.Contains(out, "（共 1 次）") {
		t.Errorf("y.B 明细缺失:\n%s", out)
	}
	// 去重：x.A 三条访问只打一行
	if strings.Count(out, "f: x.A") != 1 {
		t.Errorf("x.A 明细应只有一行（去重）:\n%s", out)
	}
}

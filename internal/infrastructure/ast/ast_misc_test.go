package ast

import (
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestNestedArgExternalCallee：P2-2——外层 callee 是外部包函数时，
// 参数位置的嵌套调用仍建 passes_result（fmt.Errorf("%v", joinIDs(x))
// → joinIDs 有入边，unused 不误报）。
func TestNestedArgExternalCallee(t *testing.T) {
	_, facts := indexFixture(t, map[string]string{
		"go.mod": fixtureGoMod,
		"a.go": `package m

import "fmt"

func joinIDs(ids []int) []int { return ids }

func callNested() {
	_ = fmt.Errorf("%v", joinIDs([]int{1}))
}
`,
	})

	findFact(t, facts, "symbol:go:fmt:Errorf", "symbol:go:example.com/mtest:joinIDs", string(domain.FactPassesResult))
}

// TestEmbeddedPromotedMethodCalled：P2-2 固化——嵌入提升方法调用
// （a.Exec，Exec 由嵌入字段提升）建 calls 边到声明方法 (DB).Exec，
// unused 不误报（§16.2 旧盲区，Selection 解析已解决）。
func TestEmbeddedPromotedMethodCalled(t *testing.T) {
	_, facts := indexFixture(t, map[string]string{
		"go.mod": fixtureGoMod,
		"db/db.go": `package db

type DB struct{}

func (d *DB) Exec(q string) {}
`,
		"a.go": `package m

import "example.com/mtest/db"

type App struct {
	*db.DB
}

func (a *App) Run() {
	a.Exec("select 1")
}
`,
	})
	findFact(t, facts, "symbol:go:example.com/mtest:(App).Run", "symbol:go:example.com/mtest/db:(DB).Exec", string(domain.FactCalls))
}

// TestFuncValueCall：P2-1——函数值赋值盲区收敛：
// f := g; f() 建 calls 边（h→g）；方法值 fn := obj.M; fn() 建边（m→(T).M）。
func TestFuncValueCall(t *testing.T) {
	_, facts := indexFixture(t, map[string]string{
		"go.mod": fixtureGoMod,
		"a.go": `package m

type T struct{}

func (t *T) M() {}

func g() {}

func h() {
	f := g
	f()
}

func callMethodValue() {
	obj := &T{}
	fn := obj.M
	fn()
}
`,
	})
	findFact(t, facts, "symbol:go:example.com/mtest:h", "symbol:go:example.com/mtest:g", string(domain.FactCalls))
	findFact(t, facts, "symbol:go:example.com/mtest:callMethodValue", "symbol:go:example.com/mtest:(T).M", string(domain.FactCalls))
}

// TestPassesResultArgMetadata：passes_result 边带实参下标与参数名
// （Q185：信息栏"实参来源"分组标注具体是哪个实参——outer(inner(1))
// 的 inner 是 outer 第 1 个参数 s）。
func TestPassesResultArgMetadata(t *testing.T) {
	_, facts := indexFixture(t, map[string]string{
		"go.mod": fixtureGoMod,
		"a.go": `package m

func inner(x int) int { return x }

func outer(s string) string { return s }

func callNested() {
	_ = outer(inner(1))
}
`,
	})

	f := findFact(t, facts, "symbol:go:example.com/mtest:outer", "symbol:go:example.com/mtest:inner", string(domain.FactPassesResult))
	if f.Metadata == nil {
		t.Fatalf("passes_result 边应带 metadata（arg_index/arg_name）")
	}
	if idx, ok := f.Metadata["arg_index"]; !ok || idx != 0 {
		t.Errorf("arg_index = %v, want 0（inner 是 outer 第 1 个实参）", f.Metadata["arg_index"])
	}
	if name, ok := f.Metadata["arg_name"]; !ok || name != "s" {
		t.Errorf("arg_name = %v, want s", f.Metadata["arg_name"])
	}
}

// TestFuncSetRegistryGetter：函数集合入参 + 函数集合返回的 getter——
// 实参复合字面量里注册的函数补 getter→函数 的 CALLS 边（不按签名猜测
// 全部候选函数）。
func TestFuncSetRegistryGetter(t *testing.T) {
	_, facts := indexFixture(t, map[string]string{
		"go.mod": fixtureGoMod,
		"a.go": `package m

type V struct{ F func() bool }

func a() bool { return true }

func get(vs []V) []V { return vs }

func call() { _ = get([]V{{F: a}}) }
`,
	})
	// getter → 注册函数
	findFact(t, facts, "symbol:go:example.com/mtest:get", "symbol:go:example.com/mtest:a", string(domain.FactCalls))
	// 当前调用者 → 注册函数（registered_function_value 标记 + 行号）
	f := findFact(t, facts, "symbol:go:example.com/mtest:call", "symbol:go:example.com/mtest:a", string(domain.FactCalls))
	if f.Confidence != 0.8 {
		t.Errorf("调用者补边置信度 = %v; want 0.8", f.Confidence)
	}
	if v, ok := f.Metadata["registered_function_value"]; !ok || v != true {
		t.Errorf("调用者补边应带 registered_function_value 标记: %v", f.Metadata)
	}
	if _, ok := f.Metadata["line_num"]; !ok {
		t.Errorf("调用者补边应带调用行号: %v", f.Metadata)
	}
}

// TestFuncSetRecursiveType 自引用类型（struct 指向自身 + 函数字段）不
// 递归爆栈，且仍能识别函数集合、建 getter→函数 边。
func TestFuncSetRecursiveType(t *testing.T) {
	_, facts := indexFixture(t, map[string]string{
		"go.mod": fixtureGoMod,
		"a.go": `package m

type N struct {
	Next *N
	F    func() bool
}

func a() bool { return true }

func get(vs []N) []N { return vs }

func call() { _ = get([]N{{Next: &N{}, F: a}}) }
`,
	})
	findFact(t, facts, "symbol:go:example.com/mtest:get", "symbol:go:example.com/mtest:a", string(domain.FactCalls))
}

// TestFuncSetPkgRegistry：包级函数值注册表变量（`var registry = []V{{F: a}}`）
// 传入「函数集合入参+返回」的 getter → getter 到每个注册函数补 CALLS 边
// （置信度 0.8）；函数引用去重排序。
func TestFuncSetPkgRegistry(t *testing.T) {
	_, facts := indexFixture(t, map[string]string{
		"go.mod": fixtureGoMod,
		"a.go": `package m

type V struct{ F func() bool }

func a() bool { return true }

func b() bool { return false }

var registry = []V{{F: a}, {F: b}}

func get(vs []V) []V { return vs }

func call() { _ = get(registry) }
`,
	})
	f := findFact(t, facts, "symbol:go:example.com/mtest:get", "symbol:go:example.com/mtest:a", string(domain.FactCalls))
	if f.Confidence != 0.8 {
		t.Errorf("注册表传播边置信度 = %v; want 0.8", f.Confidence)
	}
	findFact(t, facts, "symbol:go:example.com/mtest:get", "symbol:go:example.com/mtest:b", string(domain.FactCalls))
	// 当前调用者 → 每个注册函数（registered_function_value 标记）
	for _, reg := range []string{"a", "b"} {
		cf := findFact(t, facts, "symbol:go:example.com/mtest:call", "symbol:go:example.com/mtest:"+reg, string(domain.FactCalls))
		if v, ok := cf.Metadata["registered_function_value"]; !ok || v != true {
			t.Errorf("caller→%s 应带 registered_function_value 标记: %v", reg, cf.Metadata)
		}
	}
}

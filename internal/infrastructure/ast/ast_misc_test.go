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

// TestFuncSetNestedCallCalls：返回函数切片的嵌套调用（变参展开
// consume(get()...) / 内建 append(list, get()...)）补 caller→getter 的
// CALLS 边（修复 region getter 到上层工厂的断链；passes_result 仍保留）。
func TestFuncSetNestedCallCalls(t *testing.T) {
	_, facts := indexFixture(t, map[string]string{
		"go.mod": fixtureGoMod,
		"a.go": `package m

type V struct{ F func() bool }

func a() bool { return true }

func get() []V { return []V{{F: a}} }

func consume(vs ...V) {}

func viaAppend() { consume(get()...) }

func viaBuiltin() {
	list := []V{}
	list = append(list, get()...)
	_ = list
}
`,
	})
	findFact(t, facts, "symbol:go:example.com/mtest:viaAppend", "symbol:go:example.com/mtest:get", string(domain.FactCalls))
	findFact(t, facts, "symbol:go:example.com/mtest:viaBuiltin", "symbol:go:example.com/mtest:get", string(domain.FactCalls))
	// 返回值关系保留
	findFact(t, facts, "symbol:go:example.com/mtest:consume", "symbol:go:example.com/mtest:get", string(domain.FactPassesResult))
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
	findFact(t, facts, "symbol:go:example.com/mtest:get", "symbol:go:example.com/mtest:a", string(domain.FactCalls))
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

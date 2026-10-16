package ast

import (
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestFuncSetFactoryReturn：工厂函数内部用 getter 注册函数集合并返回——
// 工厂 → 注册函数 CALLS 边（上层可由 call→factory→注册函数 查询到）。
func TestFuncSetFactoryReturn(t *testing.T) {
	_, facts := indexFixture(t, map[string]string{
		"go.mod": fixtureGoMod,
		"a.go": `package m

type V struct{ F func() bool }

func a() bool { return true }

func get(vs []V) []V { return vs }

func factory() []V { return get([]V{{F: a}}) }

func call() { _ = factory() }
`,
	})
	findFact(t, facts, "symbol:go:example.com/mtest:factory", "symbol:go:example.com/mtest:a", string(domain.FactCalls))
	findFact(t, facts, "symbol:go:example.com/mtest:call", "symbol:go:example.com/mtest:factory", string(domain.FactCalls))
}

// TestFuncSetTraversalExec：遍历包级注册表并逐一执行函数值——遍历函数
// 应能识别到每个注册函数（range 元素 v.F() 的调用关系）。
func TestFuncSetTraversalExec(t *testing.T) {
	_, facts := indexFixture(t, map[string]string{
		"go.mod": fixtureGoMod,
		"a.go": `package m

type V struct{ F func() bool }

func a() bool { return true }

func b() bool { return false }

var registry = []V{{F: a}, {F: b}}

func run() {
	for _, v := range registry {
		_ = v.F()
	}
}
`,
	})
	for _, reg := range []string{"a", "b"} {
		f := findFact(t, facts, "symbol:go:example.com/mtest:run", "symbol:go:example.com/mtest:"+reg, string(domain.FactCalls))
		if f.Confidence != 0.8 {
			t.Errorf("run→%s 置信度 = %v, want 0.8", reg, f.Confidence)
		}
	}
}

// TestFuncSetNestedRecursiveContainer：嵌套函数集合容器（[][]V）经
// getter 传递仍能收集到注册函数（递归访问集防爆栈）。
func TestFuncSetNestedRecursiveContainer(t *testing.T) {
	_, facts := indexFixture(t, map[string]string{
		"go.mod": fixtureGoMod,
		"a.go": `package m

type V struct {
	Inner [][]V
	F     func() bool
}

func a() bool { return true }

func get(vs [][]V) [][]V { return vs }

func call() { _ = get([][]V{{{F: a}}}) }
`,
	})
	findFact(t, facts, "symbol:go:example.com/mtest:get", "symbol:go:example.com/mtest:a", string(domain.FactCalls))
}

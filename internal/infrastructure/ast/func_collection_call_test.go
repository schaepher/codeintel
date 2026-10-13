package ast

// 函数切片（function collection）经 Append / 内建 append 传递的 CALLS 边
// 回归（从 ast_misc_test.go 拆出）。

import (
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestFuncCollectionNestedCallCalls：返回函数切片的嵌套调用（变参展开
// consume(get()...) / 内建 append(list, get()...)）补 caller→getter 的
// CALLS 边（修复 region getter 到上层工厂的断链；passes_result 仍保留）。
func TestFuncCollectionNestedCallCalls(t *testing.T) {
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

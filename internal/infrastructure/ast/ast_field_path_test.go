package ast

import (
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestNestedFieldSelectorCall（todo4-3）：嵌套字段选择器链
// （a.B.C.M()）的调用关系能被识别（AST 字段路径 → callee）。
func TestNestedFieldSelectorCall(t *testing.T) {
	_, facts := indexFixture(t, map[string]string{
		"go.mod": fixtureGoMod,
		"a.go": `package m

type C struct{}

func (c *C) M() {}

type B struct{ C *C }

type A struct{ B B }

func f(a A) { a.B.C.M() }
`,
	})
	findFact(t, facts, "symbol:go:example.com/mtest:f", "symbol:go:example.com/mtest:(C).M", string(domain.FactCalls))
}

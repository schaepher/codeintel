package action

// todo23 §3.1：旧索引只有行号时，同一行的两个不同包调用不能串线。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestLookupCallSameLineDifferentPackages(t *testing.T) {
	src := `package m

import (
	a "example.com/a"
	b "example.com/b"
)

func F() {
	a.Foo(); b.Foo()
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var calls []*ast.CallExpr
	ast.Inspect(f, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			if _, ok := c.Fun.(*ast.SelectorExpr); ok {
				calls = append(calls, c)
			}
		}
		return true
	})
	if len(calls) != 2 {
		t.Fatalf("应有 2 个调用，got %d", len(calls))
	}
	line := fset.Position(calls[0].Lparen).Line
	if fset.Position(calls[1].Lparen).Line != line {
		t.Fatalf("夹具应在同一行")
	}
	// 模拟旧索引：只有行号，且被后写的 b 覆盖
	tgts := &seqTargets{
		byLine: map[int]seqTarget{line: {tid: "symbol:go:example.com/b:Foo"}},
		byName: map[string][]seqTarget{
			"Foo": {{tid: "symbol:go:example.com/a:Foo"}, {tid: "symbol:go:example.com/b:Foo"}},
		},
	}
	gotA, okA := tgts.lookupCall(fset.Position(calls[0].Lparen), calls[0].Fun, f, "example.com/m", "", "", nil)
	if !okA || gotA.tid != "symbol:go:example.com/a:Foo" {
		t.Errorf("a.Foo() 应命中 example.com/a:Foo，got %+v ok=%v", gotA, okA)
	}
	gotB, okB := tgts.lookupCall(fset.Position(calls[1].Lparen), calls[1].Fun, f, "example.com/m", "", "", nil)
	if !okB || gotB.tid != "symbol:go:example.com/b:Foo" {
		t.Errorf("b.Foo() 应命中 example.com/b:Foo，got %+v ok=%v", gotB, okB)
	}
}

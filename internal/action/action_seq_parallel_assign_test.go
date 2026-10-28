package action

// todo23 §2.2：平行赋值必须先计算全部 RHS 再更新 LHS（`a,b = b,a` 不能串）。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestSceneParallelSwapUsesOldValues(t *testing.T) {
	src := `package m

func F() {
	x, y := "1", "2"
	x, y = y, x
	_ = x
	_ = y
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var body *ast.BlockStmt
	ast.Inspect(f, func(n ast.Node) bool {
		if fd, ok := n.(*ast.FuncDecl); ok && fd.Name.Name == "F" {
			body = fd.Body
		}
		return true
	})
	if body == nil {
		t.Fatal("找不到 F")
	}
	s := newLitScopes(nil, nil, nil)
	walkReachingStmts(fset, []byte(src), body.List, token.NoPos, s, 0)
	if v, ok := s.lookup("x"); !ok || !v.known || v.val != "2" {
		t.Errorf("swap 后 x 应为 \\\"2\\\"，got %+v", v)
	}
	if v, ok := s.lookup("y"); !ok || !v.known || v.val != "1" {
		t.Errorf("swap 后 y 应为 \\\"1\\\"（旧 x），got %+v", v)
	}
}

func TestFuncValueParallelSwapUsesOldValues(t *testing.T) {
	src := `package m

func A() {}
func B() {}

func Swap() {
	a := A
	b := B
	a, b = b, a
	b()
}
`
	acts, dir := seedDIRepo(t, src, map[string]string{})
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/m:Swap", RepoAbs: dir, Depth: 3})
	if err != nil {
		t.Fatalf("CodeSequence: %v", err)
	}
	nodes := seqCallNodesNamed(root, "b")
	if len(nodes) != 1 {
		t.Fatalf("应有 1 个 b() 节点，got %d", len(nodes))
	}
	ev := nodes[0].Evidence
	if ev == nil || ev.SymbolID != "symbol:go:example.com/m:A" {
		t.Errorf("swap 后 b() 应调用旧 a=A，got %+v", ev)
	}
}

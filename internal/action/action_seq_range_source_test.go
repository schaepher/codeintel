package action

// todo24 §11.5：普通数据 range 低层回归——只读回调来源保持、取址/未知 range
// 赋值失效、非函数列表不按元素展开。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func parseRangeBody(t *testing.T, src string) (*token.FileSet, *ast.File, *ast.RangeStmt) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var rs *ast.RangeStmt
	ast.Inspect(f, func(n ast.Node) bool {
		if r, ok := n.(*ast.RangeStmt); ok && rs == nil {
			rs = r
		}
		return true
	})
	if rs == nil {
		t.Fatal("夹具缺少 range")
	}
	return fset, f, rs
}

func TestMergePossibleKeepsReadOnlyValue(t *testing.T) {
	// 只读回调：循环体没有改写 f → 循环后的来源保持不变（不是多候选）。
	fset, f, _ := parseRangeBody(t, `package m
func F(f func()) {
	for _, x := range []int{1, 2} {
		_ = x
		f()
	}
	f()
}`)
	var fd *ast.FuncDecl
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok {
			fd = fn
		}
	}
	_ = fset
	// 构造 env：f 绑定到字面量，pre 是同一值的克隆。
	var lit *ast.FuncLit
	ast.Inspect(fd, func(n ast.Node) bool {
		if l, ok := n.(*ast.FuncLit); ok && lit == nil {
			lit = l
		}
		return true
	})
	env := newSeqEnv(nil)
	paramObj := fd.Type.Params.List[0].Names[0].Obj
	v := literalSeqValue(CodeSequenceRequest{}, nil, env, &ast.FuncLit{Type: &ast.FuncType{}}, seqOriginVar)
	env.set(paramObj, v)
	pre := env.clone()
	env.mergePossible(pre)
	got, ok := env.lookup(paramObj)
	if !ok || got.kind != seqValueLit {
		t.Fatalf("只读回调应保持原来源，got %+v", got)
	}
}

func TestInvalidateAddrTaken(t *testing.T) {
	fset, f, _ := parseRangeBody(t, `package m
func F(f func()) {
	for _, x := range []int{1} {
		_ = x
		Replace(&f)
	}
}
func Replace(*func()) {}`)
	_ = fset
	var fd *ast.FuncDecl
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == "F" {
			fd = fn
		}
	}
	paramObj := fd.Type.Params.List[0].Names[0].Obj
	env := newSeqEnv(nil)
	env.set(paramObj, namedSeqValue("symbol:go:example.com/m:Sink"))
	req := CodeSequenceRequest{env: env}
	var call *ast.CallExpr
	ast.Inspect(fd, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok && call == nil {
			call = c
		}
		return true
	})
	seqInvalidateAddrTaken(req, call)
	if v, ok := env.lookup(paramObj); !ok || v.kind != seqValueUnknown {
		t.Fatalf("取址实参后函数槽应降级为未知，got %+v", v)
	}
}

func TestInvalidateRangeFuncSlots(t *testing.T) {
	_, f, rs := parseRangeBody(t, `package m
func F(f func()) {
	for _, f = range others {
		f()
	}
}
var others []func()`)
	_ = f
	var fd *ast.FuncDecl
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok {
			fd = fn
		}
	}
	paramObj := fd.Type.Params.List[0].Names[0].Obj
	env := newSeqEnv(nil)
	env.set(paramObj, namedSeqValue("symbol:go:example.com/m:Sink"))
	req := CodeSequenceRequest{env: env}
	seqInvalidateRangeFuncSlots(req, rs)
	if v, ok := env.lookup(paramObj); !ok || v.kind != seqValueUnknown {
		t.Fatalf("range 赋值目标的函数槽应失效，got %+v", v)
	}
}

func TestSeqListHoldsFuncValuesRejectsDataList(t *testing.T) {
	fset, f, _ := parseRangeBody(t, `package m
func F(steps []int) {
	for _, s := range steps {
		_ = s
	}
}`)
	_ = fset
	// 普通数据列表（元素为 unknown）不得当函数列表。
	dataList := listSeqValue([]seqValue{unknownSeqValue("int 字面量"), unknownSeqValue("int 字面量")})
	req := CodeSequenceRequest{curCtx: &seqFileCtx{file: f}}
	var rangeExpr ast.Expr
	ast.Inspect(f, func(n ast.Node) bool {
		if r, ok := n.(*ast.RangeStmt); ok {
			rangeExpr = r.X
		}
		return true
	})
	if seqListHoldsFuncValues(New(nil), req, rangeExpr, dataList) {
		t.Error("普通数据列表不应被当作函数列表")
	}
	funcList := listSeqValue([]seqValue{namedSeqValue("symbol:go:example.com/m:Sink")})
	if !seqListHoldsFuncValues(New(nil), req, rangeExpr, funcList) {
		t.Error("函数值列表应被接受")
	}
}

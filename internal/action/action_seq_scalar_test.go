package action

// U04c 单测：字面量规范化、入口参数来源身份、逃逸后赋值不恢复来源。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestSeqBasicLitOriginNormalization(t *testing.T) {
	cases := []struct{ expr, typ, lit string }{
		{`"a\tb"`, "string", `"a\tb"`},
		{`0x4`, "int", "4"},
		{`1_0`, "int", "10"},
		{`'a'`, "rune", `'a'`},
		{`1.50`, "float64", "1.5"},
	}
	for _, c := range cases {
		e, err := parser.ParseExpr(c.expr)
		if err != nil {
			t.Fatal(err)
		}
		lit, ok := e.(*ast.BasicLit)
		if !ok {
			t.Fatalf("%s 不是字面量", c.expr)
		}
		o := seqBasicLitOrigin(lit)
		if o == nil || o.Kind != SeqOriginLiteral || o.ScalarType != c.typ || o.Literal != c.lit {
			t.Fatalf("%s → %+v（want type=%s literal=%s）", c.expr, o, c.typ, c.lit)
		}
	}
}

// TestSeqBindEntryParamOriginsEntryOnly 只有本次查询入口的形参才是 parameter
// 来源（其它入口的同名参数不能借用）。
func TestSeqBindEntryParamOriginsEntryOnly(t *testing.T) {
	const src = `package flow
func Entry(id string, kind int) {}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "flow.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var fn *ast.FuncDecl
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == "Entry" {
			fn = fd
		}
	}
	req := CodeSequenceRequest{RepoAbs: "/repo", entryID: "symbol:go:example.com/flow:Entry"}

	env := newSeqEnv(nil)
	seqBindEntryParamOrigins(req, env, fn.Type.Params, fset, []byte(src),
		"symbol:go:example.com/flow:Other")
	if len(env.scalars) != 0 {
		t.Fatalf("非入口符号不得绑定 parameter 来源: %+v", env.scalars)
	}

	env = newSeqEnv(nil)
	seqBindEntryParamOrigins(req, env, fn.Type.Params, fset, []byte(src),
		"symbol:go:example.com/flow:Entry")
	if len(env.scalars) != 2 {
		t.Fatalf("入口参数应有 2 个来源: %+v", env.scalars)
	}
	var slot seqScalarSlot
	for _, s := range env.scalars {
		if s.origin != nil && s.origin.ScalarType == "int" {
			slot = s
		}
	}
	if slot.origin == nil || slot.origin.Kind != SeqOriginParameter ||
		slot.origin.Parameter == nil || *slot.origin.Parameter != 1 ||
		slot.origin.Symbol != "symbol:go:example.com/flow:Entry" || slot.origin.Repo != "/repo" {
		t.Fatalf("第二个参数来源: %+v", slot.origin)
	}
}

// TestSeqScalarEscapePersists 取址后赋值不能恢复来源。
func TestSeqScalarEscapePersists(t *testing.T) {
	env := newSeqEnv(nil)
	obj := &ast.Object{Kind: ast.Var, Name: "id"}
	env.setScalar(obj, seqOriginParameter("symbol:go:example.com/flow:Entry", "/repo", "string", 0))
	if slot, ok := env.scalarSlot(obj); !ok || slot.origin == nil {
		t.Fatalf("初始来源缺失: %+v", slot)
	}
	env.markScalarEscaped(obj)
	env.setScalar(obj, seqOriginParameter("symbol:go:example.com/flow:Entry", "/repo", "string", 0))
	if slot, _ := env.scalarSlot(obj); !slot.escaped {
		t.Fatalf("逃逸标记丢失: %+v", slot)
	}
	if o, _ := env.scalarOriginAt(obj, 10); o != nil {
		t.Fatalf("逃逸后的读取不得恢复来源: %+v", o)
	}
	// 克隆保留逃逸标记。
	clone := env.clone()
	if slot, _ := clone.scalarSlot(obj); !slot.escaped {
		t.Fatalf("clone 丢失逃逸标记: %+v", slot)
	}
}

// TestSeqScalarBranchMergeSame 分支来源相同保留、不同未知。
func TestSeqScalarBranchMergeSame(t *testing.T) {
	base := newSeqEnv(nil)
	obj := &ast.Object{Kind: ast.Var, Name: "alias"}
	src := seqOriginParameter("symbol:go:example.com/flow:Entry", "/repo", "string", 0)

	b1 := base.clone()
	b1.setScalar(obj, src)
	b2 := base.clone()
	b2.setScalar(obj, src)
	base.mergeScalars([]*seqEnv{b1, b2})
	if slot, _ := base.scalarSlot(obj); slot.origin == nil || slot.origin.Kind != SeqOriginParameter {
		t.Fatalf("相同来源应保留: %+v", slot)
	}

	base = newSeqEnv(nil)
	b1 = base.clone()
	b1.setScalar(obj, src)
	b2 = base.clone()
	b2.setScalar(obj, seqBasicLitOriginTest(`"other"`))
	base.mergeScalars([]*seqEnv{b1, b2})
	if slot, _ := base.scalarSlot(obj); slot.origin != nil {
		t.Fatalf("不同来源应未知: %+v", slot)
	}
}

func seqBasicLitOriginTest(expr string) *SeqStorageOrigin {
	e, err := parser.ParseExpr(expr)
	if err != nil {
		panic(err)
	}
	return seqBasicLitOrigin(e.(*ast.BasicLit))
}

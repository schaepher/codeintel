package action

// todo23 §2.4：命名函数类型 / 类型别名 / 跨包导入 / 循环类型识别。

import (
	"go/ast"
	"os"
	"path/filepath"
	"testing"
)

func firstArrayTypeOf(t *testing.T, f *ast.File, name string) ast.Expr {
	t.Helper()
	var found ast.Expr
	ast.Inspect(f, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok || len(vs.Names) != 1 || vs.Names[0].Name != name {
			return true
		}
		found = vs.Type
		return false
	})
	if found == nil {
		t.Fatalf("找不到 %s 的声明类型", name)
	}
	return found
}

func TestSeqEmptyFuncSliceNamedTypes(t *testing.T) {
	src := `package m

type Check func()

type Alias = Check

type C1 C2

type C2 C1

var named []Check
var aliased []Alias
var cyclic []C1
var ints []int
`
	fset, f := parseSeqFile(t, src)
	ctx := &seqFileCtx{fset: fset, file: f, src: []byte(src), rel: "main.go"}
	req := CodeSequenceRequest{curCtx: ctx, curSym: "symbol:go:example.com/m:Entry",
		RepoAbs: t.TempDir(), extPkgs: map[string]*seqExternalPkg{}}
	acts := &Actions{}
	cases := []struct {
		name string
		want bool
	}{
		{"named", true}, {"aliased", true}, {"cyclic", false}, {"ints", false},
	}
	for _, c := range cases {
		got := seqEmptyFuncSliceTypeIn(acts, req, ctx, firstArrayTypeOf(t, f, c.name))
		if got != c.want {
			t.Errorf("%s: seqEmptyFuncSliceTypeIn = %v，期望 %v", c.name, got, c.want)
		}
	}
	src2 := src + "\nfunc Use() {\n\tvar check Alias\n\t_ = check\n}\n"
	fset2, f2 := parseSeqFile(t, src2)
	ctx2 := &seqFileCtx{fset: fset2, file: f2, src: []byte(src2), rel: "main.go"}
	// 直接对声明类型判断（`var check Alias`）
	req2 := req
	req2.curCtx = ctx2
	if !seqIsFuncTypeIn(acts, req2, ctx2, parseTypeOf(t, f2, "check"), 0) {
		t.Errorf("var check Alias 应识别为函数类型（类型别名）")
	}
}

// parseTypeOf 取 `var name Type` 的 Type 表达式。
func parseTypeOf(t *testing.T, f *ast.File, name string) ast.Expr {
	t.Helper()
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		var out ast.Expr
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			vs, ok := n.(*ast.ValueSpec)
			if ok && len(vs.Names) == 1 && vs.Names[0].Name == name && vs.Type != nil {
				out = vs.Type
				return false
			}
			return true
		})
		if out != nil {
			return out
		}
	}
	t.Fatalf("找不到 var %s 的声明类型", name)
	return nil
}

func TestSeqEmptyFuncSliceCrossPackage(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "app")
	dep := filepath.Join(root, "dep")
	for _, d := range []string{app, dep} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p, s string) {
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(dep, "go.mod"), "module example.com/dep\n\ngo 1.21\n")
	write(filepath.Join(dep, "dep.go"), "package dep\n\ntype Check func()\n")
	write(filepath.Join(app, "go.mod"), "module example.com/app\n\ngo 1.21\n\nrequire example.com/dep v0.0.0\n\nreplace example.com/dep => ../dep\n")
	appSrc := `package app

import "example.com/dep"

var checks []dep.Check
`
	write(filepath.Join(app, "main.go"), appSrc)
	fset, f := parseSeqFile(t, appSrc)
	ctx := &seqFileCtx{fset: fset, file: f, src: []byte(appSrc), rel: "main.go"}
	req := CodeSequenceRequest{RepoAbs: app, curCtx: ctx,
		curSym: "symbol:go:example.com/app:Entry", extPkgs: map[string]*seqExternalPkg{}}
	if !seqEmptyFuncSliceTypeIn(New(nil), req, ctx, firstArrayTypeOf(t, f, "checks")) {
		t.Errorf("[]dep.Check 应识别为函数列表（跨包命名类型）")
	}
}

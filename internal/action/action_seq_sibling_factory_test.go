package action

// todo24 §11.2：同包兄弟文件的裸函数名工厂返回类型（低层回归）。
//
// 规则：当前文件有同名声明 → 只分析该声明；没有才按同包目录定位兄弟文件；
// 多重/冲突声明（非法 Go）与缺失工厂保守返回 unknown；AST 对象证明为局部
// 函数值/类型时不得回退到同名包函数。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"
)

// siblingFactoryReq 用一组源文件构建最小请求（只需 RepoAbs/curFile/缓存）。
func siblingFactoryReq(t *testing.T, files map[string]string, entry string) (CodeSequenceRequest, *astFilePair) {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fset := token.NewFileSet()
	src, err := os.ReadFile(filepath.Join(dir, entry))
	if err != nil {
		t.Fatal(err)
	}
	f, err := parser.ParseFile(fset, entry, src, 0)
	if err != nil {
		t.Fatal(err)
	}
	req := CodeSequenceRequest{RepoAbs: dir, curFile: entry,
		curCtx:    &seqFileCtx{fset: fset, file: f, src: src, rel: entry},
		fileCache: map[string]*seqParsedFile{entry: {fset: fset, file: f, src: src}},
	}
	return req, &astFilePair{f: f, fset: fset, src: src}
}

type astFilePair struct {
	f    *ast.File
	fset *token.FileSet
	src  []byte
}

func TestFuncReturnTypesSiblingFile(t *testing.T) {
	req, ap := siblingFactoryReq(t, map[string]string{
		"flow.go":    "package flow\n\nfunc Bridge() API { return Build() }\n",
		"factory.go": "package flow\n\ntype Impl struct{}\n\nfunc Build() *Impl { return &Impl{} }\n",
	}, "flow.go")
	got := funcReturnTypes(New(nil), req, ap.f, importAliases(ap.f), "Build", 3, "example.com/flow")
	if len(got) != 1 || got[0].typeName != "Impl" || got[0].pkgPath != "example.com/flow" {
		t.Fatalf("兄弟文件工厂应解析到 Impl，got %+v", got)
	}
}

func TestFuncReturnTypesCurrentFileWins(t *testing.T) {
	req, ap := siblingFactoryReq(t, map[string]string{
		"flow.go":    "package flow\n\ntype Local struct{}\n\nfunc Build() *Local { return &Local{} }\n",
		"factory.go": "package flow\n\ntype Impl struct{}\n\nfunc Build() *Impl { return &Impl{} }\n",
	}, "flow.go")
	got := funcReturnTypes(New(nil), req, ap.f, importAliases(ap.f), "Build", 3, "example.com/flow")
	if len(got) != 1 || got[0].typeName != "Local" {
		t.Fatalf("当前文件有声明时只分析该声明，got %+v", got)
	}
}

func TestFuncReturnTypesSiblingConflictUnknown(t *testing.T) {
	req, ap := siblingFactoryReq(t, map[string]string{
		"flow.go": "package flow\n\nfunc Bridge() API { return Build() }\n",
		"a.go":    "package flow\n\ntype A struct{}\n\nfunc Build() *A { return &A{} }\n",
		"b.go":    "package flow\n\ntype B struct{}\n\nfunc Build() *B { return &B{} }\n",
	}, "flow.go")
	if got := funcReturnTypes(New(nil), req, ap.f, importAliases(ap.f), "Build", 3, "example.com/flow"); len(got) != 0 {
		t.Fatalf("兄弟文件同名冲突应保守返回 unknown，got %+v", got)
	}
}

func TestFuncReturnTypesMissingFactory(t *testing.T) {
	req, ap := siblingFactoryReq(t, map[string]string{
		"flow.go": "package flow\n\nfunc Bridge() API { return Missing() }\n",
	}, "flow.go")
	if got := funcReturnTypes(New(nil), req, ap.f, importAliases(ap.f), "Missing", 3, "example.com/flow"); len(got) != 0 {
		t.Fatalf("缺失工厂应返回 unknown，got %+v", got)
	}
}

func TestCallReturnTypesShadowedIdentNotGuessed(t *testing.T) {
	req, ap := siblingFactoryReq(t, map[string]string{
		"flow.go":    "package flow\n\nvar runtimeFactory func() API\n\nfunc Bridge() API { Build := runtimeFactory; return Build() }\n",
		"factory.go": "package flow\n\ntype Impl struct{}\n\nfunc Build() *Impl { return &Impl{} }\n",
	}, "flow.go")
	// 找到 Bridge 里的 `Build()` 调用并解析其返回类型——局部函数值遮蔽时
	// 不得回退到兄弟文件的 Build。
	var callExpr *ast.CallExpr
	ast.Inspect(ap.f, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			if id, ok := c.Fun.(*ast.Ident); ok && id.Name == "Build" {
				callExpr = c
			}
		}
		return true
	})
	if callExpr == nil {
		t.Fatal("夹具缺少 Build() 调用")
	}
	got := callReturnTypes(New(nil), req, ap.f, importAliases(ap.f), callExpr.Fun, 3, "example.com/flow")
	if len(got) != 0 {
		t.Fatalf("局部函数值遮蔽时不得按名字猜包函数返回类型，got %+v", got)
	}
}

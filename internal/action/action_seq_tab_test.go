package action

// S3 测试：多行条件 label 的 tab 应替换为空格（mermaid 渲染不出）；
// 字符串字面量 \t 转义序列（反斜杠+t）不含真实 tab 不受影响。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func TestSeqLabelTabClean(t *testing.T) {
	src := "package m\n\nfunc run() {\n\tif a &&\n\t\tb {\n\t\treturn\n\t}\n}\n"
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "main.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var cond ast.Expr
	ast.Inspect(f, func(n ast.Node) bool {
		if is, ok := n.(*ast.IfStmt); ok {
			cond = is.Cond
			return false
		}
		return true
	})
	label := seqExprText(fset, []byte(src), cond)
	if strings.Contains(label, "\t") {
		t.Errorf("label 应无 tab（多行条件合并成一行）: %q", label)
	}
	if !strings.Contains(label, "b") {
		t.Errorf("label 应含续行条件 b: %q", label)
	}
}

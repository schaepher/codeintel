package action

// todo23 §3.1：receiver 字段数据流必须绑定**当前 receiver 对象**，
// 不能只比较变量名（其它同名/局部变量的 x.field 不借用 receiver 证明）。

import (
	"go/ast"
	"testing"
)

func TestReceiverFieldSelRequiresReceiverObject(t *testing.T) {
	src := `package m

type T struct{ f *M }

type M struct{}

func (m *M) Do() {}

func (t *T) Run() {
	x := t
	x.f.Do()
	t.f.Do()
}
`
	_, f := parseSeqFile(t, src)
	var recvObj *ast.Object
	var found []struct {
		base string
		sel  *ast.SelectorExpr
	}
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Name.Name != "Run" || fd.Recv == nil {
			continue
		}
		recvObj = fd.Recv.List[0].Names[0].Obj
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Do" {
				return true
			}
			if outer, ok := sel.X.(*ast.SelectorExpr); ok {
				if id, ok := outer.X.(*ast.Ident); ok {
					found = append(found, struct {
						base string
						sel  *ast.SelectorExpr
					}{base: id.Name, sel: sel})
				}
			}
			return true
		})
	}
	if recvObj == nil || len(found) != 2 {
		t.Fatalf("夹具解析失败: recvObj=%v calls=%d", recvObj, len(found))
	}
	for _, c := range found {
		field, ok := receiverFieldSel(c.sel, recvObj, "t")
		switch c.base {
		case "x":
			if ok {
				t.Errorf("局部变量 x.f 不应借用 receiver 字段证明（field=%q）", field)
			}
		case "t":
			if !ok || field != "f" {
				t.Errorf("receiver t.f 应命中字段 f，got %q ok=%v", field, ok)
			}
		}
	}
}

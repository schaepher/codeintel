package action

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
	"go/ast"
	"go/parser"
	"go/token"
)

func TestDbgU06(t *testing.T) {
	dir := "/home/schaepher/Codes/ana/.tmp/todo26/e-u06"
	db, _ := sqlite.Open(dir)
	defer db.Close()
	a := New(sqlite.NewRepo(db))
	t.Logf("isIface((API).Work)=%v isIface((One).Work)=%v", a.isIfaceMethodID("symbol:go:example.com/flow:(API).Work"), a.isIfaceMethodID("symbol:go:example.com/flow:(One).Work"))
	src, _ := os.ReadFile(filepath.Join(dir, "flow.go"))
	fs := token.NewFileSet()
	f, _ := parser.ParseFile(fs, "flow.go", src, 0)
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Name.Name != "Entry" { continue }
		var call *ast.CallExpr
		ast.Inspect(fd.Body, func(n ast.Node) bool { if c, ok := n.(*ast.CallExpr); ok { call = c }; return true })
		sel := call.Fun.(*ast.SelectorExpr)
		recvObj := fd.Recv.List[0].Names[0].Obj
		t.Logf("recvObj=%p name=%s", recvObj, fd.Recv.List[0].Names[0].Name)
		field, ok := receiverFieldSel(sel, recvObj, "s")
		t.Logf("receiverFieldSel ok=%v field=%q", ok, field)
		t.Logf("impls=%v", a.receiverFieldImpls(CodeSequenceRequest{RepoAbs: dir}, "Service", "biz", "Work"))
	}
}

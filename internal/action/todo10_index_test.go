package action

// todo10-8 夹具的最小索引器（fixture 级近似，不依赖 scip-go）：
// go/parser 建节点 + 同包 calls 边 + 字段读写摘要。从
// todo10_fixtures_test.go 拆出（行数治理）。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// indexTodo10Fixture 用 go/parser 建最小索引（节点 + 同包 calls 边）——
// 夹具测试不依赖 scip-go，也不写入仓库目录（先复制到临时目录）。
// 返回 (标签仓库目录, actions)。
func indexTodo10Fixture(t *testing.T, name string) (string, *Actions) {
	t.Helper()
	srcDir := filepath.Join("testdata", "todo10", name)
	dst := t.TempDir()
	for _, fn := range []string{"go.mod", "flow.go"} {
		b, err := os.ReadFile(filepath.Join(srcDir, fn))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dst, fn), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sqlite.Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filepath.Join(dst, "flow.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	modPath := "example.com/fx/" + name
	idOf := func(recv, name string) string {
		if recv != "" {
			return "symbol:go:" + modPath + ":(" + recv + ")." + name
		}
		return "symbol:go:" + modPath + ":" + name
	}
	// 1) 节点 + 方法接收者
	type fnInfo struct {
		id   string
		name string
		recv string
		line int
		body *ast.BlockStmt
		decl *ast.FuncDecl
	}
	var fns []fnInfo
	all := map[string]string{} // 短名/方法名 → ID（同包解析）
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		recv := ""
		if fd.Recv != nil && len(fd.Recv.List) > 0 {
			recv = strings.TrimPrefix(strings.TrimPrefix(exprTypeName(fd.Recv.List[0].Type), ""), "")
			recv = strings.TrimLeft(exprTypeName(fd.Recv.List[0].Type), "*")
		}
		id := idOf(recv, fd.Name.Name)
		fns = append(fns, fnInfo{id: id, name: fd.Name.Name, recv: recv, line: fset.Position(fd.Pos()).Line, body: fd.Body, decl: fd})
		if recv == "" {
			all[fd.Name.Name] = id
		} else {
			all[fd.Name.Name] = id // 方法按短名兜底（夹具无重名）
		}
	}
	nodes := make([]*domain.CodeEntity, 0, len(fns))
	for _, fn := range fns {
		kind := domain.KindFunction
		if fn.recv != "" {
			kind = domain.KindMethod
		}
		nodes = append(nodes, &domain.CodeEntity{ID: domain.CanonicalID(fn.id), Kind: kind,
			Name: fn.name, FilePath: "flow.go", LineStart: fn.line})
	}
	// 2) calls 边（同包：Ident / pkg-less SelectorExpr 的 Sel 名）
	var facts []*domain.Fact
	for _, fn := range fns {
		ast.Inspect(fn.body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := ""
			switch fun := call.Fun.(type) {
			case *ast.Ident:
				name = fun.Name
			case *ast.SelectorExpr:
				name = fun.Sel.Name
			}
			target, ok := all[name]
			if !ok || target == fn.id {
				return true
			}
			pos := fset.Position(call.Lparen)
			facts = append(facts, &domain.Fact{
				SourceID: domain.CanonicalID(fn.id), TargetID: domain.CanonicalID(target),
				Kind: domain.FactCalls, ToolSource: domain.ToolCodeGraph, Confidence: 0.8,
				Metadata: map[string]any{"line_num": float64(pos.Line), "pos": float64(pos.Offset)},
			})
			return true
		})
	}
	// 3) 字段读写摘要（fixture 级近似：按 selector 名收 direct_write/read，
	// 路径用 <module>.<Field>——供 --focus / 异步因果判据使用）
	var sums []*domain.FunctionFieldSummary
	for _, fn := range fns {
		fields := fixtureFieldAccess(fn.body)
		for w, line := range fields.writes {
			sums = append(sums, &domain.FunctionFieldSummary{
				FunctionID: domain.CanonicalID(fn.id), AccessKind: domain.SummaryDirectWrite,
				FieldPath: modPath + "." + w, LineStart: line,
			})
		}
		for rd := range fields.reads {
			sums = append(sums, &domain.FunctionFieldSummary{
				FunctionID: domain.CanonicalID(fn.id), AccessKind: domain.SummaryDirectRead,
				FieldPath: modPath + "." + rd, LineStart: fields.reads[rd],
			})
		}
	}
	if _, err := r.SaveBatchStats(nodes, facts, sums); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(&domain.BuildMeta{BuildID: "fx-" + name, ToolName: "fixture", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	return dst, New(r)
}

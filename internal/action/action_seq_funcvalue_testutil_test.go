package action

// todo22 §2（第一增量）测试基建：真实源码 + 索引节点 + 调用边（pos=左括号
// 偏移，与 AST 适配器口径一致），以及 JSON/树节点查找辅助。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// seedFuncValueRepo 写真实源码 + 索引节点 + 调用边。
// calls 把"调用表达式文本（callLabel）"映射到目标 canonical ID；同名字面量
// 不会被当成调用（只在可执行位置出现时才算）。边的 metadata pos 用左括号
// 偏移（与 AST 适配器一致），line_num 用调用行。
func seedFuncValueRepo(t *testing.T, src string, calls map[string]string) (*Actions, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "flow.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "flow.go", src, 0)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	var nodes []*domain.CodeEntity
	var edges []*domain.Fact
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		caller := "symbol:go:example.com/m:" + fd.Name.Name
		if fd.Recv != nil && len(fd.Recv.List) > 0 {
			caller = "symbol:go:example.com/m:(" + recvTypeName(fd.Recv.List[0].Type) + ")." + fd.Name.Name
		}
		nodes = append(nodes, &domain.CodeEntity{
			ID: domain.CanonicalID(caller), Kind: domain.KindFunction, Name: fd.Name.Name,
			FilePath: "flow.go", LineStart: fset.Position(fd.Pos()).Line,
		})
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			label := callLabel(fset, []byte(src), call.Fun)
			target, ok := calls[label]
			if !ok {
				return true
			}
			edges = append(edges, &domain.Fact{
				SourceID: domain.CanonicalID(caller), TargetID: domain.CanonicalID(target),
				Kind: domain.FactCalls, ToolSource: domain.ToolCodeGraph, Confidence: 0.8,
				Metadata: map[string]any{
					"line_num": fset.Position(call.Lparen).Line,
					"pos":      fset.Position(call.Lparen).Offset,
				},
			})
			return true
		})
	}
	if _, err := r.SaveBatchStats(nodes, edges, nil); err != nil {
		t.Fatalf("save: %v", err)
	}
	return New(r), dir
}

// seqFindNodes 收集满足条件的节点（含嵌套与 Else 分支）。
func seqFindNodes(root *CodeSeqNode, pred func(*CodeSeqNode) bool) []*CodeSeqNode {
	var out []*CodeSeqNode
	var walk func(n *CodeSeqNode)
	walk = func(n *CodeSeqNode) {
		if n == nil {
			return
		}
		if pred(n) {
			out = append(out, n)
		}
		for _, c := range n.Nodes {
			walk(c)
		}
		for _, c := range n.Else {
			walk(c)
		}
	}
	walk(root)
	return out
}

// seqCallNodesNamed 收集 label 匹配的调用节点。
func seqCallNodesNamed(root *CodeSeqNode, label string) []*CodeSeqNode {
	return seqFindNodes(root, func(n *CodeSeqNode) bool {
		return n.Kind == "call" && n.Label == label
	})
}

// writeSeqFixture 只写源码 + go.mod（接口类夹具自建节点/边时用）。
func writeSeqFixture(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "flow.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// parseSeqFile 解析夹具源码（返回 fset 与 AST）。
func parseSeqFile(t *testing.T, src string) (*token.FileSet, *ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "flow.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	return fset, f
}

// seedSeqFuncValueRepoInDir 用 seedFuncValueRepo 的索引逻辑，但夹具是已建好的
// 目录（外部依赖测试需要 go.mod replace）。
func seedSeqFuncValueRepoInDir(t *testing.T, appSrc string) (*Actions, string) {
	t.Helper()
	root := t.TempDir()
	app := filepath.Join(root, "app")
	ext := filepath.Join(root, "ext")
	for _, d := range []string{app, ext} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	gomod := "module example.com/app\n\ngo 1.21\n\nrequire example.com/ext v0.0.0\n\nreplace example.com/ext => ../ext\n"
	if err := os.WriteFile(filepath.Join(app, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "flow.go"), []byte(appSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ext, "go.mod"), []byte("module example.com/ext\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ext, "ext.go"), []byte(extRunSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	// 索引：仅为 app 包内的函数建节点（外部包不进索引，模拟真实情况）
	db, err := sqlite.Open(app)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	fset, f := parseSeqFile(t, appSrc)
	var nodes []*domain.CodeEntity
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		nodes = append(nodes, &domain.CodeEntity{
			ID:   domain.CanonicalID("symbol:go:example.com/app:" + fd.Name.Name),
			Kind: domain.KindFunction, Name: fd.Name.Name, FilePath: "flow.go",
			LineStart: fset.Position(fd.Pos()).Line,
		})
	}
	if _, err := r.SaveBatchStats(nodes, nil, nil); err != nil {
		t.Fatalf("save: %v", err)
	}
	return New(r), app
}

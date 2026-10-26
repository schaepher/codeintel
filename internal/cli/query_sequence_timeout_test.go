package cli

// todo12-17 R3：查询预算——超时取消展开并失败退出（不返回看似完整的成功）。

import (
	"fmt"
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

func TestSequenceQueryTimeoutBudget(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var src strings.Builder
	src.WriteString("package m\n\n")
	const n = 120
	for i := 0; i < n; i++ {
		src.WriteString(fmt.Sprintf("func F%d() {\n", i))
		if i+1 < n {
			src.WriteString(fmt.Sprintf("\tF%d()\n", i+1))
		}
		src.WriteString("}\n\n")
	}
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte(src.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var nodes []*domain.CodeEntity
	var facts []*domain.Fact
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		id := domain.CanonicalID("symbol:go:example.com/m:" + fd.Name.Name)
		nodes = append(nodes, &domain.CodeEntity{ID: id, Kind: domain.KindFunction,
			Name: fd.Name.Name, FilePath: "main.go", LineStart: fset.Position(fd.Pos()).Line})
		ast.Inspect(fd.Body, func(x ast.Node) bool {
			call, ok := x.(*ast.CallExpr)
			if !ok {
				return true
			}
			if fun, ok := call.Fun.(*ast.Ident); ok {
				facts = append(facts, &domain.Fact{SourceID: id,
					TargetID: domain.CanonicalID("symbol:go:example.com/m:" + fun.Name),
					Kind: domain.FactCalls, Confidence: 0.8,
					Metadata: map[string]any{"line_num": fset.Position(call.Pos()).Line}})
			}
			return true
		})
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := sqlite.NewRepo(db).SaveBatchStats(nodes, facts, nil); err != nil {
		t.Fatal(err)
	}
	if err := sqlite.NewRepo(db).Save(&domain.BuildMeta{BuildID: "timeout", ToolName: "test", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	code := 0
	out := captureStdout(func() {
		code = cmdQuery([]string{"sequence", "symbol:go:example.com/m:F0", "--repo", dir,
			"--code", "--depth", "200", "--timeout", "1us", "--json"})
	})
	if code == 0 {
		t.Fatalf("超预算查询应失败退出（不得输出完整结果）:\n%s", out)
	}
}

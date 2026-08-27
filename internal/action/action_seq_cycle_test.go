package action

import "os"
import "path/filepath"
import "testing"
import "github.com/schaepher/codeintel/internal/domain"
import "github.com/schaepher/codeintel/internal/infrastructure/sqlite"

// TestCodeSequenceCycleGuard：递归自环（函数调自身）——路径防环——
// 嵌套展开不无限深入（visited 路径语义；depth 5 也只展开一层）。
func TestCodeSequenceCycleGuard(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `package m

func recurse(n int) int {
	if n <= 0 {
		return 0
	}
	return recurse(n - 1)
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:recurse", Kind: domain.KindFunction, Name: "recurse", FilePath: "main.go", LineStart: 3},
	}, []*domain.Fact{
		{SourceID: "symbol:go:example.com/m:recurse", TargetID: "symbol:go:example.com/m:recurse",
			Kind: domain.FactCalls, Confidence: 1.0, Metadata: map[string]any{"line_num": 7}},
	}, nil); err != nil {
		t.Fatal(err)
	}
	acts := New(sqlite.NewRepo(db))
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "recurse", RepoAbs: dir, Depth: 5})
	if err != nil {
		t.Fatal(err)
	}
	// 展开一层：自调用子节点存在但不再嵌套展开（防环）
	var walk func(n *CodeSeqNode, depth int)
	maxDepth := 0
	walk = func(n *CodeSeqNode, depth int) {
		if depth > maxDepth {
			maxDepth = depth
		}
		for _, c := range n.Nodes {
			walk(c, depth+1)
		}
	}
	walk(root, 0)
	// ≤2：branch 内 S2 裸 return 节点占一层——递归调用本身不再展开
	if maxDepth > 2 {
		t.Errorf("递归自环应防环（maxDepth = %d; want ≤2——子调用不再展开，return 节点占一层）", maxDepth)
	}
}

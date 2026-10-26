package action

// todo12-13：展开停止、焦点过滤与索引降级诊断保持完整。
//   - 未解析调用点必须有终点诊断（AST 表达式存在不等于实现已解析）
//   - 焦点/场景投影诊断与"原分析诊断"分开列出；筛选不顶替 depth/unknown

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

func seedSeqDiagProjRepo(t *testing.T, src string, nodes []*domain.CodeEntity, facts []*domain.Fact) (string, *Actions) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	if _, err := r.SaveBatchStats(nodes, facts, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(&domain.BuildMeta{BuildID: "diagproj", ToolName: "test", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	return dir, New(r)
}

func hasDiag(root *CodeSeqNode, code string) *SeqDiagCount {
	if root == nil || root.Meta == nil {
		return nil
	}
	for i := range root.Meta.Diagnostics {
		if root.Meta.Diagnostics[i].Code == code {
			return &root.Meta.Diagnostics[i]
		}
	}
	return nil
}

// 未解析调用点：AST 存在但目标未解析 → unresolved 终点诊断。
func TestSeqDiagUnresolvedCall(t *testing.T) {
	src := `package m

func Wrap() int {
	v := ext.Load()
	return v
}
`
	dir, acts := seedSeqDiagProjRepo(t, src, []*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:Wrap", Kind: domain.KindFunction, Name: "Wrap",
			FilePath: "main.go", LineStart: 3},
	}, nil)
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "symbol:go:example.com/m:Wrap",
		RepoAbs: dir, Depth: 2})
	if err != nil || root == nil {
		t.Fatalf("CodeSequence %v root=%v", err, root)
	}
	var found *CodeSeqNode
	walkSeqTree(root.Nodes, func(n *CodeSeqNode) {
		if n.Kind == "call" && strings.Contains(n.Label, "ext.Load") {
			found = n
		}
	})
	if found == nil {
		t.Fatalf("应输出未解析调用节点: %+v", root.Nodes)
	}
	if found.Stop == nil || found.Stop.Code != SeqStopUnresolved {
		t.Errorf("未解析调用应有 unresolved 终点诊断: %+v", found)
	}
	if hasDiag(root, SeqStopUnresolved) == nil {
		t.Errorf("meta 诊断应聚合 unresolved: %+v", root.Meta.Diagnostics)
	}
}

func walkSeqTree(nodes []*CodeSeqNode, fn func(*CodeSeqNode)) {
	for _, n := range nodes {
		fn(n)
		walkSeqTree(n.Nodes, fn)
		walkSeqTree(n.Else, fn)
	}
}

// 焦点投影：原 unresolved 诊断保留，另给 focus_projection。
func TestSeqDiagFocusKeepsAnalysis(t *testing.T) {
	src := `package m

func Wrap() int {
	v := ext.Load()
	return v
}
`
	dir, acts := seedSeqDiagProjRepo(t, src, []*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:Wrap", Kind: domain.KindFunction, Name: "Wrap",
			FilePath: "main.go", LineStart: 3},
	}, nil)
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "symbol:go:example.com/m:Wrap",
		RepoAbs: dir, Depth: 2, Focus: []string{"return"}})
	if err != nil || root == nil {
		t.Fatalf("CodeSequence %v root=%v", err, root)
	}
	if hasDiag(root, SeqStopUnresolved) == nil {
		t.Errorf("焦点投影不得顶替原 unresolved 诊断: %+v", root.Meta.Diagnostics)
	}
	if d := hasDiag(root, SeqDiagFocusProjection); d == nil || !strings.Contains(d.Detail, "原分析诊断保留") {
		t.Errorf("应另列焦点投影诊断: %+v", root.Meta.Diagnostics)
	}
}

// depth 诊断在加焦点前后都保留。
func TestSeqDiagDepthKeptAcrossFocus(t *testing.T) {
	src := `package m

func Wrap() int {
	v := inner()
	return v
}

func inner() int {
	return leaf()
}

func leaf() int { return 1 }
`
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:Wrap", Kind: domain.KindFunction, Name: "Wrap", FilePath: "main.go", LineStart: 3},
		{ID: "symbol:go:example.com/m:inner", Kind: domain.KindFunction, Name: "inner", FilePath: "main.go", LineStart: 8},
		{ID: "symbol:go:example.com/m:leaf", Kind: domain.KindFunction, Name: "leaf", FilePath: "main.go", LineStart: 12},
	}
	facts := []*domain.Fact{
		{SourceID: "symbol:go:example.com/m:Wrap", TargetID: "symbol:go:example.com/m:inner",
			Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": 4}},
		{SourceID: "symbol:go:example.com/m:inner", TargetID: "symbol:go:example.com/m:leaf",
			Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": 9}},
	}
	dir, acts := seedSeqDiagProjRepo(t, src, nodes, facts)
	plain, err := acts.CodeSequence(CodeSequenceRequest{Target: "symbol:go:example.com/m:Wrap",
		RepoAbs: dir, Depth: 2})
	if err != nil || plain == nil {
		t.Fatalf("plain %v root=%v", err, plain)
	}
	if hasDiag(plain, SeqStopDepthLimit) == nil {
		t.Fatalf("depth 截断应进诊断: %+v", plain.Meta.Diagnostics)
	}
	focused, err := acts.CodeSequence(CodeSequenceRequest{Target: "symbol:go:example.com/m:Wrap",
		RepoAbs: dir, Depth: 2, Focus: []string{"return"}})
	if err != nil || focused == nil {
		t.Fatalf("focused %v root=%v", err, focused)
	}
	if hasDiag(focused, SeqStopDepthLimit) == nil {
		t.Errorf("焦点筛选不得让原 depth 诊断消失: %+v", focused.Meta.Diagnostics)
	}
	if hasDiag(focused, SeqDiagFocusProjection) == nil {
		t.Errorf("应另列焦点投影诊断: %+v", focused.Meta.Diagnostics)
	}
}

// 索引降级：build success 不是覆盖完整——degraded 必须在查询诊断里可见。
func TestSeqDiagIndexDegraded(t *testing.T) {
	src := `package m

func Wrap() int { return 1 }
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:Wrap", Kind: domain.KindFunction, Name: "Wrap",
			FilePath: "main.go", LineStart: 3},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(&domain.BuildMeta{BuildID: "deg1", ToolName: "test",
		Status: domain.BuildDegraded, CommitSHA: "abc123",
		ErrorMsg: "ssa: type check failed", DegradeStats: `{"sql_ast_ok":1,"sql_ast_fail":2}`}); err != nil {
		t.Fatal(err)
	}
	root, err := New(r).CodeSequence(CodeSequenceRequest{Target: "symbol:go:example.com/m:Wrap",
		RepoAbs: dir, Depth: 1})
	if err != nil || root == nil {
		t.Fatalf("CodeSequence %v root=%v", err, root)
	}
	if root.Meta.Coverage == nil || root.Meta.Coverage.BuildStatus != string(domain.BuildDegraded) ||
		root.Meta.Coverage.IndexSHA != "abc123" {
		t.Fatalf("应携带索引覆盖信息: %+v", root.Meta.Coverage)
	}
	if root.Meta.Coverage.DegradeStats["sql_ast_fail"] != float64(2) {
		t.Errorf("降级统计应与 SQL 解析分开透传: %+v", root.Meta.Coverage.DegradeStats)
	}
	d := hasDiag(root, "index_degraded")
	if d == nil || !strings.Contains(d.Detail, "type check failed") {
		t.Errorf("降级构建应给 index_degraded 诊断: %+v", root.Meta.Diagnostics)
	}
}

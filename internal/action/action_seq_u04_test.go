package action

// U04：跨入口审核流程的边界——持久化身份/顺序未证明时必须作为边界输出，
// 不凭空把 Submit 的 CreateNode 连到后续 FinishReview 人工节点。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

const u04Src = `package flow

import (
	"context"
	"database/sql"
)

var DB *sql.DB

func CreateNode(ctx context.Context, id, kind string) {
	DB.ExecContext(ctx, "INSERT INTO nodes(id,kind) VALUES(?,?)", id, kind)
}

func Submit(ctx context.Context, id string) { CreateNode(ctx, id, "profile") }

func Complete(string) {}

func FinishReview(ctx context.Context, id string) {
	var kind string
	if err := DB.QueryRowContext(ctx, "SELECT kind FROM nodes WHERE id=?", id).Scan(&kind); err != nil {
		return
	}
	if kind == "profile" {
		CreateNode(ctx, id+"-service", "service")
	} else {
		Complete(id)
	}
}
`

func seedU04(t *testing.T) (*Actions, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/flow\n\ngo 1.26.2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "flow.go"), []byte(u04Src), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	pkg := "symbol:go:example.com/flow:"
	line := func(prefix string) int {
		for i, l := range strings.Split(u04Src, "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), prefix) {
				return i + 1
			}
		}
		return 0
	}
	nodes := []*domain.CodeEntity{
		{ID: domain.CanonicalID(pkg + "CreateNode"), Kind: domain.KindFunction, Name: "CreateNode",
			FilePath: "flow.go", LineStart: line("func CreateNode(")},
		{ID: domain.CanonicalID(pkg + "Submit"), Kind: domain.KindFunction, Name: "Submit",
			FilePath: "flow.go", LineStart: line("func Submit(")},
		{ID: domain.CanonicalID(pkg + "Complete"), Kind: domain.KindFunction, Name: "Complete",
			FilePath: "flow.go", LineStart: line("func Complete(")},
		{ID: domain.CanonicalID(pkg + "FinishReview"), Kind: domain.KindFunction, Name: "FinishReview",
			FilePath: "flow.go", LineStart: line("func FinishReview(")},
		{ID: "symbol:go:database/sql:(DB).ExecContext", Kind: domain.KindMethod, Name: "(DB).ExecContext"},
		{ID: "symbol:go:database/sql:(DB).QueryRowContext", Kind: domain.KindMethod, Name: "(DB).QueryRowContext"},
		{ID: "symbol:go:database/sql:(Row).Scan", Kind: domain.KindMethod, Name: "(Row).Scan"},
	}
	facts := []*domain.Fact{
		{SourceID: domain.CanonicalID(pkg + "Submit"), TargetID: domain.CanonicalID(pkg + "CreateNode"),
			Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": line("func Submit(")}},
		{SourceID: domain.CanonicalID(pkg + "CreateNode"),
			TargetID: "symbol:go:database/sql:(DB).ExecContext",
			Kind:     domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": line("DB.ExecContext(")}},
		{SourceID: domain.CanonicalID(pkg + "FinishReview"),
			TargetID: "symbol:go:database/sql:(DB).QueryRowContext",
			Kind:     domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": line("if err := DB.QueryRowContext(")}},
		{SourceID: domain.CanonicalID(pkg + "FinishReview"),
			TargetID: "symbol:go:database/sql:(Row).Scan",
			Kind:     domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": line("if err := DB.QueryRowContext(")}},
		{SourceID: domain.CanonicalID(pkg + "FinishReview"), TargetID: domain.CanonicalID(pkg + "CreateNode"),
			Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": line("CreateNode(ctx, id+\"-service\"")}},
		{SourceID: domain.CanonicalID(pkg + "FinishReview"), TargetID: domain.CanonicalID(pkg + "Complete"),
			Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": line("Complete(id)")}},
	}
	if _, err := r.SaveBatchStats(nodes, facts, nil); err != nil {
		t.Fatalf("save: %v", err)
	}
	return New(r), dir
}

// Submit 只输出自己的持久化边界（有表/列、无跨入口顺序）；不得渲染 FinishReview。
func TestU04SubmitPersistenceBoundaryOnly(t *testing.T) {
	acts, dir := seedU04(t)
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/flow:Submit", RepoAbs: dir, Depth: 6})
	if err != nil {
		t.Fatal(err)
	}
	calls := seqFindNodes(root, func(n *CodeSeqNode) bool {
		return n.Kind == "call" && strings.Contains(n.Label, "ExecContext")
	})
	if len(calls) != 1 {
		t.Fatalf("Submit 应展开到 1 个持久化调用，got %d: %v", len(calls), labelsOf(root.Nodes))
	}
	n := calls[0]
	if n.Stop == nil || !strings.Contains(n.Stop.Detail, "持久化边界") ||
		!strings.Contains(n.Stop.Detail, "INSERT INTO nodes(id,kind)") {
		t.Fatalf("持久化边界应写清表/列: %+v", n.Stop)
	}
	if !strings.Contains(n.Stop.Detail, "未证明") {
		t.Errorf("必须明示值来源/跨入口顺序未证明: %q", n.Stop.Detail)
	}
	// 不凭空连接后续人工审核
	if got := len(seqFindNodes(root, func(n *CodeSeqNode) bool {
		return n.Kind == "call" && n.Label == "FinishReview"
	})); got != 0 {
		t.Errorf("Submit 树里不得凭配置/名称连出 FinishReview（got %d）", got)
	}
}

// FinishReview 的 kind 分支必须保留条件与互斥目标（profile → 建服务节点 /
// 其他 → Complete），且不能从 Submit 推断执行次数。
func TestU04FinishReviewKindBranches(t *testing.T) {
	acts, dir := seedU04(t)
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/flow:FinishReview", RepoAbs: dir, Depth: 6})
	if err != nil {
		t.Fatal(err)
	}
	// 读取身份：SELECT kind FROM nodes WHERE id=?（QueryRowContext/Scan 节点保留）
	hasQuery := false
	seqFindNodes(root, func(n *CodeSeqNode) bool {
		if n.Kind == "call" && strings.Contains(n.Label, "QueryRowContext") {
			hasQuery = true
		}
		return false
	})
	if !hasQuery {
		t.Errorf("应保留持久化读取调用: %v", labelsOf(root.Nodes))
	}
	// 两个互斥分支：建服务节点 / Complete
	if got := len(seqCallNodesNamed(root, "CreateNode")); got != 1 {
		t.Errorf("kind==profile 分支应建服务节点（CreateNode=%d）", got)
	}
	if got := len(seqCallNodesNamed(root, "Complete")); got != 1 {
		t.Errorf("else 分支应 Complete（got %d）", got)
	}
	branches := seqFindNodes(root, func(n *CodeSeqNode) bool {
		return n.Kind == "branch" && strings.Contains(n.Label, "kind")
	})
	if len(branches) != 1 {
		t.Errorf("应保留 kind 条件分支: %v", labelsOf(root.Nodes))
	}
	// 两个人工审核节点 → 2 次 FinishReview 需要请求身份/顺序证明；本夹具没有，
	// 因此只允许输出边界，不伪造次数。
	for _, n := range seqFindNodes(root, func(n *CodeSeqNode) bool { return n.Kind == "call" }) {
		if n.Label == "FinishReview" { // 入口节点本身
			continue
		}
		if strings.Contains(n.Label, "FinishReview") {
			t.Errorf("入口树内不得自调用 FinishReview: %s", n.Label)
		}
	}
}

package action

// todo12-9：sequence 的 return/字段焦点闭合必要值来源。
//   - --focus return / --focus NodeStatus 都保留 status 初始化、条件改写、
//     必要 Review 依赖，排除 Audit
//   - 不存在/歧义焦点报错；合法但无写入焦点不报错

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

const focusWrapSrc = `package m

type ReviewInfo struct { DefaultStatus string }
type Handler struct {
	NodeStatus string
	Unused     string
}

func Review(info ReviewInfo) string { return info.DefaultStatus }
func Audit() {}
func Unlock() {}
func Notify() {}

func (h *Handler) Wrap(rejected bool) (string, error) {
	defer Unlock()
	go Notify()
	reviewStatus := Review(ReviewInfo{DefaultStatus: "pending"})
	status := "not_started"
	if rejected && reviewStatus != "" {
		status = "rejected"
	}
	h.NodeStatus = status
	Audit()
	return status, nil
}
`

func seedFocusWrapRepo(t *testing.T) (*Actions, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(focusWrapSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:Handler", Kind: domain.KindStruct, Name: "Handler",
			FilePath: "main.go", LineStart: 4,
			Properties: map[string]any{"fields": []map[string]any{
				{"name": "NodeStatus"}, {"name": "Unused"}}}},
		{ID: "symbol:go:example.com/m:(Handler).Wrap", Kind: domain.KindMethod, Name: "(Handler).Wrap",
			FilePath: "main.go", LineStart: 14},
		{ID: "symbol:go:example.com/m:Review", Kind: domain.KindFunction, Name: "Review",
			FilePath: "main.go", LineStart: 9},
		{ID: "symbol:go:example.com/m:Audit", Kind: domain.KindFunction, Name: "Audit",
			FilePath: "main.go", LineStart: 10},
		{ID: "symbol:go:example.com/m:Unlock", Kind: domain.KindFunction, Name: "Unlock",
			FilePath: "main.go", LineStart: 11},
		{ID: "symbol:go:example.com/m:Notify", Kind: domain.KindFunction, Name: "Notify",
			FilePath: "main.go", LineStart: 12},
	}
	facts := []*domain.Fact{
		{SourceID: "symbol:go:example.com/m:(Handler).Wrap", TargetID: "symbol:go:example.com/m:Review",
			Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": 17}},
		{SourceID: "symbol:go:example.com/m:(Handler).Wrap", TargetID: "symbol:go:example.com/m:Audit",
			Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": 23}},
		{SourceID: "symbol:go:example.com/m:(Handler).Wrap", TargetID: "symbol:go:example.com/m:Unlock",
			Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": 15}},
		{SourceID: "symbol:go:example.com/m:(Handler).Wrap", TargetID: "symbol:go:example.com/m:Notify",
			Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": 16}},
	}
	fields := []*domain.FunctionFieldSummary{
		{FunctionID: "symbol:go:example.com/m:(Handler).Wrap", AccessKind: domain.SummaryDirectWrite,
			FieldPath: "example.com/m.Handler.NodeStatus", LineStart: 22},
	}
	if _, err := r.SaveBatchStats(nodes, facts, fields); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(&domain.BuildMeta{BuildID: "focus1", ToolName: "test", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	return New(r), dir
}

func collectFocusNodes(root *CodeSeqNode) (assigns, calls, returns []string) {
	var walk func(nodes []*CodeSeqNode)
	walk = func(nodes []*CodeSeqNode) {
		for _, n := range nodes {
			switch n.Kind {
			case "assign":
				assigns = append(assigns, n.Label)
			case "call":
				calls = append(calls, n.Label)
			case "return":
				returns = append(returns, strings.Join(n.Values, ","))
			}
			walk(n.Nodes)
			walk(n.Else)
		}
	}
	walk(root.Nodes)
	return
}

func assertFocusClosure(t *testing.T, root *CodeSeqNode, mode string) {
	t.Helper()
	assigns, calls, _ := collectFocusNodes(root)
	joined := strings.Join(assigns, " | ")
	for _, want := range []string{
		`reviewStatus := Review(ReviewInfo{DefaultStatus: "pending"})`,
		`status := "not_started"`,
		`status = "rejected"`,
		"h.NodeStatus = status",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("%s 应保留依赖赋值 %q，实为 %v", mode, want, assigns)
		}
	}
	for _, c := range calls {
		if c == "Audit" {
			t.Errorf("%s 不得包含无关调用 Audit: %v", mode, calls)
		}
	}
	hasReview := false
	for _, c := range calls {
		if strings.Contains(c, "Review") {
			hasReview = true
		}
	}
	if !hasReview {
		t.Errorf("%s 应保留必要 Review 依赖: %v", mode, calls)
	}
}

// A1：return 焦点闭合 status 初始化/条件改写/Review，排除 Audit。
func TestSeqFocusReturnClosure(t *testing.T) {
	acts, dir := seedFocusWrapRepo(t)
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/m:(Handler).Wrap", RepoAbs: dir, Depth: 2, Focus: []string{"return"}})
	if err != nil || root == nil {
		t.Fatalf("CodeSequence %v root=%v", err, root)
	}
	assertFocusClosure(t, root, "return")
	_, _, returns := collectFocusNodes(root)
	if len(returns) == 0 {
		t.Errorf("return 焦点应保留 return 节点")
	}
}

// A1：NodeStatus 焦点同样闭合来源链（排除 Audit）。
func TestSeqFocusFieldClosure(t *testing.T) {
	acts, dir := seedFocusWrapRepo(t)
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/m:(Handler).Wrap", RepoAbs: dir, Depth: 2, Focus: []string{"NodeStatus"}})
	if err != nil || root == nil {
		t.Fatalf("CodeSequence %v root=%v", err, root)
	}
	assertFocusClosure(t, root, "NodeStatus")
}

// A3：不存在焦点报错；合法但无写入焦点正常返回。
func TestSeqFocusValidation(t *testing.T) {
	acts, dir := seedFocusWrapRepo(t)
	if _, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/m:(Handler).Wrap", RepoAbs: dir, Depth: 1,
		Focus: []string{"No.SuchField"}}); err == nil || !strings.Contains(err.Error(), "不存在") {
		t.Errorf("不存在焦点应报错: %v", err)
	}
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/m:(Handler).Wrap", RepoAbs: dir, Depth: 1,
		Focus: []string{"Unused"}})
	if err != nil || root == nil {
		t.Fatalf("合法但无写入焦点不应报错: %v root=%v", err, root)
	}
}

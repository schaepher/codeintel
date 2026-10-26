package action

// todo13-9：--focus return 沿具体别名/实例保留影响返回的被调写入。
//   - 保留 first 实例定义 → alias=first → AssignStatus(alias,"wanted") 与其写入
//   - 排除 AssignStatus(second,"unrelated")（对返回无影响）
//   - 保留既有本地到达定义/类型身份/非法焦点报错

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

const focusReturnAliasSrc = `package m

type Handler struct{ NodeStatus string }

func AssignStatus(h *Handler, status string) {
	h.NodeStatus = status
}

func AliasPair() string {
	first, second := &Handler{}, &Handler{}
	alias := first
	AssignStatus(alias, "wanted")
	AssignStatus(second, "unrelated")
	return alias.NodeStatus
}
`

func seedFocusReturnAlias(t *testing.T) (*Actions, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(focusReturnAliasSrc), 0o644); err != nil {
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
			FilePath: "main.go", LineStart: 3,
			Properties: map[string]any{"fields": []map[string]any{{"name": "NodeStatus"}}}},
		{ID: "symbol:go:example.com/m:AssignStatus", Kind: domain.KindFunction, Name: "AssignStatus",
			FilePath: "main.go", LineStart: 5},
		{ID: "symbol:go:example.com/m:AliasPair", Kind: domain.KindFunction, Name: "AliasPair",
			FilePath: "main.go", LineStart: 9},
	}
	facts := []*domain.Fact{
		{SourceID: "symbol:go:example.com/m:AliasPair", TargetID: "symbol:go:example.com/m:AssignStatus",
			Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": 12}},
	}
	if _, err := r.SaveBatchStats(nodes, facts, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(&domain.BuildMeta{BuildID: "focusreturn", ToolName: "test", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	return New(r), dir
}

func TestSeqFocusReturnAliasWrites(t *testing.T) {
	acts, dir := seedFocusReturnAlias(t)
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/m:AliasPair", RepoAbs: dir, Depth: 3,
		Focus: []string{"return"}})
	if err != nil || root == nil {
		t.Fatalf("CodeSequence %v root=%v", err, root)
	}
	assigns, calls, returns := collectFocusNodes(root)
	if len(returns) == 0 {
		t.Errorf("应保留 return alias.NodeStatus: %+v", returns)
	}
	nAssign := 0
	for _, c := range calls {
		if strings.Contains(c, "AssignStatus") {
			nAssign++
		}
	}
	if nAssign != 1 {
		t.Errorf("应只保留影响返回的那次 AssignStatus 调用，实为 %d: %v", nAssign, calls)
	}
	joinedAssigns := strings.Join(assigns, " | ")
	if !strings.Contains(joinedAssigns, "alias := first") {
		t.Errorf("应保留 alias=first 的别名依据: %v", assigns)
	}
	if !strings.Contains(joinedAssigns, "first, second := ") {
		t.Errorf("应保留 first 实例定义: %v", assigns)
	}
	if !strings.Contains(joinedAssigns, `h.NodeStatus = status`) {
		t.Errorf("应保留被调函数内的实际写入: %v", assigns)
	}
	if root.Meta.Focus == nil || !root.Meta.Focus.Applied {
		t.Errorf("焦点应生效: %+v", root.Meta.Focus)
	}

	// 无焦点对照仍能看到两个 AssignStatus（未回归既有输出）
	plain, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/m:AliasPair", RepoAbs: dir, Depth: 3})
	if err != nil || plain == nil {
		t.Fatalf("CodeSequence plain: %v", err)
	}
	_, plainCalls, _ := collectFocusNodes(plain)
	nPlain := 0
	for _, c := range plainCalls {
		if strings.Contains(c, "AssignStatus") {
			nPlain++
		}
	}
	if nPlain != 2 {
		t.Errorf("无焦点应看到两个 AssignStatus 调用: %v", plainCalls)
	}
}

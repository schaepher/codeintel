package action

// todo12-9 R2：焦点依赖的类型身份/别名边界。
//   - 不同类型同名字段不串线（全路径焦点只命中目标类型）
//   - 类型未知的同名字段保守保留并标 partial
//   - 跨函数别名（Apply(a) 写 a.State）保留调用与其实际写点

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

const focusAliasSrc = `package m

type A struct{ State string }
type B struct{ State string }

func get() *A { return &A{} }

func Apply(a *A) {
	a.State = "applied"
}

func Wrap(a *A, b *B) {
	Apply(a)
	a.State = "a"
	b.State = "b"
}
`

func TestSeqFocusTypeIdentityAndAlias(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(focusAliasSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:A", Kind: domain.KindStruct, Name: "A", FilePath: "main.go", LineStart: 3,
			Properties: map[string]any{"fields": []map[string]any{{"name": "State"}}}},
		{ID: "symbol:go:example.com/m:B", Kind: domain.KindStruct, Name: "B", FilePath: "main.go", LineStart: 4,
			Properties: map[string]any{"fields": []map[string]any{{"name": "State"}}}},
		{ID: "symbol:go:example.com/m:get", Kind: domain.KindFunction, Name: "get", FilePath: "main.go", LineStart: 6},
		{ID: "symbol:go:example.com/m:Apply", Kind: domain.KindFunction, Name: "Apply", FilePath: "main.go", LineStart: 8},
		{ID: "symbol:go:example.com/m:Wrap", Kind: domain.KindFunction, Name: "Wrap", FilePath: "main.go", LineStart: 12},
	}
	facts := []*domain.Fact{
		{SourceID: "symbol:go:example.com/m:Wrap", TargetID: "symbol:go:example.com/m:Apply",
			Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": 13}},
	}
	if _, err := r.SaveBatchStats(nodes, facts, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(&domain.BuildMeta{BuildID: "focusalias", ToolName: "test", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	acts := New(r)

	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/m:Wrap", RepoAbs: dir, Depth: 2,
		Focus: []string{"example.com/m.A.State"}})
	if err != nil || root == nil {
		t.Fatalf("CodeSequence %v root=%v", err, root)
	}
	assigns, calls, _ := collectFocusNodes(root)
	joined := strings.Join(assigns, " | ")
	if !strings.Contains(joined, "a.State = \"a\"") {
		t.Errorf("应保留目标类型 A 的写入: %v", assigns)
	}
	if strings.Contains(joined, "b.State = \"b\"") {
		t.Errorf("不同类型同名字段不得串线（B.State）: %v", assigns)
	}
	// 跨函数别名：Apply(a) 调用与其内部 a.State 写入都要保留
	hasApply := false
	for _, c := range calls {
		if strings.Contains(c, "Apply") {
			hasApply = true
		}
	}
	if !hasApply {
		t.Errorf("焦点依赖应保留被调 Apply 调用: %v", calls)
	}
	calleeAssign := false
	for _, a := range assigns {
		if strings.Contains(a, "a.State = \"applied\"") {
			calleeAssign = true
		}
	}
	if !calleeAssign {
		t.Errorf("被调函数内的实际写点应保留: %v", assigns)
	}
	if root.Meta.Focus == nil || root.Meta.Focus.Partial {
		t.Errorf("类型可判定时不应 partial: %+v", root.Meta.Focus)
	}

	// 类型未知：x := get(); x.State = "u" → 保守保留 + partial
	src2 := `package m

type A struct{ State string }

func get() *A { return &A{} }

func Unknown() {
	x := get()
	x.State = "u"
}
`
	dir2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir2, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir2, "main.go"), []byte(src2), 0o644); err != nil {
		t.Fatal(err)
	}
	db2, err := sqlite.Open(dir2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db2.Close() })
	r2 := sqlite.NewRepo(db2)
	if _, err := r2.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:A", Kind: domain.KindStruct, Name: "A", FilePath: "main.go", LineStart: 3,
			Properties: map[string]any{"fields": []map[string]any{{"name": "State"}}}},
		{ID: "symbol:go:example.com/m:get", Kind: domain.KindFunction, Name: "get", FilePath: "main.go", LineStart: 5},
		{ID: "symbol:go:example.com/m:Unknown", Kind: domain.KindFunction, Name: "Unknown", FilePath: "main.go", LineStart: 7},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := r2.Save(&domain.BuildMeta{BuildID: "focusunknown", ToolName: "test", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	u, err := New(r2).CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/m:Unknown", RepoAbs: dir2, Depth: 1,
		Focus: []string{"example.com/m.A.State"}})
	if err != nil || u == nil {
		t.Fatalf("Unknown CodeSequence %v root=%v", err, u)
	}
	uassigns, _, _ := collectFocusNodes(u)
	if !strings.Contains(strings.Join(uassigns, " | "), "x.State = \"u\"") {
		t.Errorf("类型未知的写入应保守保留: %v", uassigns)
	}
	if u.Meta.Focus == nil || !u.Meta.Focus.Partial {
		t.Errorf("类型未知应标 focus partial: %+v", u.Meta.Focus)
	}
	found := false
	for _, d := range u.Meta.Diagnostics {
		if d.Code == "focus_dependency_unresolved" {
			found = true
		}
	}
	if !found {
		t.Errorf("应有 focus_dependency_unresolved 诊断: %+v", u.Meta.Diagnostics)
	}
}

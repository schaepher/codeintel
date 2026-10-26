package cli

// todo12-11：Mermaid 保留结构化时序中的必要赋值（状态初始化/条件改写）。
// JSON/文本/mermaid 三个消费者使用同一节点切片；不得出现空 alt。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

const mermaidAssignSrc = `package m

func Wrap(rejected bool) (string, error) {
	status := "not_started"
	if rejected {
		status = "rejected"
	}
	return status, nil
}
`

func seedMermaidAssignRepo(t *testing.T) (string, *action.Actions) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(mermaidAssignSrc), 0o644); err != nil {
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
	if err := r.Save(&domain.BuildMeta{BuildID: "mermaid-assign", ToolName: "test", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	return dir, action.New(r)
}

// A1：三格式对照——两个状态赋值、条件与返回都在。
func TestSequenceMermaidKeepsAssign(t *testing.T) {
	dir, acts := seedMermaidAssignRepo(t)
	root, err := acts.CodeSequence(action.CodeSequenceRequest{
		Target: "symbol:go:example.com/m:Wrap", RepoAbs: dir, Depth: 1,
		Focus: []string{"status", "return"}})
	if err != nil || root == nil {
		t.Fatalf("CodeSequence %v root=%v", err, root)
	}
	m := renderCodeSeqMermaid(root)
	for _, want := range []string{
		`status := "not_started"`,
		`status = "rejected"`,
		"alt rejected",
		"return status, nil",
	} {
		if !strings.Contains(m, want) {
			t.Errorf("mermaid 应含 %q:\n%s", want, m)
		}
	}
	// 语法：alt/end 成对，且 alt 与对应 end 之间不能为空
	lines := strings.Split(m, "\n")
	alt := 0
	emptyAlt := false
	for i, ln := range lines {
		if strings.HasPrefix(strings.TrimSpace(ln), "alt ") {
			alt++
			// 下一条（跳过注释/空行）必须是内容而非 end/else
			j := i + 1
			for j < len(lines) && strings.TrimSpace(lines[j]) == "" {
				j++
			}
			if j < len(lines) {
				next := strings.TrimSpace(lines[j])
				if next == "end" || next == "else" {
					emptyAlt = true
				}
			}
		}
		if strings.TrimSpace(ln) == "end" && alt > 0 {
			alt--
		}
	}
	if emptyAlt {
		t.Errorf("不得生成空 alt:\n%s", m)
	}
	if alt != 0 {
		t.Errorf("alt/end 不配对:\n%s", m)
	}
	// 文本消费者同一赋值切片
	text := seqText(root.Nodes, 0)
	for _, want := range []string{`status := "not_started"`, `status = "rejected"`} {
		if !strings.Contains(text, want) {
			t.Errorf("文本应含 %q:\n%s", want, text)
		}
	}
	// 赋值不得画成远程调用（没有 "->>" 行包含赋值文本）
	for _, ln := range lines {
		if strings.Contains(ln, "->>") && strings.Contains(ln, "status :=") {
			t.Errorf("赋值不应画成调用线: %s", ln)
		}
	}
}

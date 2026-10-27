package action

// todo22 P1 测试：场景值沿**实参/结构体字段**跨函数传播——`Run` 里构造/
// 赋值的 `req.Source` 经 `Submit(req)` → `helper(req)` → `check(req)` 传递到
// 条件分支，按证据裁剪；未知字段值/取址改写后不得回退 --scene 键。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

const sceneFieldSrc = `package m

type Req struct{ Source int }

const (
	SourceA = 7
	SourceB = 19
)

func A() {}

func B() {}

func pick() int { return SourceA }

func check(req *Req) {
	if req.Source == SourceB {
		B()
	} else {
		A()
	}
}

func helper(req *Req) {
	check(req)
}

func Submit(req *Req) {
	helper(req)
}

func RunLit() {
	r := &Req{Source: SourceB}
	Submit(r)
}

func RunSet() {
	r := &Req{}
	r.Source = SourceB
	Submit(r)
}

func RunDirect() {
	Submit(&Req{Source: SourceB})
}

func RunUnknown() {
	r := &Req{}
	r.Source = pick()
	Submit(r)
}
`

func seedSceneFieldRepo(t *testing.T) (*Actions, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/f\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(sceneFieldSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	lineOf := func(sub string) int {
		i := strings.Index(sceneFieldSrc, sub)
		if i < 0 {
			t.Fatalf("source 缺少 %q", sub)
		}
		return strings.Count(sceneFieldSrc[:i], "\n") + 1
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	mk := func(name, decl string) *domain.CodeEntity {
		return &domain.CodeEntity{ID: domain.CanonicalID("symbol:go:example.com/f:" + name),
			Kind: domain.KindFunction, Name: name, FilePath: "main.go", LineStart: lineOf(decl)}
	}
	nodes := []*domain.CodeEntity{
		mk("A", "func A()"), mk("B", "func B()"), mk("pick", "func pick()"),
		mk("check", "func check("), mk("helper", "func helper("), mk("Submit", "func Submit("),
		mk("RunLit", "func RunLit("), mk("RunSet", "func RunSet("),
		mk("RunDirect", "func RunDirect("), mk("RunUnknown", "func RunUnknown("),
	}
	fact := func(src, dst string) *domain.Fact {
		return &domain.Fact{SourceID: domain.CanonicalID("symbol:go:example.com/f:" + src),
			TargetID: domain.CanonicalID("symbol:go:example.com/f:" + dst),
			Kind:     domain.FactCalls, ToolSource: domain.ToolCodeGraph, Confidence: 0.8}
	}
	facts := []*domain.Fact{
		fact("Submit", "helper"), fact("helper", "check"),
		fact("check", "B"), fact("check", "A"),
		fact("RunLit", "Submit"), fact("RunSet", "Submit"),
		fact("RunDirect", "Submit"), fact("RunUnknown", "Submit"),
		fact("RunUnknown", "pick"),
	}
	if _, err := r.SaveBatchStats(nodes, facts, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(&domain.BuildMeta{BuildID: "scenef1", ToolName: "fixture", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	return New(r), dir
}

func TestSceneFieldPropagationAcrossCalls(t *testing.T) {
	acts, dir := seedSceneFieldRepo(t)
	cases := []struct {
		name   string
		entry  string
		scene  []string
		status string
	}{
		{"复合字面量字段", "RunLit", nil, SceneTaken},
		{"字段赋值（常量）", "RunSet", nil, SceneTaken},
		{"直接传复合字面量", "RunDirect", nil, SceneTaken},
		{"实参值优先于场景键", "RunLit", []string{"Source=7"}, SceneTaken},
		{"未知字段值（无场景）", "RunUnknown", nil, SceneUnknown},
		{"未知字段值不回退场景键", "RunUnknown", []string{"Source=19"}, SceneUnknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := CodeSequenceRequest{Target: "symbol:go:example.com/f:" + c.entry,
				RepoAbs: dir, Depth: 6}
			if len(c.scene) > 0 {
				req.Scene = NewScene(c.scene)
			}
			root, err := acts.CodeSequence(req)
			if err != nil {
				t.Fatalf("CodeSequence: %v", err)
			}
			fits := sceneFitsOf(t, root)
			if len(fits) != 1 {
				t.Fatalf("期望 1 个分支（check 内），got %d: %+v", len(fits), fits)
			}
			if fits[0].Status != c.status {
				t.Errorf("分支 → %s，期望 %s（reason=%s source=%s literal=%q expected=%q）",
					fits[0].Status, c.status, fits[0].Reason, fits[0].Source, fits[0].Literal, fits[0].Expected)
			}
		})
	}
}

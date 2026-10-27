package action

// todo22 P1 测试：AOP bypass 的运行时事实（`ctx.Value("bypass") != nil`）——
// 只有 --scene 显式给出该 context key 时才裁剪；未给出/动态 key 保持 unknown。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

func TestSceneContextValueCompare(t *testing.T) {
	consts := sceneConstsMap{"BypassKey": "bypass"}
	cases := []struct {
		cond   string
		scene  []string
		status string
	}{
		{`ctx.Value("bypass") != nil`, []string{"bypass=true"}, SceneTaken},
		{`ctx.Value("bypass") != nil`, []string{"bypass=false"}, SceneExcluded},
		{`ctx.Value("bypass") == nil`, []string{"bypass=false"}, SceneTaken},
		{`ctx.Value("bypass") == nil`, []string{"bypass=true"}, SceneExcluded},
		{`nil != ctx.Value("bypass")`, []string{"bypass=1"}, SceneTaken},
		{`ctx.Value(BypassKey) != nil`, []string{"bypass=on"}, SceneTaken},
		// 未给出的键：unknown（不猜）
		{`ctx.Value("bypass") != nil`, nil, SceneUnknown},
		{`ctx.Value("bypass") != nil`, []string{"region=CN"}, SceneUnknown},
		// 动态 key：unknown
		{`ctx.Value(k) != nil`, []string{"bypass=true"}, SceneUnknown},
		// 非 nil 比较不归本规则（交给普通比较逻辑 → unknown）
		{`ctx.Value("bypass") != "x"`, []string{"bypass=true"}, SceneUnknown},
	}
	for _, c := range cases {
		sc := NewScene(c.scene)
		fit := sc.matchCondSrc(sceneCond(t, c.cond), nil, nil, consts)
		if fit.Status != c.status {
			t.Errorf("%s @ %v → %s（reason=%s），期望 %s", c.cond, c.scene, fit.Status, fit.Reason, c.status)
		}
	}
}

const sceneCtxSrc = `package m

import "context"

type Req struct{}

const BypassKey = "bypass"

func Direct(ctx context.Context, req *Req) {}

func Wrapper(ctx context.Context, req *Req) {}

func Bypassed(ctx context.Context, req *Req) {
	if ctx.Value("bypass") != nil {
		Direct(ctx, req)
	} else {
		Wrapper(ctx, req)
	}
}

func BypassedConst(ctx context.Context, req *Req) {
	if ctx.Value(BypassKey) != nil {
		Direct(ctx, req)
	} else {
		Wrapper(ctx, req)
	}
}

func Dynamic(ctx context.Context, req *Req, key string) {
	if ctx.Value(key) != nil {
		Direct(ctx, req)
	} else {
		Wrapper(ctx, req)
	}
}
`

func seedSceneCtxRepo(t *testing.T) (*Actions, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/c\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(sceneCtxSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	lineOf := func(sub string) int {
		i := strings.Index(sceneCtxSrc, sub)
		if i < 0 {
			t.Fatalf("source 缺少 %q", sub)
		}
		return strings.Count(sceneCtxSrc[:i], "\n") + 1
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	mk := func(name, decl string) *domain.CodeEntity {
		return &domain.CodeEntity{ID: domain.CanonicalID("symbol:go:example.com/c:" + name),
			Kind: domain.KindFunction, Name: name, FilePath: "main.go", LineStart: lineOf(decl)}
	}
	nodes := []*domain.CodeEntity{
		mk("Direct", "func Direct("), mk("Wrapper", "func Wrapper("),
		mk("Bypassed", "func Bypassed("), mk("BypassedConst", "func BypassedConst("),
		mk("Dynamic", "func Dynamic("),
	}
	fact := func(src, dst string) *domain.Fact {
		return &domain.Fact{SourceID: domain.CanonicalID("symbol:go:example.com/c:" + src),
			TargetID: domain.CanonicalID("symbol:go:example.com/c:" + dst),
			Kind:     domain.FactCalls, ToolSource: domain.ToolCodeGraph, Confidence: 0.8}
	}
	facts := []*domain.Fact{
		fact("Bypassed", "Direct"), fact("Bypassed", "Wrapper"),
		fact("BypassedConst", "Direct"), fact("BypassedConst", "Wrapper"),
		fact("Dynamic", "Direct"), fact("Dynamic", "Wrapper"),
	}
	if _, err := r.SaveBatchStats(nodes, facts, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(&domain.BuildMeta{BuildID: "scenectx1", ToolName: "fixture", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	return New(r), dir
}

func callLabels(root *CodeSeqNode) map[string]bool {
	out := map[string]bool{}
	var walk func(nodes []*CodeSeqNode)
	walk = func(nodes []*CodeSeqNode) {
		for _, n := range nodes {
			if n.Kind == "call" {
				out[n.Label] = true
			}
			walk(n.Nodes)
			walk(n.Else)
		}
	}
	walk(root.Nodes)
	return out
}

func TestSceneContextValueSequence(t *testing.T) {
	acts, dir := seedSceneCtxRepo(t)
	cases := []struct {
		name    string
		entry   string
		scene   []string
		want    []string
		notWant []string
	}{
		{"bypass=true 走直连", "Bypassed", []string{"bypass=true"}, []string{"Direct"}, []string{"Wrapper"}},
		{"bypass=false 走包装", "Bypassed", []string{"bypass=false"}, []string{"Wrapper"}, []string{"Direct"}},
		{"无场景两分支保留", "Bypassed", nil, []string{"Direct", "Wrapper"}, nil},
		{"常量 key 同样生效", "BypassedConst", []string{"bypass=true"}, []string{"Direct"}, []string{"Wrapper"}},
		{"动态 key 不猜", "Dynamic", []string{"bypass=true"}, []string{"Direct", "Wrapper"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := CodeSequenceRequest{Target: "symbol:go:example.com/c:" + c.entry, RepoAbs: dir, Depth: 3}
			if len(c.scene) > 0 {
				req.Scene = NewScene(c.scene)
			}
			root, err := acts.CodeSequence(req)
			if err != nil {
				t.Fatalf("CodeSequence: %v", err)
			}
			labels := callLabels(root)
			for _, w := range c.want {
				if !labels[w] {
					t.Errorf("应执行 %s；got %v", w, labels)
				}
			}
			for _, w := range c.notWant {
				if labels[w] {
					t.Errorf("不应执行 %s（场景裁剪失败）；got %v", w, labels)
				}
			}
		})
	}
}

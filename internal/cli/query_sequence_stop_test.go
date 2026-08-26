package cli

// R83 测试：时序图停止包配置（seq.stop_packages——命中不深入）+
// 参与者按出现顺序排列（箭头从左到右）。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// withStopPkgs 覆盖 agentConfigPath 指向临时配置（写 stop_packages）。
func withStopPkgs(t *testing.T, stops []string) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	content := "seq:\n  stop_packages: []\n"
	if len(stops) > 0 {
		var b strings.Builder
		b.WriteString("seq:\n  stop_packages:\n")
		for _, s := range stops {
			b.WriteString("    - " + s + "\n")
		}
		content = b.String()
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	old := agentConfigPath
	agentConfigPath = func() string { return p }
	t.Cleanup(func() { agentConfigPath = old })
}

// TestSeqStopPkgHit：包匹配（完整路径/短名/后缀）。
func TestSeqStopPkgHit(t *testing.T) {
	withStopPkgs(t, []string{"example.com/m/repo", "infra"})
	cases := map[string]bool{
		"symbol:go:example.com/m/repo:(R).Get":     true,  // 完整路径
		"symbol:go:example.com/m/repo:helper":      true,  // 完整路径
		"symbol:go:example.com/m/pkg/infra:(X).Y":  true,  // 短名
		"symbol:go:example.com/m/svc:(S).Run":      false, // 未命中
		"symbol:go:example.com/m/order:(O).Pay":    false,
	}
	for id, want := range cases {
		if got := seqStopPkgHit(id); got != want {
			t.Errorf("seqStopPkgHit(%s) = %v; want %v", id, got, want)
		}
	}
}

// TestSeqStopPkgNoConfig：无配置 → 不命中。
func TestSeqStopPkgNoConfig(t *testing.T) {
	withStopPkgs(t, nil)
	if seqStopPkgHit("symbol:go:example.com/m/repo:(R).Get") {
		t.Error("无配置不应命中")
	}
}

// TestSeqStopPkgBlocksExpand：R83——停止包命中 → depth 2 不展开内部。
func TestSeqStopPkgBlocksExpand(t *testing.T) {
	dir := seedRepo(t)
	src := `package m

import "example.com/m/svc"

func Prepare() {
	svc.LoadItems()
}
`
	svcSrc := `package svc

func LoadItems() {
	helper()
}

func helper() {}
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "svc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "svc", "svc.go"), []byte(svcSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:Prepare", Kind: domain.KindFunction, Name: "Prepare", FilePath: "main.go", LineStart: 5},
		{ID: "symbol:go:example.com/m/svc:LoadItems", Kind: domain.KindFunction, Name: "LoadItems", FilePath: "svc/svc.go", LineStart: 4},
		{ID: "symbol:go:example.com/m/svc:helper", Kind: domain.KindFunction, Name: "helper", FilePath: "svc/svc.go", LineStart: 8},
	}, []*domain.Fact{
		{SourceID: "symbol:go:example.com/m:Prepare", TargetID: "symbol:go:example.com/m/svc:LoadItems",
			Kind: domain.FactCalls, Confidence: 0.9, Metadata: map[string]any{"line_num": 6}},
		{SourceID: "symbol:go:example.com/m/svc:LoadItems", TargetID: "symbol:go:example.com/m/svc:helper",
			Kind: domain.FactCalls, Confidence: 0.9, Metadata: map[string]any{"line_num": 5}},
	}, nil); err != nil {
		t.Fatal(err)
	}
	acts, err := newTestActions(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	// 无配置：depth 2 展开 helper
	root := codeSequence(acts, dir, "Prepare", 2)
	if len(root.Nodes) != 1 || len(root.Nodes[0].Nodes) != 1 {
		t.Fatalf("无配置 depth 2 应展开: %+v", root.Nodes)
	}
	// svc 在停止列表：depth 2 不展开（节点保留 Nodes 空）
	withStopPkgs(t, []string{"example.com/m/svc"})
	root2 := codeSequence(acts, dir, "Prepare", 2)
	if len(root2.Nodes) != 1 || len(root2.Nodes[0].Nodes) != 0 {
		t.Fatalf("停止包应不展开内部: %+v", root2.Nodes)
	}
	if root2.Nodes[0].Label != "svc.LoadItems" {
		t.Errorf("节点应保留（不深入）: %+v", root2.Nodes[0])
	}
}

// TestRenderCodeSeqOrder：R83——参与者按出现顺序（调用方先声明靠左，
// 箭头从左到右；不再字母排序）。
func TestRenderCodeSeqOrder(t *testing.T) {
	root := &codeSeqNode{Kind: "call", Label: "Prepare", Nodes: []*codeSeqNode{
		{Kind: "call", Label: "svc.Validate", Line: 1},
		{Kind: "call", Label: "svc.Save", Line: 2},
	}}
	m := renderCodeSeqMermaid(root)
	// 出现顺序：Prepare → svc.Validate → svc.Save（不是字母序 Save 在前）
	idxV := strings.Index(m, "as svc.Validate")
	idxS := strings.Index(m, "as svc.Save")
	if idxV < 0 || idxS < 0 || idxV > idxS {
		t.Errorf("参与者应按出现顺序（Validate 在 Save 前）:\n%s", m)
	}
	if !strings.Contains(m, "P0->>P1: svc.Validate") {
		t.Errorf("消息线应 P0->>P1:\n%s", m)
	}
}

package cli

// R83/R95 测试：时序图停止包配置（seq.stop_packages——命中不深入）
// 的 cli 端到端（配置读取 → CodeSequenceRequest.StopPackages 传递）；
// 参与者按出现顺序排列（mermaid 渲染留 cli）。停止包判定/签名解析
// 等纯逻辑断言在 action 包。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
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

// TestCmdSeqStopPkgBlocksExpand：cli 端到端——config.yaml 停止包 →
// --code --depth 2 不展开内部（文本输出无嵌套行）。
func TestCmdSeqStopPkgBlocksExpand(t *testing.T) {
	dir := seedNestedSeqRepo(t)
	// 无配置：depth 2 展开 helper
	out1 := captureStdout(func() {
		if code := cmdQuery([]string{"sequence", "--code", "Prepare", "--depth", "2", "--repo", dir}); code != 0 {
			t.Fatalf("sequence --code exit = %d", code)
		}
	})
	if !strings.Contains(out1, "helper") {
		t.Errorf("无配置应展开嵌套（helper 缺失）:\n%s", out1)
	}
	// svc 在停止列表：depth 2 不展开
	withStopPkgs(t, []string{"example.com/m/svc"})
	out2 := captureStdout(func() {
		if code := cmdQuery([]string{"sequence", "--code", "Prepare", "--depth", "2", "--repo", dir}); code != 0 {
			t.Fatalf("sequence --code exit = %d", code)
		}
	})
	if !strings.Contains(out2, "svc.LoadItems") {
		t.Errorf("节点应保留（svc.LoadItems 缺失）:\n%s", out2)
	}
	if strings.Contains(out2, "helper") {
		t.Errorf("停止包应不展开内部（helper 不应出现）:\n%s", out2)
	}
}

// TestRenderCodeSeqOrder：R83——参与者按出现顺序（调用方先声明靠左，
// 箭头从左到右；不再字母排序）；参与者 = 调用对象（Actor）。
func TestRenderCodeSeqOrder(t *testing.T) {
	root := &action.CodeSeqNode{Kind: "call", Label: "Prepare", Actor: "Prepare", Nodes: []*action.CodeSeqNode{
		{Kind: "call", Label: "svc.Validate", Actor: "svc", Line: 1, Args: []string{"order.Data"}, Returns: []string{"bool", "error"}},
		{Kind: "call", Label: "svc.Save", Actor: "svc", Line: 2},
		{Kind: "call", Label: "repo.CreateOrder", Actor: "repo", Line: 3},
	}}
	m := renderCodeSeqMermaid(root)
	// 出现顺序：Prepare → svc → repo（不是字母序）
	idxV := strings.Index(m, "as svc")
	idxS := strings.Index(m, "as repo")
	if idxV < 0 || idxS < 0 || idxV > idxS {
		t.Errorf("参与者应按出现顺序（svc 在 repo 前）:\n%s", m)
	}
	// 参与者是对象（svc/repo），消息线保留完整调用名 + 参数第二行
	if !strings.Contains(m, "P0->>P1: svc.Validate<br/>(order.Data)") {
		t.Errorf("消息线应带参数类型（第二行）:\n%s", m)
	}
	if !strings.Contains(m, "P1-->>P0: return bool, error") {
		t.Errorf("应含 return 线（返回值类型）:\n%s", m)
	}
	if !strings.Contains(m, "->>P2: repo.CreateOrder") {
		t.Errorf("参与者应为对象 repo:\n%s", m)
	}
}

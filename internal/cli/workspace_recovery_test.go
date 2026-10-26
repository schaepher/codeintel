package cli

// todo13-15：workspace 范围与既有 worktree 归属恢复。
//   - 未登记主仓库也能认领 ws 下 git 元数据证明的既有 worktree（不索引主仓）
//   - list --workspace 精确列出三条
//   - 无法证明归属 → 明确失败，不错误认领
//   - ext-chain 候选默认按同一 workspace 限定；--all-repos 显式扩展

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

func seedBareMain(t *testing.T, name string) string {
	t.Helper()
	main := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, main, "init", "-b", "main", ".")
	runGit(t, main, "config", "user.email", "t@t")
	runGit(t, main, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(main, "go.mod"),
		[]byte("module example.com/"+name+"\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(main, "main.go"), "package main\n\nfunc main() {}\n")
	runGit(t, main, "add", ".")
	runGit(t, main, "commit", "-m", "init")
	return main
}

// 三个既有 worktree 归属丢失：官方命令认领，不要求先 init 主仓库。
func TestWorkspaceRecoveryUnregisteredMains(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git 不可用")
	}
	isolateRegistryDir(t)
	ws := t.TempDir()
	var mains []string
	for _, name := range []string{"main-a", "main-b", "main-c"} {
		main := seedBareMain(t, name)
		mains = append(mains, main)
		runGit(t, main, "worktree", "add", "--detach", filepath.Join(ws, name))
	}
	// 注册表里没有主仓库条目（归属丢失）
	args := []string{"init", "--dir", ws}
	for _, m := range mains {
		args = append(args, "--repo", m)
	}
	var code int
	out := captureStdout(func() {
		code = cmdWorkspace(args)
	})
	if code != 0 {
		t.Fatalf("workspace init exit = %d\n%s", code, out)
	}
	if !strings.Contains(out, "已认领") {
		t.Errorf("应显示认领结果:\n%s", out)
	}
	r, err := sqlite.OpenRegistry(registryDirFn())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, m := range mains {
		wt := filepath.Join(ws, filepath.Base(m))
		rp, ok, err := r.FindRepo(wt)
		if err != nil || !ok {
			t.Fatalf("%s 未认领: ok=%v err=%v", wt, ok, err)
		}
		if !rp.IsWorktree || filepath.Clean(rp.WorktreeOf) != filepath.Clean(m) || rp.Workspace != ws {
			t.Errorf("归属错误: %+v", rp)
		}
		if rp.BuildID != "" {
			t.Errorf("认领不应构建主仓库/worktree: %+v", rp)
		}
	}
	// list --workspace 精确列出 3 条
	jsonOut := captureStdout(func() {
		if code := cmdList([]string{"--workspace", ws, "--json"}); code != 0 {
			t.Errorf("list exit = %d", code)
		}
	})
	var entries []map[string]any
	if err := json.Unmarshal([]byte(jsonOut), &entries); err != nil {
		t.Fatalf("list JSON: %v\n%s", err, jsonOut)
	}
	if len(entries) != 3 {
		t.Errorf("list --workspace 应精确列出 3 条，实为 %d: %s", len(entries), jsonOut)
	}
	for _, m := range mains {
		if !strings.Contains(jsonOut, filepath.Join(ws, filepath.Base(m))) {
			t.Errorf("list 应含 %s: %s", m, jsonOut)
		}
	}
}

// 无法证明 Git 归属：明确失败，不错误认领。
func TestWorkspaceRecoveryRejectsUnprovable(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git 不可用")
	}
	isolateRegistryDir(t)
	ws := t.TempDir()
	main := seedBareMain(t, "main-x")
	// ws 下的目录不是 worktree
	if err := os.MkdirAll(filepath.Join(ws, "junk"), 0o755); err != nil {
		t.Fatal(err)
	}
	var code int
	out := captureStdout(func() {
		code = cmdWorkspace([]string{"init", "--dir", ws, "--repo", main})
	})
	if code == 0 {
		t.Errorf("无可证明 worktree 时应失败: exit=%d\n%s", code, out)
	}
	r, _ := sqlite.OpenRegistry(registryDirFn())
	defer r.Close()
	if repos, _ := r.ListRepos(); len(repos) != 0 {
		t.Errorf("不得错误认领: %+v", repos)
	}
	// 非 git 路径
	if err := os.MkdirAll(filepath.Join(ws, "not-git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if code := cmdWorkspace([]string{"init", "--dir", ws, "--repo", filepath.Join(ws, "not-git")}); code == 0 {
		t.Errorf("非 git 主仓库路径应失败")
	}
}

// ext-chain 候选默认按 workspace 限定；--all-repos 显式扩展。
func TestExtChainCandidateWorkspaceScope(t *testing.T) {
	isolateRegistryDir(t)
	r, err := sqlite.OpenRegistry(registryDirFn())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	wsA := filepath.Join(t.TempDir(), "workspace-a")
	wsB := filepath.Join(t.TempDir(), "workspace-b")
	main := filepath.Join(t.TempDir(), "main")
	entries := []sqlite.RegistryRepo{
		{Path: filepath.Join(wsA, "client"), Workspace: wsA, BuildID: "b", RegisteredAt: "t"},
		{Path: filepath.Join(wsA, "server"), Workspace: wsA, BuildID: "b", RegisteredAt: "t"},
		{Path: filepath.Join(wsB, "server"), Workspace: wsB, BuildID: "b", RegisteredAt: "t"},
		{Path: filepath.Join(main, "server"), BuildID: "b", RegisteredAt: "t"},
	}
	for _, e := range entries {
		if err := r.RegisterRepo(e); err != nil {
			t.Fatal(err)
		}
	}
	self := filepath.Join(wsA, "client")
	cands, scope, reason := extChainCandidateRepos(self, false)
	if scope != "workspace:"+wsA || !strings.Contains(reason, "workspace") {
		t.Errorf("默认应为 workspace 范围: scope=%s reason=%s", scope, reason)
	}
	if len(cands) != 1 || cands[0] != filepath.Join(wsA, "server") {
		t.Errorf("默认候选只应有同 workspace server: %+v", cands)
	}
	all, scope, reason := extChainCandidateRepos(self, true)
	if scope != "all-registered" || !strings.Contains(reason, "--all-repos") {
		t.Errorf("显式扩展应可区分: scope=%s reason=%s", scope, reason)
	}
	if len(all) != 3 {
		t.Errorf("--all-repos 应含 3 个候选: %+v", all)
	}
}

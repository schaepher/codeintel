package cli

// todo19-7：workspace 认领既有 worktree 时保留实际构建元数据。

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

func markWorktreeBuilt(t *testing.T, wtDir string) sqlite.RegistryRepo {
	t.Helper()
	r, err := sqlite.OpenRegistry(registryDirFn())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	prev, ok, err := r.FindRepo(wtDir)
	if err != nil || !ok {
		t.Fatalf("worktree 未注册: ok=%v err=%v", ok, err)
	}
	prev.BuildID = "built-1"
	prev.LastBuiltAt = "2026-01-01T00:00:00Z"
	prev.HeadCommit = gitHead(wtDir)
	if err := r.RegisterRepo(prev); err != nil {
		t.Fatal(err)
	}
	return prev
}

func registryFind(t *testing.T, path string) sqlite.RegistryRepo {
	t.Helper()
	r, err := sqlite.OpenRegistry(registryDirFn())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	rp, ok, err := r.FindRepo(path)
	if err != nil || !ok {
		t.Fatalf("条目缺失: %s ok=%v err=%v", path, ok, err)
	}
	return rp
}

func TestWorkspaceClaimKeepsBuildMetadata(t *testing.T) {
	main := seedWorkspaceMain(t)
	ws := t.TempDir()
	if code := cmdWorkspace([]string{"init", "--dir", ws}); code != 0 {
		t.Fatalf("workspace init exit=%d", code)
	}
	wt := filepath.Join(ws, filepath.Base(main))
	prev := markWorktreeBuilt(t, wt)

	out := captureStdout(func() {
		if code := cmdWorkspace([]string{"init", "--dir", ws, "--repo", main}); code != 0 {
			t.Errorf("claim exit=%d", code)
		}
	})
	rp := registryFind(t, wt)
	if rp.BuildID != prev.BuildID || rp.LastBuiltAt != prev.LastBuiltAt ||
		rp.HeadCommit != prev.HeadCommit || rp.Workspace != ws {
		t.Errorf("认领后构建元数据/归属错误:\nprev=%+v\nnow=%+v\n%s", prev, rp, out)
	}
	if statusOf(rp) != statusBuilt {
		t.Errorf("HEAD 未变应仍为已构建: %+v", rp)
	}

	// HEAD 已变：保留构建对应 HeadCommit → 过期（不能伪造新 HEAD 成功）
	writeTestFile(t, filepath.Join(wt, "extra.go"), "package main\n")
	runGit(t, wt, "add", ".")
	runGit(t, wt, "commit", "-m", "change")
	captureStdout(func() {
		if code := cmdWorkspace([]string{"init", "--dir", ws, "--repo", main}); code != 0 {
			t.Errorf("re-claim exit=%d", code)
		}
	})
	rp = registryFind(t, wt)
	if rp.HeadCommit != prev.HeadCommit || rp.BuildID != prev.BuildID {
		t.Errorf("HEAD 变化后应保留实际构建 HEAD/BuildID: %+v", rp)
	}
	if statusOf(rp) != statusStale {
		t.Errorf("HEAD 已变应判过期: %+v", rp)
	}
}

func TestWorkspaceClaimKeepsUnbuilt(t *testing.T) {
	main := seedWorkspaceMain(t)
	ws := t.TempDir()
	if code := cmdWorkspace([]string{"init", "--dir", ws}); code != 0 {
		t.Fatalf("init exit=%d", code)
	}
	wt := filepath.Join(ws, filepath.Base(main))
	out := captureStdout(func() {
		if code := cmdWorkspace([]string{"init", "--dir", ws, "--repo", main}); code != 0 {
			t.Errorf("claim exit=%d", code)
		}
	})
	rp := registryFind(t, wt)
	if rp.BuildID != "" || rp.LastBuiltAt != "" || statusOf(rp) != statusUnbuilt {
		t.Errorf("未构建条目认领后仍应未构建: %+v\n%s", rp, out)
	}
}

// 未登记主仓库的恢复分支同样保留构建元数据。
func TestWorkspaceRecoveryClaimKeepsBuildMetadata(t *testing.T) {
	main := seedBareMain(t, "main-meta")
	isolateRegistryDir(t)
	ws := t.TempDir()
	runGit(t, main, "worktree", "add", "--detach", filepath.Join(ws, "main-meta"))
	wt := filepath.Join(ws, "main-meta")
	// 先模拟已构建登记（init 语义），但不登记主仓库
	r, err := sqlite.OpenRegistry(registryDirFn())
	if err != nil {
		t.Fatal(err)
	}
	prev := sqlite.RegistryRepo{Path: wt, Module: "example.com/main-meta", GoModCount: 1,
		HeadCommit: gitHead(wt), BuildID: "built-meta", LastBuiltAt: "2026-01-01T00:00:00Z",
		IsWorktree: true, WorktreeOf: main, RegisteredAt: "2026-01-01T00:00:00Z"}
	if err := r.RegisterRepo(prev); err != nil {
		t.Fatal(err)
	}
	r.Close()
	captureStdout(func() {
		if code := cmdWorkspace([]string{"init", "--dir", ws, "--repo", main}); code != 0 {
			t.Errorf("recovery claim exit=%d", code)
		}
	})
	rp := registryFind(t, wt)
	if rp.BuildID != "built-meta" || rp.LastBuiltAt != prev.LastBuiltAt ||
		rp.HeadCommit != prev.HeadCommit || rp.Workspace != ws || statusOf(rp) != statusBuilt {
		t.Errorf("恢复分支应保留构建元数据: %+v", rp)
	}
	_ = os.Remove
}

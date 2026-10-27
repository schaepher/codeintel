package action

// P1-03（构建流程收口）：action.Build 是 CLI init/update/reindex 与 MCP
// init/update 的唯一构建流程——本文件锁住它的分叉点（全量/增量/无变更/
// go.mod 变更/非 git/非法超时）与构建元数据（工作区指纹）。

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// scipGoAvailable 构建流程依赖 scip-go（与 orchestrator 端到端测试同口径）。
func scipGoAvailable() bool {
	_, err := exec.LookPath("scip-go")
	return err == nil
}

// buildFixture 建一个最小 Go module + git 仓库（一次提交），返回目录。
func buildFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/buildfix\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "main.go"), "package main\n\nfunc main() {\n\thelper()\n}\n\nfunc helper() {}\n")
	gitRun(t, dir, "init", "-q")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "init")
	return dir
}

// TestBuildRequiresGoModule 非 module 目录拒绝（原 cli buildRepo 语义）。
func TestBuildRequiresGoModule(t *testing.T) {
	_, err := Build(context.Background(), BuildRequest{RepoPath: t.TempDir(), Mode: BuildFull})
	if err == nil {
		t.Fatal("非 go module 应报错")
	}
}

// TestBuildRejectsGoWorkRoot workspace 根（有 go.work 无 go.mod）拒绝并
// 提示进入模块目录——避免把整个 workspace 当成一个 module。
func TestBuildRejectsGoWorkRoot(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.work"), "go 1.21\n")
	_, err := Build(context.Background(), BuildRequest{RepoPath: dir, Mode: BuildFull})
	if err == nil || !strings.Contains(err.Error(), "go.work") {
		t.Fatalf("go.work 根 err = %v", err)
	}
}

// TestBuildInvalidAdapterTimeout 超时必须为正时长。
func TestBuildInvalidAdapterTimeout(t *testing.T) {
	dir := buildFixture(t)
	_, err := Build(context.Background(), BuildRequest{RepoPath: dir, Mode: BuildFull,
		AdapterTimeout: -time.Second})
	if err == nil || !strings.Contains(err.Error(), "adapter-timeout") {
		t.Fatalf("超时校验 err = %v", err)
	}
}

// TestBuildIncrementalRequiresGit 增量需要 git（首次构建用 init）。
func TestBuildIncrementalRequiresGit(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/nogit\n\ngo 1.21\n")
	_, err := Build(context.Background(), BuildRequest{RepoPath: dir, Mode: BuildIncremental})
	if err == nil || !strings.Contains(err.Error(), "git") {
		t.Fatalf("非 git 增量 err = %v", err)
	}
}

// TestBuildFullThenIncremental 全量 → 增量 → 无变更 三段闭环：
// 指纹入库、变更文件列表、NoChange 判定。
func TestBuildFullThenIncremental(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go 不在 PATH（构建流程集成用例跳过）")
	}
	dir := buildFixture(t)
	// 构建前工作区就有未跟踪 Go 文件 → 指纹必须记录它（否则重索引后
	// 仍会误报过期）
	writeFile(t, filepath.Join(dir, "extra.go"), "package main\n\nfunc extra() {}\n")

	full, err := Build(context.Background(), BuildRequest{RepoPath: dir, Mode: BuildFull})
	if err != nil {
		t.Fatalf("FullBuild: %v", err)
	}
	if full.Result == nil || full.Result.Status != domain.BuildSuccess || full.Result.Nodes == 0 {
		t.Fatalf("full outcome = %+v", full.Result)
	}
	if full.Repo == nil || full.Repo.Module != "example.com/buildfix" {
		t.Fatalf("repo = %+v", full.Repo)
	}

	// 构建时的脏文件 → 指纹已写进 build_metadata（stale 判定的另一半）
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_, _, fp, err := sqlite.NewRepo(db).LatestBuildFreshness()
	db.Close()
	if err != nil || fp == "" {
		t.Fatalf("首次构建指纹 = %q, %v（脏文件应入指纹）", fp, err)
	}

	inc, err := Build(context.Background(), BuildRequest{RepoPath: dir, Mode: BuildIncremental})
	if err != nil {
		t.Fatalf("IncrementalBuild: %v", err)
	}
	if inc.NoChange || len(inc.Changed) != 1 || inc.Changed[0] != "extra.go" {
		t.Fatalf("incremental outcome = nochange=%v changed=%v", inc.NoChange, inc.Changed)
	}
	if inc.Result == nil || inc.Result.Nodes == 0 {
		t.Fatalf("增量结果为空: %+v", inc.Result)
	}

	// 提交该文件 → 索引 commit 落后（提交差异），再增量一次让索引追上 HEAD
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "add extra")
	if _, err := Build(context.Background(), BuildRequest{RepoPath: dir, Mode: BuildIncremental}); err != nil {
		t.Fatalf("catch-up IncrementalBuild: %v", err)
	}

	// 无变更：不再构建（NoChange，Result 为 nil）
	none, err := Build(context.Background(), BuildRequest{RepoPath: dir, Mode: BuildIncremental})
	if err != nil {
		t.Fatalf("no-change Build: %v", err)
	}
	if !none.NoChange || none.Result != nil {
		t.Fatalf("no-change outcome = %+v", none)
	}
}

// TestBuildGoModChangedNeedsFull go.mod 变更影响 module 范围：增量拒绝并
// 要求 init（NeedsFull + 错误信息里给出可执行命令）。
func TestBuildGoModChangedNeedsFull(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go 不在 PATH")
	}
	dir := buildFixture(t)
	if _, err := Build(context.Background(), BuildRequest{RepoPath: dir, Mode: BuildFull}); err != nil {
		t.Fatalf("FullBuild: %v", err)
	}
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/buildfix\n\ngo 1.21\n\n// bump\n")
	out, err := Build(context.Background(), BuildRequest{RepoPath: dir, Mode: BuildIncremental})
	if err == nil || !strings.Contains(err.Error(), "codeintel init") {
		t.Fatalf("go.mod 变更 err = %v", err)
	}
	if !out.NeedsFull {
		t.Errorf("NeedsFull 未置位: %+v", out)
	}
}

// TestBuildIgnoresIgnoredDirs 忽略目录内的变更不进增量（仓库级
// codeintel.yaml）——指纹同口径过滤。
func TestBuildIgnoresIgnoredDirs(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go 不在 PATH")
	}
	dir := buildFixture(t)
	if err := SaveIgnoreDirs(dir, []string{"gen"}); err != nil {
		t.Fatalf("SaveIgnoreDirs: %v", err)
	}
	if _, err := Build(context.Background(), BuildRequest{RepoPath: dir, Mode: BuildFull}); err != nil {
		t.Fatalf("FullBuild: %v", err)
	}
	writeFile(t, filepath.Join(dir, "gen", "generated.go"), "package gen\n")
	out, err := Build(context.Background(), BuildRequest{RepoPath: dir, Mode: BuildIncremental})
	if err != nil {
		t.Fatalf("IncrementalBuild: %v", err)
	}
	if !out.NoChange {
		t.Fatalf("忽略目录内的变更不应触发增量: %v", out.Changed)
	}
	if _, err := os.Stat(filepath.Join(dir, "codeintel.yaml")); err != nil {
		t.Errorf("codeintel.yaml 应存在: %v", err)
	}
}

// TestBuildResultExposesBuildID：BuildResult 暴露实际写入 build_metadata 的
// BuildID（todo23 §10.1），CommitSHA 单独保存、不被当 BuildID。
func TestBuildResultExposesBuildID(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go 不在 PATH")
	}
	dir := buildFixture(t)
	out, err := Build(context.Background(), BuildRequest{RepoPath: dir, Mode: BuildFull})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if out.Result == nil || out.Result.BuildID == "" {
		t.Fatalf("BuildResult.BuildID 应非空: %+v", out.Result)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	meta, err := sqlite.NewRepo(db).GetLatest()
	if err != nil || meta == nil {
		t.Fatalf("GetLatest: %v", err)
	}
	if out.Result.BuildID != meta.BuildID {
		t.Errorf("BuildResult.BuildID=%q 与 build_metadata=%q 不一致", out.Result.BuildID, meta.BuildID)
	}
	if out.Result.CommitSHA != "" && out.Result.CommitSHA == out.Result.BuildID {
		t.Errorf("CommitSHA 不得充当 BuildID: %q", out.Result.CommitSHA)
	}
}

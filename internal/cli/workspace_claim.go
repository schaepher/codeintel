package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// isGitMainRepo 路径是否为 git 主仓库（.git 是目录；worktree 的 .git 是文件）。
func isGitMainRepo(path string) bool {
	fi, err := os.Stat(filepath.Join(path, ".git"))
	return err == nil && fi.IsDir()
}

// claimExistingWorktrees 认领 wsDir 下已存在、且 git worktree 元数据
// （.git 文件里的 gitdir → 主仓库）证明属于 main 的 worktree。
//
// todo13-15：恢复丢失归属的既有工作树——官方命令即可完成，不需要先
// init 主仓库、不额外索引未授权主仓库、也不手改注册 SQLite。
func claimExistingWorktrees(r *sqlite.Registry, wsDir, main string) (claimed, failed int, found bool) {
	entries, err := os.ReadDir(wsDir)
	if err != nil {
		return 0, 0, false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		wtDir := filepath.Join(wsDir, e.Name())
		isWT, wtOf := detectWorktree(wtDir)
		if !isWT || filepath.Clean(wtOf) != filepath.Clean(main) {
			continue
		}
		found = true
		module, _ := readGoModule(wtDir)
		gmc := 1
		if repo, err := buildRepo(wtDir); err == nil && repo != nil {
			gmc = len(repo.Modules)
		}
		stamp := time.Now().UTC().Format(time.RFC3339)
		if prev, ok, perr := r.FindRepo(wtDir); perr == nil && ok && prev.RegisteredAt != "" {
			stamp = prev.RegisteredAt
		}
		if err := r.RegisterRepo(sqlite.RegistryRepo{
			Path: wtDir, Module: module, GoModCount: gmc,
			IsWorktree: true, WorktreeOf: main, Workspace: wsDir,
			HeadCommit: gitHead(wtDir), RegisteredAt: stamp,
		}); err != nil {
			fmt.Printf("  失败 %s: 认领 worktree: %v\n", e.Name(), err)
			failed++
			continue
		}
		fmt.Printf("  已认领 %s → %s（workspace 归属已恢复，主仓库未索引）\n", e.Name(), wtDir)
		claimed++
	}
	return claimed, failed, found
}

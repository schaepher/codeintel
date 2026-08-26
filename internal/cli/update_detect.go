package cli

import (
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"go.uber.org/zap"

	_ "modernc.org/sqlite"
)

// detectChangedGoFiles 检测仓库中变更的 Go 源文件（相对路径）：
//   - 索引 commit 落后于 HEAD（build_metadata 最新 commit_sha ≠ HEAD）：
//     git diff --name-only <buildSHA> HEAD——提交内变更（工作区干净时
//     git diff HEAD 检测不到，索引基于旧 commit 的场景）
//   - git diff --name-only HEAD：已跟踪文件的修改/删除/新增
//   - git ls-files --others --exclude-standard：未跟踪文件（含新文件）
//
// 返回 .go 文件与 go.mod/go.work（module 级变更由调用方处理）。
func detectChangedGoFiles(repoPath string) ([]string, error) {
	logger := zap.L()
	logger.Debug("enter detectChangedGoFiles")
	defer logger.Debug("exit detectChangedGoFiles")

	if _, err := os.Stat(filepath.Join(repoPath, ".git")); err != nil {
		return nil, fmt.Errorf("%s 不是 git 仓库（增量更新需要 git；首次构建请用 init）", repoPath)
	}
	seen := map[string]bool{}
	var out []string
	add := func(list string) {
		for _, f := range strings.Split(strings.TrimSpace(list), "\n") {
			f = strings.TrimSpace(f)
			if f == "" || seen[f] {
				continue
			}
			if strings.HasSuffix(f, ".go") || f == "go.mod" || f == "go.work" {
				seen[f] = true
				out = append(out, f)
			}
		}
	}
	if b, err := exec.Command("git", "-C", repoPath, "rev-parse", "HEAD").Output(); err == nil {
		if head := strings.TrimSpace(string(b)); head != "" {
			if sha := indexCommitSHA(repoPath); sha != "" && sha != head {
				if b, err := exec.Command("git", "-C", repoPath, "diff", "--name-only", sha, head).Output(); err == nil {
					add(string(b))
				}
			}
		}
	}
	if b, err := exec.Command("git", "-C", repoPath, "diff", "--name-only", "HEAD").Output(); err == nil {
		add(string(b))
	}
	if b, err := exec.Command("git", "-C", repoPath, "ls-files", "--others", "--exclude-standard").Output(); err == nil {
		add(string(b))
	}
	sort.Strings(out)
	return out, nil
}

// indexCommitSHA 返回索引最新构建的 commit_sha（build_metadata 最新记录）。
// 索引不存在 / 无构建记录 / 读取失败 → 空串（回退工作区检测）。
func indexCommitSHA(repoPath string) string {
	path := filepath.Join(repoPath, ".codeintel", "codeintel.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return ""
	}
	defer db.Close()
	var sha string
	if err := db.QueryRow(`SELECT COALESCE(commit_sha,'') FROM build_metadata
		ORDER BY timestamp DESC, rowid DESC LIMIT 1`).Scan(&sha); err != nil {
		return ""
	}
	return sha
}

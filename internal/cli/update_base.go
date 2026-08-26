package cli

// R85 --base 分层：变更检测基准 = base HEAD（diff base..当前 + 工作区）。

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// repoCommitSHA 返回目录的 git HEAD commit（--base 变更基准）。
func repoCommitSHA(repoPath string) (string, error) {
	b, err := exec.Command("git", "-C", repoPath, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse: %w", err)
	}
	sha := strings.TrimSpace(string(b))
	if sha == "" {
		return "", fmt.Errorf("git HEAD 为空")
	}
	return sha, nil
}

// detectChangedGoFilesSince 检测相对指定 commit 的变更 Go 源文件
// （R85 --base 场景：diff base..HEAD + 工作区 + 未跟踪；返回 .go 与
// go.mod/go.work，module 级变更由调用方处理）。
func detectChangedGoFilesSince(repoPath, sinceCommit string) ([]string, error) {
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
	if b, err := exec.Command("git", "-C", repoPath, "diff", "--name-only", sinceCommit, "HEAD").Output(); err == nil {
		add(string(b))
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

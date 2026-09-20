package cli

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
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
	dirty, err := dirtyGoFiles(repoPath)
	if err != nil {
		return nil, err
	}
	for _, f := range dirty {
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out, nil
}

// dirtyGoFiles 工作区**相对 HEAD** 的变更 Go 文件（已跟踪改动/删除 + 未跟踪
// 新文件），不含"索引 commit 落后于 HEAD"那一档（Q254b）。
//
// 为什么要单独一个口径：stale 判定的内容指纹必须**在构建前后稳定**。若用
// detectChangedGoFiles（含 sha 差异），构建前索引落后 → 集合里多出"提交差异"
// 文件，构建后索引追上 → 同一函数只返回工作区脏文件 → 集合漂移 → 指纹永不
// 匹配（实测：重索引后仍报"11 个 Go 文件在构建后被改动"）。提交差异由
// staleInfo 的 SHA 比较负责，不需要进指纹。
func dirtyGoFiles(repoPath string) ([]string, error) {
	if _, err := os.Stat(filepath.Join(repoPath, ".git")); err != nil {
		return nil, fmt.Errorf("%s 不是 git 仓库", repoPath)
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
	if b, err := exec.Command("git", "-C", repoPath, "diff", "--name-only", "HEAD").Output(); err == nil {
		add(string(b))
	}
	if b, err := exec.Command("git", "-C", repoPath, "ls-files", "--others", "--exclude-standard").Output(); err == nil {
		add(string(b))
	}
	sort.Strings(out)
	return out, nil
}

// worktreeFingerprint 工作区脏文件的内容指纹（Q254b）。
//
// 存 **path→内容 sha256 的 JSON**（而非聚合哈希）：诊断时能逐文件定位差异，
// 提示也能给出"已改/新增/删除"条数。内容不变则指纹不变（mtime、git status
// 行数都无关）。构建前存进 build_metadata；查询时算当前值比对——一致即
// "这些变更在构建时就已进索引"（"改文件→重索引"是正常闭环）。
func worktreeFingerprint(repoPath string, files []string) string {
	if len(files) == 0 {
		return ""
	}
	b, err := json.Marshal(currentDirtyHashes(repoPath, files))
	if err != nil {
		return ""
	}
	return string(b)
}

// currentDirtyHashes 当前工作区脏文件的 path→内容哈希（不存在记 missing）。
func currentDirtyHashes(repoPath string, files []string) map[string]string {
	out := map[string]string{}
	for _, f := range files {
		out[f] = fileHash(repoPath, f)
	}
	return out
}

// fileHash 文件内容 sha256（读取失败记 "missing"——删除也是变更）。
func fileHash(repoPath, file string) string {
	b, err := os.ReadFile(filepath.Join(repoPath, file))
	if err != nil {
		return "missing"
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// fingerprintDiff 比较构建时与当前指纹，返回（已改、新增、删除）条数；
// stored 非 JSON（老记录/空）→ ok=false，调用方按"无指纹"保守处理。
func fingerprintDiff(stored string, current map[string]string) (changed, added, removed int, ok bool) {
	if stored == "" {
		return 0, 0, 0, false
	}
	var prev map[string]string
	if err := json.Unmarshal([]byte(stored), &prev); err != nil {
		return 0, 0, 0, false
	}
	for path, hash := range prev {
		cur, exists := current[path]
		switch {
		case !exists:
			removed++
		case cur != hash:
			changed++
		}
	}
	for path := range current {
		if _, exists := prev[path]; !exists {
			added++
		}
	}
	return changed, added, removed, true
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

package cli

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// Q254b：索引新鲜度（stale）判定从 update.go 抽到本文件——它是"查询前要不要
// 提示重建"的独立关注点，与 update 命令流程无关（query / mcp / serve 也用）。

// staleInfo 索引过期检测（field_trace.md §20.3 / Q243；Q254b 重写判据）：
//  1. build_metadata 最新 commit_sha ≠ git HEAD → 过期（提示两个 SHA + 变更
//     Go 文件数）
//  2. SHA 一致但工作区有变更 → **比内容指纹**：与构建时记录的
//     worktree_fingerprint 一致则说明这些变更在构建时就已进索引（"改文件→
//     重索引"是正常闭环），不报过期；不一致才报
//  3. commit_sha 为空（历史构建）→ 回退 timestamp 比较
//
// Q254b 修的两处误报：①变更集合只取 detectChangedGoFiles（.go/go.mod/
// go.work）——原实现按 `git status --porcelain` 行数统计，md/py/脚本等无关
// 文件也被算成"未索引"；②不再把"已索引的脏文件"当过期（指纹比对）。
//
// 非 git 仓库 / 无构建记录 / 不过期 → 返回空。
func staleInfo(repoAbs string, r *sqlite.Repo) string {
	changed, err := detectChangedGoFiles(repoAbs) // 含"索引 commit 落后"的提交差异
	if err != nil {
		return "" // 非 git 仓库：无法比较
	}
	dirty, derr := dirtyGoFiles(repoAbs) // 指纹口径：只看工作区相对 HEAD
	if derr != nil {
		return ""
	}
	var buildSHA, buildFP string
	var buildTs int64
	if err := r.QueryRow(`SELECT COALESCE(commit_sha,''), timestamp,
		COALESCE(worktree_fingerprint,'')
		FROM build_metadata ORDER BY timestamp DESC, rowid DESC LIMIT 1`).
		Scan(&buildSHA, &buildTs, &buildFP); err != nil {
		return "" // 无构建记录
	}
	headSHA := strings.TrimSpace(string(execOut(repoAbs, "rev-parse", "HEAD")))
	if buildSHA != "" && headSHA != "" && buildSHA != headSHA {
		return fmt.Sprintf("索引可能过期（基于 commit %s，HEAD 为 %s，%d 个 Go 文件变更）；运行 codeintel update",
			shortSHA(buildSHA), shortSHA(headSHA), len(changed))
	}
	if len(dirty) > 0 {
		cur := currentDirtyHashes(repoAbs, dirty)
		changedN, addedN, removedN, ok := fingerprintDiff(buildFP, cur)
		switch {
		case ok && changedN+addedN+removedN == 0:
			return "" // 构建时已包含这些变更（"改文件→重索引"闭环）
		case !ok:
			return fmt.Sprintf("索引可能过期（工作区 %d 个 Go 文件变更，构建记录无工作区指纹）；运行 codeintel update",
				len(dirty))
		default:
			// 数字分两层：构建后真正改动的（触发原因）+ 工作区未提交总数
			return fmt.Sprintf("索引可能过期（构建后 %d 个 Go 文件改动：%d 已改/%d 新增/%d 删除；工作区共 %d 个未提交变更）；运行 codeintel update",
				changedN+addedN+removedN, changedN, addedN, removedN, len(dirty))
		}
	}
	if buildSHA != "" {
		return "" // SHA 一致且工作区无相关变更——新鲜
	}
	headTs, err := strconv.ParseInt(strings.TrimSpace(string(execOut(repoAbs, "log", "-1", "--format=%ct"))), 10, 64)
	if err != nil || headTs <= 0 {
		return ""
	}
	if buildTs < headTs {
		return fmt.Sprintf("索引可能过期（构建于 %s，HEAD 更新于 %s）；运行 codeintel update",
			time.Unix(buildTs, 0).Format("01-02 15:04"), time.Unix(headTs, 0).Format("01-02 15:04"))
	}
	return ""
}

// shortSHA 压缩 commit SHA 显示（前 8 位）。
func shortSHA(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// execOut 运行 git 命令返回 stdout（失败返回空）。
func execOut(repoAbs string, args ...string) []byte {
	full := append([]string{"-C", repoAbs}, args...)
	out, err := exec.Command("git", full...).Output()
	if err != nil {
		return nil
	}
	return out
}

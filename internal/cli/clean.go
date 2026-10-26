package cli

import (
	"flag"
	"fmt"
	"go.uber.org/zap"
	"os"
	"path/filepath"
)

// cmdClean 实现 `codeintel clean --repo <path>`（TD.md 10.2 数据清理）。
func cmdClean(args []string) int {
	logger := zap.L()
	logger.Debug("enter cmdClean")
	defer logger.Debug("exit cmdClean")
	fs := flag.NewFlagSet("clean", flag.ExitOnError)
	repoPath := fs.String("repo", ".", "仓库根目录")
	force := fs.Bool("force", false, "不提示直接删除")
	purgeCache := fs.Bool("purge-cache", false, "连包级分析缓存（.codeintel/cache）一起删除——默认保留")
	forget := fs.Bool("forget", false, "同时注销全局台账条目（默认保留 workspace/worktree 归属，重建后可恢复）")
	fs.Parse(args)
	*repoPath = ResolveRepoRef(*repoPath) // Q238：注册表短名/后缀/module

	abs, err := filepath.Abs(*repoPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	target := filepath.Join(abs, ".codeintel")
	if _, err := os.Stat(target); err != nil {
		fmt.Printf("没有找到索引目录 %s\n", target)
		return 0
	}
	// 删除范围明示（--force 只跳过确认，仍打印）：除包级缓存外全部内容，
	// 包括人工数据（连线规则 relation_rules、问答 qa_history）与时序图
	// （.codeintel/sequence/）——clean 是破坏性操作。
	fmt.Printf("删除范围：%s（含派生索引、人工连线规则 relation_rules、问答 qa_history、时序图 sequence）\n", target)
	if !*force {
		fmt.Printf("确定删除 %s ？输入 yes 确认: ", target)
		var answer string
		if _, err := fmt.Scanln(&answer); err != nil || answer != "yes" {
			fmt.Println("已取消")
			return 0
		}
	}
	if *purgeCache {
		if err := os.RemoveAll(target); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Printf("已删除 %s（含派生索引、人工规则/问答/时序图、包级分析缓存）\n", target)
		cleanRegistryAfterClean(abs, *forget)
		return 0
	}
	// 默认保留 .codeintel/cache（Q176 包级分析缓存）：pkg 源码 hash 自校验
	// + 格式版本——任何情况下不会用错缓存，删除纯浪费（重建时未变包
	// 的 emitFunction 可直接跳过）。磁盘清理用 --purge-cache。
	entries, err := os.ReadDir(target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	for _, e := range entries {
		if e.Name() == "cache" {
			continue
		}
		if err := os.RemoveAll(filepath.Join(target, e.Name())); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
	}
	fmt.Printf("已删除索引 %s（保留 .codeintel/cache 包级分析缓存；--purge-cache 可一并删除）\n", target)
	// todo12-15：默认保留台账元数据（含 workspace/worktree 归属）——
	// init 重建后自动恢复；--forget 才真正注销（独立清理语义）
	cleanRegistryAfterClean(abs, *forget)
	return 0
}

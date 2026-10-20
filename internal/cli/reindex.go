package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
	"github.com/schaepher/codeintel/internal/orchestrator"
	"github.com/schaepher/codeintel/internal/progress"
)

// cmdReindex 实现 `codeintel reindex --repo <path>`：一步重建索引——
// 与 init 相同走全量构建（FullBuild 清空图数据表 DROP+CREATE 重建，
// 保留 build_metadata 与配置表），语义明确为"重建"。
func cmdReindex(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("reindex", flag.ExitOnError)
	// Q237：--repo 缺省当前工作目录
	repoPath := fs.String("repo", ".", "仓库根目录（含 go.mod；默认当前目录）")
	fs.Int("workers", defaultBuildWorkers(), "SSA 分析并发数（Q221/Q252e：默认 min(NumCPU, 8)；透传给 init）")
	fs.String("progress", progress.ModeAuto, "构建进度显示（Q253：auto|plain|none；透传给 init）")
	fs.Duration("adapter-timeout", orchestrator.AdapterTimeout, "每个适配器独立超时（默认 10m；透传给 init）")
	fs.Parse(args)
	*repoPath = ResolveRepoRef(*repoPath) // Q238：注册表短名/后缀/module

	abs, err := filepath.Abs(*repoPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: resolve repo path: %v\n", err)
		return 1
	}
	fmt.Printf("重建索引: %s\n", abs)
	fmt.Println("说明：图数据（nodes/edges/函数摘要等）为派生数据，将整体重建；人工数据（relation_rules 连线规则、qa_history 问答）保留")
	// Q254/Q?：reindex 的语义是"重建"——检测到 schema 不兼容（如 edges
	// 改整数代理键）时不再删除整个数据库，而是**在事务中重置派生图表**
	// （保留 relation_rules / qa_history）；重置失败回滚，数据库保持原样。
	if db, err := sqlite.Open(abs); err != nil {
		if strings.Contains(err.Error(), "schema mismatch") {
			fmt.Printf("检测到旧索引 schema 不兼容——重置派生图表（保留 relation_rules/qa_history）\n  原因: %v\n", err)
			if rerr := sqlite.RebuildDerivedSchema(abs); rerr != nil {
				fmt.Fprintf(os.Stderr, "error: 重置派生图表失败（数据库未改动）: %v\n", rerr)
				return 1
			}
		} else {
			fmt.Fprintf(os.Stderr, "error: 打开索引失败（未删除数据库）: %v\n", err)
			return 1
		}
	} else {
		db.Close()
	}
	return cmdInit(ctx, args)
}

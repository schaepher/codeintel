package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
	"github.com/schaepher/codeintel/internal/progress"
)

// cmdReindex 实现 `codeintel reindex --repo <path>`：一步重建索引——
// 与 init 相同走全量构建（FullBuild 清空图数据表 DROP+CREATE 重建，
// 保留 build_metadata 与配置表），语义明确为"重建"。
func cmdReindex(ctx context.Context, args []string) int {
	repoPath, adapterTimeout, err := parseReindexArgs(args)
	if err != nil {
		// todo10-1：非法参数在任何库操作之前拒绝——否则会先
		// RebuildDerivedSchema（重置派生图表）才发现参数错。
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}
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
	return cmdInit(ctx, initArgs(args, adapterTimeout))
}

// parseReindexArgs 解析 reindex 参数（repo/workers/progress/adapter-timeout）。
// 除 --repo 外的参数只用于**校验与透传**（构建由 cmdInit 执行）。
// adapter-timeout 返回 0 表示未显式指定（沿用 orchestrator 默认 10m）；
// 显式传 0/负值返回错误（todo10-1：非法值不得进入库操作）。
func parseReindexArgs(args []string) (repoPath *string, adapterTimeout time.Duration, err error) {
	fs := flag.NewFlagSet("reindex", flag.ExitOnError)
	// Q237：--repo 缺省当前工作目录
	repoPath = fs.String("repo", ".", "仓库根目录（含 go.mod；默认当前目录）")
	fs.Int("workers", action.DefaultWorkers(), "SSA 分析并发数（Q221/Q252e：默认 min(NumCPU, 8)；透传给 init）")
	fs.String("progress", progress.ModeAuto, "构建进度显示（Q253：auto|plain|none；透传给 init）")
	// 默认 0 = 未指定（不能把默认 10m 当作"用户传了 10m"——否则
	// `--adapter-timeout 0` 与未传无法区分）
	timeoutFlag := fs.Duration("adapter-timeout", 0, "每个适配器独立超时（默认 10m；透传给 init，必须为正时长）")
	fs.Parse(args)
	explicit := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "adapter-timeout" {
			explicit = true
		}
	})
	if explicit {
		if *timeoutFlag <= 0 {
			return repoPath, 0, fmt.Errorf("--adapter-timeout 必须为正时长（收到 %s）", *timeoutFlag)
		}
		return repoPath, *timeoutFlag, nil
	}
	return repoPath, 0, nil
}

// initArgs 透传给 cmdInit 的参数：显式指定超时时追加规范化写法
// （`--adapter-timeout=<v>`）——不依赖 init 对原始形式的二次解析。
func initArgs(args []string, adapterTimeout time.Duration) []string {
	if adapterTimeout <= 0 {
		return args
	}
	out := make([]string, 0, len(args)+1)
	out = append(out, args...)
	return append(out, "--adapter-timeout="+adapterTimeout.String())
}

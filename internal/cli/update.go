package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/logging"
	"github.com/schaepher/codeintel/internal/orchestrator"
	"github.com/schaepher/codeintel/internal/progress"
	"go.uber.org/zap"
)

// cmdUpdate 实现 `codeintel update --repo <path>`（增量更新，TD.md 5.2 增量语义）：
// git 检测变更的 .go 文件 → 删除其旧数据 → 全量分析 + 只写变更文件相关数据。
// go.mod / go.work 变更影响 module 范围，须全量 init。
// P1-03：构建流程在 action.Build（与 MCP update 同源）；本函数只做参数解析 →
// 转发 → 渲染 + 台账刷新。
func cmdUpdate(ctx context.Context, args []string) int {
	logger := zap.L()
	logger.Debug("enter cmdUpdate")
	defer logger.Debug("exit cmdUpdate")
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	// Q237：--repo 缺省当前工作目录
	repoPath := fs.String("repo", ".", "仓库根目录（须已运行 codeintel init 且为 git 仓库；默认当前目录）")
	workers := fs.Int("workers", action.DefaultWorkers(), "SSA 分析并发数（Q221/Q252e：默认 min(NumCPU, 8)）")
	progressMode := fs.String("progress", progress.ModeAuto, "构建进度显示（Q253：只写 stderr）：auto=终端时进度条、否则逐行；plain=逐行；none=不显示")
	adapterTimeout := fs.Duration("adapter-timeout", orchestrator.AdapterTimeout, "每个适配器独立超时（默认 10m；大仓可显式调大，必须为正时长）")
	// R85：--base 分层——base 目录（完整索引，只读共享）。变更基准 =
	// base HEAD（diff base..当前），只分析变更包；base 数据物化到本地
	baseDir := fs.String("base", "", "base 分支目录（其 .codeintel 为完整索引；变更检测基准 = base HEAD）")
	fs.Parse(args)
	if err := validateAdapterTimeout(fs, *adapterTimeout); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}
	*repoPath = ResolveRepoRef(*repoPath) // Q238：注册表短名/后缀/module

	abs, err := filepath.Abs(*repoPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: resolve repo path: %v\n", err)
		return 1
	}
	// Q182：分析器版本变化（codeintel 新特性/逻辑变更）→ 自动降级全量
	// 重建（增量写库范围无法覆盖未变更包，新特性须全量生效）
	if action.AnalyzerVersionChanged(abs) {
		fmt.Printf("分析器版本变化（codeintel 新特性/逻辑变更），本次执行全量重建——未变更包也将以新逻辑重建\n")
	}
	// 日志切换到 .codeintel/codeintel.log（stdout 只留查询结果，Q88）
	if err := logging.ToFile(abs); err != nil {
		fmt.Fprintf(os.Stderr, "warning: 日志切换失败: %v\n", err)
	}
	rep := progress.New(os.Stderr, progress.Config{Mode: *progressMode, Prefix: "[index] 步骤", Title: "增量更新"})
	out, err := action.Build(ctx, action.BuildRequest{
		RepoPath: abs, Mode: action.BuildIncremental,
		Workers: *workers, AdapterTimeout: *adapterTimeout,
		BaseDir: *baseDir, Progress: rep,
		// UI：变更清单在构建开始前打印（与改动前输出顺序一致）
		OnPlan: func(o *action.BuildOutcome) {
			fmt.Printf("增量更新: %s (%d 个文件变更)\n", abs, len(o.Changed))
			for _, f := range o.Changed {
				fmt.Printf("  - %s\n", f)
			}
		},
	})
	rep.Finish()
	if out != nil {
		for _, w := range out.Warnings {
			fmt.Fprintf(os.Stderr, "warning: %s\n", w)
		}
		if out.BaseMaterialized {
			// 物化 = 完整索引（等价全量）
			fmt.Printf("物化 base 索引: %s\n", out.BaseDir)
		}
		if out.NoChange {
			fmt.Println("无变更的 .go 文件（索引已是最新）")
			return 0
		}
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	printVacuumOutcome(out.Vacuum)
	result := out.Result
	if result == nil {
		fmt.Fprintln(os.Stderr, "error: 构建未返回结果")
		return 1
	}

	// 构建报告（TD.md 6.1，tool_name=incremental）
	fmt.Println()
	fmt.Println("===== 增量更新报告 =====")
	for _, a := range result.Adapter {
		mark := "ok"
		if a.Err != nil {
			mark = "FAILED: " + a.Err.Error()
		}
		fmt.Printf("  %-10s %s (%s)\n", a.Name, mark, a.Duration.Round(time.Millisecond))
	}
	fmt.Printf("  变更文件: %d\n", len(out.Changed))
	fmt.Printf("  符号数:   %d\n", result.Nodes)
	fmt.Printf("  边数:     %d\n", result.Edges)
	if result.SkippedEdges > 0 {
		fmt.Printf("  跳过边:   %d\n", result.SkippedEdges)
	}
	fmt.Printf("  状态:     %s\n", result.Status)
	fmt.Printf("  耗时:     %s\n", result.Duration.Round(time.Millisecond))
	// R6：SQL 解析降级统计（AST 死代码类问题提前暴露）
	if ds := result.DegradeStats; ds != "" {
		fmt.Printf("  SQL 解析: %s\n", ds)
	}
	fmt.Println("=========================")
	if result.Status == domain.BuildFailed {
		fmt.Fprintln(os.Stderr, "增量更新失败：SCIP 符号索引不可用。请检查 scip-go 是否安装。")
		return 1
	}
	if result.Status == domain.BuildDegraded {
		fmt.Fprintln(os.Stderr, "警告：增量更新降级完成（部分工具失败，已保留可用数据）。")
	}
	// Q238：update 成功刷新全局台账构建状态（registered_at 不变）
	refreshRepoAfterUpdate(abs, result.BuildID, result.CommitSHA)
	return 0
}

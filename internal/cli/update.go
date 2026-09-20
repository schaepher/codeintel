package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
	"github.com/schaepher/codeintel/internal/infrastructure/ssa"
	"github.com/schaepher/codeintel/internal/logging"
	"github.com/schaepher/codeintel/internal/orchestrator"
	"github.com/schaepher/codeintel/internal/progress"
	"go.uber.org/zap"

	_ "modernc.org/sqlite"
)

// cmdUpdate 实现 `codeintel update --repo <path>`（增量更新，TD.md 5.2 增量语义）：
// git 检测变更的 .go 文件 → 删除其旧数据 → 全量分析 + 只写变更文件相关数据。
// go.mod / go.work 变更影响 module 范围，须全量 init。
func cmdUpdate(ctx context.Context, args []string) int {
	logger := zap.L()
	logger.Debug("enter cmdUpdate")
	defer logger.Debug("exit cmdUpdate")
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	// Q237：--repo 缺省当前工作目录
	repoPath := fs.String("repo", ".", "仓库根目录（须已运行 codeintel init 且为 git 仓库；默认当前目录）")
	workers := fs.Int("workers", defaultBuildWorkers(), "SSA 分析并发数（Q221/Q252e：默认 min(NumCPU, 8)）")
	progressMode := fs.String("progress", progress.ModeAuto, "构建进度显示（Q253：只写 stderr）：auto=终端时进度条、否则逐行；plain=逐行；none=不显示")
	// R85：--base 分层——base 目录（完整索引，只读共享）。变更基准 =
	// base HEAD（diff base..当前），只分析变更包；base 数据物化到本地
	baseDir := fs.String("base", "", "base 分支目录（其 .codeintel 为完整索引；变更检测基准 = base HEAD）")
	fs.Parse(args)
	*repoPath = ResolveRepoRef(*repoPath) // Q238：注册表短名/后缀/module

	abs, err := filepath.Abs(*repoPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: resolve repo path: %v\n", err)
		return 1
	}
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		fmt.Fprintf(os.Stderr, "error: %s is not a directory\n", abs)
		return 1
	}
	repo, err := buildRepo(abs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	// R85：--base 目录解析（相对当前工作目录）
	baseAbs := ""
	if *baseDir != "" {
		b, err := filepath.Abs(*baseDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: resolve base dir: %v\n", err)
			return 1
		}
		if fi, err := os.Stat(b); err != nil || !fi.IsDir() {
			fmt.Fprintf(os.Stderr, "error: --base %s is not a directory\n", b)
			return 1
		}
		baseAbs = b
	}

	// 变更检测：--base 时基准 = base HEAD（diff base..当前 + 工作区）；
	// 否则默认（本地索引 commit 或 HEAD）
	var changed []string
	if baseAbs != "" {
		baseCommit, err := repoCommitSHA(baseAbs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: 读 base 目录 commit: %v\n", err)
			return 1
		}
		changed, err = detectChangedGoFilesSince(abs, baseCommit)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
	} else if changed, err = detectChangedGoFiles(abs); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if len(changed) == 0 && baseAbs == "" {
		fmt.Println("无变更的 .go 文件（索引已是最新）")
		return 0
	}
	// R85：--base 模式即使无变更也继续（物化 base 索引——本地空库/新
	// workspace 首次建立分层）
	// module 级文件变更：影响模块范围，提示全量重建
	for _, f := range changed {
		if f == "go.mod" || f == "go.work" {
			fmt.Fprintf(os.Stderr, "error: %s 已变更，影响模块范围，请运行: codeintel init --repo %s\n", f, abs)
			return 1
		}
	}

	// Q182：分析器版本变化（codeintel 新特性/逻辑变更）→ 自动降级全量
	// 重建（增量写库范围无法覆盖未变更包，新特性须全量生效）
	degraded := ssa.LoadAnalyzerMarker(abs) != ssa.AnalyzerVersionHash()
	if degraded {
		fmt.Printf("分析器版本变化（codeintel 新特性/逻辑变更），本次执行全量重建——未变更包也将以新逻辑重建\n")
	}
	fmt.Printf("增量更新: %s (%d 个文件变更)\n", abs, len(changed))
	for _, f := range changed {
		fmt.Printf("  - %s\n", f)
	}
	_ = degraded
	// 日志切换到 .codeintel/codeintel.log（stdout 只留查询结果，Q88）
	if err := logging.ToFile(abs); err != nil {
		fmt.Fprintf(os.Stderr, "warning: 日志切换失败: %v\n", err)
	}
	db, err := sqlite.Open(abs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer db.Close()

	// R85：--base 分层——base 索引物化到本地（幂等：同一 base commit
	// 跳过），之后增量只分析 diff(base..HEAD) 的变更包
	if baseAbs != "" {
		if err := db.SetBase(baseAbs); err != nil {
			fmt.Fprintf(os.Stderr, "warning: 记录 base 配置失败: %v\n", err)
		}
		materialized, err := sqlite.NewRepo(db).MaterializeBase(baseAbs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: 物化 base 索引: %v\n", err)
			return 1
		}
		if materialized {
			fmt.Printf("物化 base 索引: %s\n", baseAbs)
			// 物化 = 完整索引（等价全量）——写 analyzer marker，避免
			// IncrementalBuild 因缺失 marker 降级全量（新 workspace
			// 首次 --base 白物化 + 全量重跑）
			if merr := ssa.SaveAnalyzerMarker(abs); merr != nil {
				fmt.Fprintf(os.Stderr, "warning: 写 analyzer marker 失败: %v\n", merr)
			}
		}
	}

	orch := orchestrator.New(repo, db)
	orch.SetWorkers(*workers)
	// Q254b：工作区指纹用 dirtyGoFiles（相对 HEAD 的脏文件）——与查询期同口径，
	// 不受"索引 commit 落后"影响（那部分由 SHA 比较负责）
	if dirtyGo, derr := dirtyGoFiles(abs); derr == nil {
		orch.SetWorktreeFingerprint(worktreeFingerprint(abs, dirtyGo))
	}
	rep := progress.New(os.Stderr, progress.Config{Mode: *progressMode, Prefix: "[index] 步骤", Title: "增量更新"})
	orch.SetProgress(rep)
	result, err := orch.IncrementalBuild(ctx, changed)
	rep.Finish()
	// Q254：增量更新会按文件删旧数据（产生空洞）——VACUUM 只在真有 freelist
	// 且磁盘够时才做（全量构建路径同理，见 init.go）
	if plan, verr := db.VacuumIfWorthwhile("incremental update"); verr != nil {
		fmt.Fprintf(os.Stderr, "warning: VACUUM: %v\n", verr)
	} else if plan.Run {
		fmt.Fprintf(os.Stderr, "[index] VACUUM 已回收 %dMB\n", plan.FreeListBytes>>20)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
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
	fmt.Printf("  变更文件: %d\n", len(changed))
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
	refreshRepoAfterUpdate(abs, result.CommitSHA)
	return 0
}

// repoCommitSHA 返回目录的 git HEAD commit（--base 变更基准）。

// detectChangedGoFilesSince 检测相对指定 commit 的变更 Go 源文件
// （R85 --base 场景：diff base..HEAD + 工作区 + 未跟踪；返回 .go 与
// go.mod/go.work，module 级变更由调用方处理）。

// detectChangedGoFiles 检测仓库中变更的 Go 源文件（相对路径）：
//   - 索引 commit 落后于 HEAD（build_metadata 最新 commit_sha ≠ HEAD）：
//     git diff --name-only <buildSHA> HEAD——提交内变更（工作区干净时
//     git diff HEAD 检测不到，索引基于旧 commit 的场景）
//   - git diff --name-only HEAD：已跟踪文件的修改/删除/新增
//   - git ls-files --others --exclude-standard：未跟踪文件（含新文件）
//
// 返回 .go 文件与 go.mod/go.work（module 级变更由调用方处理）。

// indexCommitSHA 返回索引最新构建的 commit_sha（build_metadata 最新记录）。
// 索引不存在 / 无构建记录 / 读取失败 → 空串（回退工作区检测）。

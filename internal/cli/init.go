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
	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/logging"
	"github.com/schaepher/codeintel/internal/orchestrator"
	"github.com/schaepher/codeintel/internal/progress"
	"go.uber.org/zap"
)

// cmdInit 实现 `codeintel init --repo <path>`（TD.md 6.1）。
// P1-03：构建流程在 action.Build（CLI init/update/reindex 与 MCP
// init/update 同源）；本函数只做参数解析 → 转发 → 渲染 + 台账登记。
func cmdInit(ctx context.Context, args []string) int {
	logger := zap.L()
	logger.Debug("enter cmdInit")
	defer logger.Debug("exit cmdInit")
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	// Q237：--repo 缺省当前工作目录（在目标仓库内直接 codeintel init）
	repoPath := fs.String("repo", ".", "仓库根目录（含 go.mod；默认当前目录）")
	workers := fs.Int("workers", action.DefaultWorkers(), "SSA 分析并发数（Q221/Q252e：默认 min(NumCPU, 8)——同时约束 ①模块包级 SSA 建图（Q252e 前逐包串行）②函数级发射；峰值内存与并发度正相关，小内存机器可调小，如 1）")
	progressMode := fs.String("progress", progress.ModeAuto, "构建进度显示（Q253：只写 stderr，stdout 仍只承载结果）：auto=stderr 是终端时用进度条、否则逐行；plain=逐行（agent/日志抓取）；none=不显示")
	adapterTimeout := fs.Duration("adapter-timeout", orchestrator.AdapterTimeout, "每个适配器独立超时（默认 10m；大仓可显式调大，必须为正时长）")
	fs.Parse(args)
	*repoPath = ResolveRepoRef(*repoPath) // Q238：注册表短名/后缀/module

	abs, err := filepath.Abs(*repoPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: resolve repo path: %v\n", err)
		return 1
	}
	// 目标校验（目录/go.work）先于任何头部输出
	if err := action.ValidateBuildTarget(abs); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	// 头部信息（module/忽略目录）要先于构建输出——只做一次发现，
	// 结果注入 Build（不重复扫描 go.mod）
	repo, err := action.DiscoverRepository(abs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Printf("构建索引: %s (module=%s, %d 个 go.mod)\n", abs, repo.Module, len(repo.Modules))
	if len(repo.IgnoreDirs) > 0 {
		fmt.Printf("忽略目录: %s（codeintel.yaml）\n", strings.Join(repo.IgnoreDirs, ", "))
	}
	// 日志切换到 .codeintel/codeintel.log（stdout 只留查询结果，Q88）
	if err := logging.ToFile(abs); err != nil {
		fmt.Fprintf(os.Stderr, "warning: 日志切换失败: %v\n", err)
	}
	rep := progress.New(os.Stderr, progress.Config{Mode: *progressMode, Prefix: "[index] 步骤", Title: "构建索引"})
	out, err := action.Build(ctx, action.BuildRequest{
		RepoPath: abs, Repo: repo, Mode: action.BuildFull,
		Workers: *workers, AdapterTimeout: *adapterTimeout, Progress: rep,
	})
	rep.Finish() // 关闭进度活动行（无论成败）
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	printVacuumOutcome(out.Vacuum)
	result := out.Result

	// 构建报告（TD.md 6.1）
	fmt.Println()
	fmt.Println("===== 构建报告 =====")
	for _, a := range result.Adapter {
		mark := "ok"
		if a.Err != nil {
			mark = "FAILED: " + a.Err.Error()
		}
		fmt.Printf("  %-10s %s (%s)\n", a.Name, mark, a.Duration.Round(time.Millisecond))
	}
	fmt.Printf("  符号数: %d\n", result.Nodes)
	fmt.Printf("  边数:   %d\n", result.Edges)
	if result.SkippedEdges > 0 {
		fmt.Printf("  跳过边: %d (端点非索引对象)\n", result.SkippedEdges)
	}
	fmt.Printf("  状态:   %s\n", result.Status)
	if ds := result.DegradeStats; ds != "" {
		fmt.Printf("  SQL 解析: %s\n", ds)
	}
	fmt.Printf("  耗时:   %s\n", result.Duration.Round(time.Millisecond))
	if result.CommitSHA != "" {
		fmt.Printf("  HEAD:   %s\n", result.CommitSHA[:12])
	}
	fmt.Println("=========================")

	if result.Status == domain.BuildFailed {
		fmt.Fprintln(os.Stderr, "构建失败：SCIP 符号索引不可用。请检查 scip-go 是否安装。")
		return 1
	}
	if result.Status == domain.BuildDegraded {
		fmt.Fprintln(os.Stderr, "警告：构建降级完成（部分工具失败，已保留可用数据）。")
	}
	fmt.Printf("数据库: %s/.codeintel/codeintel.db\n", abs)
	// Q244：引导示例（普通程序员入口——before/trace/relations）
	fmt.Println()
	fmt.Println("试试这些：")
	fmt.Printf("  codeintel query symbol %s       符号详情\n", repo.Module)
	fmt.Printf("  codeintel before <符号|字段|表>   改前影响预判\n")
	fmt.Printf("  codeintel trace <字段|符号>       数据来龙去脉\n")
	fmt.Printf("  codeintel query table <表名>      表列数据流\n")
	fmt.Printf("  codeintel query relations <表名>  表间关联\n")
	fmt.Printf("  codeintel serve --repo %s   启动 Web 探索\n", abs)
	// Q238：构建成功注册全局台账（路径/module/HEAD/worktree 归属）
	registerRepoAfterBuild(abs, repo.Module, len(repo.Modules), result.CommitSHA)
	// #234：询问安装 post-commit 自动更新 hook（仅交互终端；默认不装）
	if isTerminal(os.Stdin) {
		_ = maybeInstallHook(abs, func() bool {
			fmt.Print("安装 post-commit hook（提交后自动更新索引）? [y/N] ")
			var s string
			if _, err := fmt.Scanln(&s); err != nil {
				return false
			}
			return strings.EqualFold(strings.TrimSpace(s), "y")
		})
	}
	return 0
}

// printVacuumOutcome VACUUM 判定结果输出（Q254：跳过时把原因写给用户）。
func printVacuumOutcome(v action.VacuumOutcome) {
	switch {
	case !v.Evaluated:
		return
	case v.Err != "":
		fmt.Fprintf(os.Stderr, "warning: VACUUM: %s\n", v.Err)
	case v.Ran:
		fmt.Fprintf(os.Stderr, "[index] VACUUM 已回收 %dMB（freelist %dMB）\n",
			v.ReclaimedBytes>>20, v.ReclaimedBytes>>20)
	default:
		fmt.Fprintf(os.Stderr, "[index] 跳过 VACUUM：%s\n", v.Reason)
	}
}

// buildRepo 仓库 module 发现（P1-03：实现迁 action.DiscoverRepository——
// 保留此薄封装给 serve/wiki/query 等需要 Repository 的调用点）。
func buildRepo(repoPath string, ignoreDirs ...string) (*domain.Repository, error) {
	return action.DiscoverRepository(repoPath, ignoreDirs...)
}

// readGoModule 读取 go.mod 的 module 行。
func readGoModule(repoPath string) (string, error) {
	logger := zap.L()
	logger.Debug("enter readGoModule")
	defer logger.Debug("exit readGoModule")
	data, err := os.ReadFile(filepath.Join(repoPath, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("read go.mod (repo must be a Go module): %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			m := strings.TrimSpace(strings.TrimPrefix(line, "module "))
			if i := strings.Index(m, " "); i >= 0 {
				m = m[:i]
			}
			return m, nil
		}
	}
	return "", fmt.Errorf("no module directive found in go.mod")
}

// resolveRepo 从参数解析仓库路径（默认当前目录），并验证存在 go.mod。
func resolveRepo(repoPath string) (string, string, error) {
	logger := zap.L()
	logger.Debug("enter resolveRepo")
	defer logger.Debug("exit resolveRepo")
	if repoPath == "" {
		repoPath = "."
	}
	repoPath = ResolveRepoRef(repoPath) // Q238：注册表短名/后缀/module 解析
	abs, err := filepath.Abs(repoPath)
	if err != nil {
		return "", "", err
	}
	module, err := readGoModule(abs)
	if err != nil {
		return "", "", err
	}
	return abs, module, nil
}

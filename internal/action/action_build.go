package action

// P1-03（构建流程收口）：索引构建编排从 cli 迁 action——`init`/`update`/
// `reindex`（CLI）与 MCP 的 `init`/`update` 工具此前各写一遍，MCP 那份
// 缺装配（不设 workers/适配器超时、不写工作区指纹、不做 VACUUM 判定），
// 导致"MCP 全量构建后 stale 判定误报"这类分叉。现在唯一实现是
// action.Build：
//
//	校验（目录/go.work/module）→ 变更检测（增量）→ 打开库 → （--base
//	物化）→ orchestrator 装配（workers/超时/指纹/进度）→ 构建 →
//	VACUUM 判定
//
// 调用方只负责：参数解析、日志落文件（logging.ToFile）、进度渲染器构造、
// 结果渲染、全局台账登记（ops）。

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
	"github.com/schaepher/codeintel/internal/infrastructure/ssa"
	"github.com/schaepher/codeintel/internal/logging"
	"github.com/schaepher/codeintel/internal/orchestrator"
	"go.uber.org/zap"
)

// BuildMode 构建模式。
type BuildMode string

const (
	BuildFull        BuildMode = "full"        // 全量（init/reindex）
	BuildIncremental BuildMode = "incremental" // 增量（update）
)

// BuildRequest 构建请求（RepoPath 为绝对路径；调用方已完成 --repo 短名解析）。
type BuildRequest struct {
	RepoPath       string             // 仓库根绝对路径
	Mode           BuildMode          // 全量 / 增量
	Workers        int                // SSA 并发（0 = DefaultWorkers()）
	AdapterTimeout time.Duration      // 每适配器超时（0 = orchestrator 默认；负值非法）
	BaseDir        string             // 增量 --base 目录绝对路径（R85 分层，可选）
	Progress       domain.Progress    // 进度渲染（nil = 静默）
	Repo           *domain.Repository // 已发现的仓库信息（nil = Build 自行发现）
	// OnPlan 变更检测完成后、构建开始前的回调（cli 打印"增量更新 N 个
	// 文件"+ 文件清单这类 UI 行为；仅增量调用）。与 PrecomputeRelations
	// 的 progressFn 同款注入方式。
	OnPlan func(*BuildOutcome)
}

// VacuumOutcome VACUUM 判定结果（渲染由调用方负责）。
type VacuumOutcome struct {
	Evaluated      bool   // 是否执行了判定（构建失败时不判定）
	Ran            bool   // 是否真的跑了 VACUUM
	Reason         string // 未跑的原因（Ran=false 时）
	ReclaimedBytes int64  // freelist 字节数（Ran=true 时展示为"已回收"）
	Err            string // VACUUM 失败信息（空=无错）
}

// BuildOutcome 构建结果。
type BuildOutcome struct {
	Repo             *domain.Repository
	Result           *orchestrator.BuildResult // NoChange/NeedsFull 时为 nil
	Changed          []string                  // 参与增量分析的文件
	NoChange         bool                      // 增量无变更（调用方提示"已是最新"）
	NeedsFull        bool                      // go.mod/go.work 变更（须改全量）
	AnalyzerDegraded bool                      // 分析器版本变化（增量实际走了全量）
	BaseDir          string                    // --base 目录（绝对路径；空=未启用）
	BaseMaterialized bool                      // --base 索引本次物化到本地
	Warnings         []string                  // 非致命问题（登记 base 失败等）
	Vacuum           VacuumOutcome
}

// Build 执行一次全量或增量构建。
//
// 返回的 error 已包含可直接展示的原因（cli 打印 "error: %v"）；增量场景
// 下 NeedsFull/NoChange 通过 outcome 表达（NoChange 不是错误）。
func Build(ctx context.Context, req BuildRequest) (*BuildOutcome, error) {
	logger := logging.FromContext(ctx)
	logger.Info("enter action.Build", zap.String("repo", req.RepoPath),
		zap.String("mode", string(req.Mode)))
	defer logger.Info("exit action.Build")

	abs := filepath.Clean(req.RepoPath)
	if err := ValidateBuildTarget(abs); err != nil {
		return nil, err
	}
	repo := req.Repo
	if repo == nil {
		discovered, derr := DiscoverRepository(abs)
		if derr != nil {
			return nil, derr
		}
		repo = discovered
	}
	out := &BuildOutcome{Repo: repo}
	if req.AdapterTimeout < 0 {
		return out, fmt.Errorf("--adapter-timeout 必须为正时长（收到 %s）", req.AdapterTimeout)
	}
	if req.Mode != BuildFull && req.Mode != BuildIncremental {
		return out, fmt.Errorf("未知构建模式 %q", req.Mode)
	}

	// ---- 变更检测（增量）----
	baseAbs := ""
	if req.BaseDir != "" {
		b, berr := filepath.Abs(req.BaseDir)
		if berr != nil {
			return out, fmt.Errorf("resolve base dir: %w", berr)
		}
		if fi, serr := os.Stat(b); serr != nil || !fi.IsDir() {
			return out, fmt.Errorf("--base %s is not a directory", b)
		}
		baseAbs = b
		out.BaseDir = b
	}
	if req.Mode == BuildIncremental {
		var derr error
		if baseAbs != "" {
			baseCommit, cerr := RepoCommitSHA(baseAbs)
			if cerr != nil {
				return out, fmt.Errorf("读 base 目录 commit: %w", cerr)
			}
			out.Changed, derr = DetectChangedGoFilesSince(abs, baseCommit)
		} else {
			out.Changed, derr = DetectChangedGoFiles(abs)
		}
		if derr != nil {
			return out, derr
		}
		// 忽略目录（codeintel.yaml 的 ignore_dirs）：其中的变更不入增量分析
		out.Changed = FilterIgnoredFiles(out.Changed, repo.IgnoreDirs)
		if len(out.Changed) == 0 && baseAbs == "" {
			out.NoChange = true
			return out, nil
		}
		// R85：--base 模式即使无变更也继续（物化 base 索引——本地空库/
		// 新 workspace 首次建立分层）
		// module 级文件变更：影响模块范围，提示全量重建
		for _, f := range out.Changed {
			if f == "go.mod" || f == "go.work" {
				out.NeedsFull = true
				return out, fmt.Errorf("%s 已变更，影响模块范围，请运行: codeintel init --repo %s", f, abs)
			}
		}
	}

	if req.Mode == BuildIncremental && req.OnPlan != nil {
		req.OnPlan(out)
	}

	// ---- 打开库 + --base 物化 ----
	db, err := sqlite.Open(abs)
	if err != nil {
		return out, err
	}
	defer db.Close()
	if baseAbs != "" {
		if err := db.SetBase(baseAbs); err != nil {
			out.Warnings = append(out.Warnings, fmt.Sprintf("记录 base 配置失败: %v", err))
		}
		materialized, merr := sqlite.NewRepo(db).MaterializeBase(baseAbs)
		if merr != nil {
			return out, fmt.Errorf("物化 base 索引: %w", merr)
		}
		out.BaseMaterialized = materialized
		if materialized {
			// 物化 = 完整索引（等价全量）——写 analyzer marker，避免
			// IncrementalBuild 因缺失 marker 降级全量
			if serr := ssa.SaveAnalyzerMarker(abs); serr != nil {
				out.Warnings = append(out.Warnings, fmt.Sprintf("写 analyzer marker 失败: %v", serr))
			}
		}
	}

	// ---- orchestrator 装配（CLI/MCP 同源：workers/超时/指纹/进度）----
	workers := req.Workers
	if workers <= 0 {
		workers = DefaultWorkers()
	}
	timeout := req.AdapterTimeout
	if timeout == 0 {
		timeout = orchestrator.AdapterTimeout
	}
	out.AnalyzerDegraded = AnalyzerVersionChanged(abs)
	orch := orchestrator.New(repo, db)
	orch.SetWorkers(workers)
	if terr := orch.SetAdapterTimeout(timeout); terr != nil {
		return out, terr
	}
	// Q254b：记录构建时的工作区变更指纹（stale 判定用——"改文件→重索引"
	// 之后不该再报过期）；口径用 DirtyGoFiles（相对 HEAD），与查询期一致
	if dirty, derr := DirtyGoFiles(abs); derr == nil {
		orch.SetWorktreeFingerprint(WorktreeFingerprint(abs, FilterIgnoredFiles(dirty, repo.IgnoreDirs)))
	}
	if req.Progress != nil {
		orch.SetProgress(req.Progress)
	}

	// ---- 构建 ----
	if req.Mode == BuildFull {
		out.Result, err = orch.FullBuild(ctx)
	} else {
		out.Result, err = orch.IncrementalBuild(ctx, out.Changed)
	}
	if err != nil {
		return out, err
	}

	// ---- VACUUM（Q254：按 freelist 判定，两个条件都满足才跑）----
	label := "full build"
	if req.Mode == BuildIncremental {
		label = "incremental update"
	}
	out.Vacuum.Evaluated = true
	plan, verr := db.VacuumIfWorthwhile(label)
	switch {
	case verr != nil:
		out.Vacuum.Err = verr.Error()
	case plan.Run:
		out.Vacuum.Ran = true
		out.Vacuum.ReclaimedBytes = plan.FreeListBytes
	default:
		out.Vacuum.Reason = plan.Reason
	}
	return out, nil
}

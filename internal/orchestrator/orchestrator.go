// Package orchestrator 实现 Index Orchestrator（TD.md 6.1 全量构建流程）：
//   - 并行启动所有适配器 goroutine，每个独立超时（默认 10 分钟）
//   - 适配器流式数据 → 分批（1000 条/事务）写入 SQLite
//   - 某适配器失败：已提交数据保留，构建标记降级
//   - SCIP 失败：构建失败（符号权威缺失）
package orchestrator

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
	"github.com/schaepher/codeintel/internal/progress"
	"go.uber.org/zap"
)

// 常量
const (
	AdapterTimeout = 10 * time.Minute // 适配器级超时（TD.md 9.1）
	BatchSize      = 20000            // 分批事务大小（TD.md 5.2；Q171/Q174 双缓冲+加大摊薄事务——大仓库 36 万 item 时减少 flush 次数；Q221：10000→20000 减半 cgocall——pprof 16% 在 SQLite C 调用）
)

// AdapterResult 记录单个适配器的执行结果。
type AdapterResult struct {
	Name     string
	Duration time.Duration
	Err      error
	// P0-2：适配器收集的 dispatch 相关模块内包（ssa 适配器产出）——
	// finishBuild 汇总写入 build_metadata，增量构建补 Load 用
	DispatchPkgs []string
}

// BuildResult 全量构建报告。
type BuildResult struct {
	Status       domain.BuildStatus
	Nodes        int
	Edges        int
	Duration     time.Duration
	CommitSHA    string
	Adapter      []AdapterResult
	SkippedEdges int // 因外键冲突跳过的边（日志用）
	// R6：SQL 解析降级统计 JSON（{"sql_ast_ok":..,"sql_ast_fail":..,
	// "sql_heuristic":..}）——构建期降级可观测（AST 死代码类问题
	// 提前暴露，不再静默）
	DegradeStats string
}

// Orchestrator 编排全量构建。
type Orchestrator struct {
	Repo     *domain.Repository
	Adapters []domain.IndexerPort
	RepoImpl *sqlite.Repo

	// Progress 进度上报（Q253，cli 注入；nil → 逐行 stderr 保底）。
	// 构建期各阶段与各适配器共用同一实例（TTY 模式单活动行）。
	Progress  domain.Progress
	plainProg *progress.Plain

	// worktreeFP 构建前由 cli 计算的工作区变更 Go 文件内容指纹（Q254b：
	// stale 判定——"改文件→重索引"之后不该再报过期）。
	worktreeFP string

	// P2 跨批 FK 收集：flush 时端点节点尚未落库的边/摘要/来源，构建尾部
	// （全部节点落库后）统一重试——原实现静默跳过导致非确定性丢边。
	// 仅 flush 协程写、finish 阶段读（flushCh 关闭 + flushWg.Wait 同步）。
	failedEdges     []*domain.Fact
	failedSummaries []*domain.FunctionFieldSummary
	failedOrigins   []*domain.SummaryOrigin
}

// SetProgress 注入进度上报，并转达给支持进度上报的适配器（ssa）。
func (o *Orchestrator) SetProgress(p domain.Progress) {
	o.Progress = p
	for _, a := range o.Adapters {
		if sp, ok := a.(interface{ SetProgress(domain.Progress) }); ok {
			sp.SetProgress(p)
		}
	}
}

// SetWorktreeFingerprint 注入构建前的工作区内容指纹（cli 计算，写入
// build_metadata，供后续 stale 判定比对）。
func (o *Orchestrator) SetWorktreeFingerprint(fp string) { o.worktreeFP = fp }

// prog 返回进度实现（注入优先；nil → 逐行 stderr，格式与 Q253 前一致）。
func (o *Orchestrator) prog() domain.Progress {
	if o.Progress != nil {
		return o.Progress
	}
	if o.plainProg == nil {
		o.plainProg = progress.NewPlain(os.Stderr, "[index] 步骤")
	}
	return o.plainProg
}

type batchT struct {
	nodes     []*domain.CodeEntity
	edges     []*domain.Fact
	summaries []*domain.FunctionFieldSummary
	origins   []*domain.SummaryOrigin // Q161 摘要来源
}

func newBuildID() string {
	logger := zap.L()
	logger.Debug("enter newBuildID")
	defer logger.Debug("exit newBuildID")
	h := sha256.Sum256([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))
	return hex.EncodeToString(h[:8])
}

func headCommitSHA(repoPath string) string {
	logger := zap.L()
	logger.Debug("enter headCommitSHA")
	defer logger.Debug("exit headCommitSHA")
	if _, err := os.Stat(filepath.Join(repoPath, ".git")); err != nil {
		return ""
	}
	out, err := exec.Command("git", "-C", repoPath, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

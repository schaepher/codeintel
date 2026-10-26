package action

// R95 迁移：`query sequence --code` 查询逻辑（原 cli/query_sequence_code.go
// + query_sequence_entry.go）——代码级时序图（源码 AST 转时序）。
// 纯语法解析不依赖类型信息；行号对齐索引调用边定位被调符号（嵌套
// 展开）。cli 只做参数解析与输出渲染（mermaid/文本树留 cli）。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"strings"
	"time"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// SeqTiming 代码级时序的阶段耗时与展开规模（todo10-9）。诊断用途——
// 只进日志 / 根节点 meta / stderr，不改变 stdout 的既有 JSON 键。
type SeqTiming struct {
	ResolveMS float64 `json:"resolve_ms"` // 符号解析（含接口方法具体化回退）
	ReadMS    float64 `json:"read_ms"`    // 源码读取 + 语法解析（含递归展开的被调函数）
	WalkMS    float64 `json:"walk_ms"`    // AST 遍历 / 调用点定位 / 接口与字段具体化
	RenderMS  float64 `json:"render_ms"`  // 渲染（cli 填；JSON 输出记 0——编码即输出，自指）
	TotalMS   float64 `json:"total_ms"`   // 端到端（含渲染）
	Nodes     int     `json:"nodes"`      // 展开规模（节点总数）
	Depth     int     `json:"depth"`      // 请求的嵌套层级
	Stop      string  `json:"stop"`       // 终止原因：none | depth | canceled
}

// 调用执行语义（todo10-2）：sync 缺省；defer 函数返回时执行；async 并发
// 启动（完成顺序未知，不得渲染成同步完成）。
const (
	SeqExecDefer = "defer"
	SeqExecAsync = "async"
)

// 终止原因（todo10-9；todo10-3 起扩展为按键诊断集）。
const (
	SeqStopNone     = "none"     // 自然结束（本函数语句走完）
	SeqStopDepth    = "depth"    // 深度上限：仍有可展开的调用点未展开
	SeqStopCanceled = "canceled" // 调用方取消
)

// ErrSeqCanceled 时序展开被取消（ctx 取消）——调用方不得当作成功。
var ErrSeqCanceled = errors.New("时序展开已取消")

// SeqMeta 根节点元信息（只有根 CodeSeqNode 携带）。
type SeqMeta struct {
	Version     int            `json:"seq_version"`
	Target      string         `json:"target,omitempty"`
	Timing      *SeqTiming     `json:"timing,omitempty"`
	Scene       *sceneSummary  `json:"scene,omitempty"`       // todo10-5：场景裁剪统计
	Focus       *SeqFocusMeta  `json:"focus,omitempty"`       // todo10-2 part3：焦点过滤是否生效
	Diagnostics []SeqDiagCount `json:"diagnostics,omitempty"` // todo10-3：展开终点聚合（按 code）
	// todo12-13：索引覆盖/降级（与展开诊断分开——只有 success 不算覆盖完整）
	Coverage *SeqCoverage `json:"coverage,omitempty"`
}

// SeqCoverage 构建期覆盖/降级信息（查询携带；与 SQL 解析统计分开）。
type SeqCoverage struct {
	BuildStatus  string         `json:"build_status,omitempty"`
	BuildError   string         `json:"build_error,omitempty"`
	IndexSHA     string         `json:"index_sha,omitempty"`
	DegradeStats map[string]any `json:"degrade_stats,omitempty"`
}

// SeqFocusMeta 焦点过滤结果（matched=0 时 applied=false——输出保留全量，
// 调用方据此提示"未命中"而不是把空图当结论）。
type SeqFocusMeta struct {
	Targets []string `json:"targets"`
	Matched int      `json:"matched"`
	Applied bool     `json:"applied"`
}

// seqTimingAcc 展开期间的累加器（req 按值传递——用指针共享）。
type seqTimingAcc struct {
	read       time.Duration // 读源码 + 语法解析（递归展开累加，互不重叠）
	depth      bool          // 有可展开而未展开的调用点（depth 截断）
	cancel     bool
	unresolved int // 未解析到项目内符号的调用点数（诊断用）
}

// millis Duration → 毫秒（保留小数——单次阶段可能远小于 1ms）。
func millis(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// coverageNum degrade_stats 数值键提取（JSON 反序列化为 float64）。
func coverageNum(stats map[string]any, key string) int64 {
	if stats == nil {
		return 0
	}
	switch v := stats[key].(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	}
	return 0
}

// seqCoverage 构建期覆盖信息（build_metadata → meta.coverage）。
func seqCoverage(m *domain.BuildMeta) *SeqCoverage {
	if m == nil {
		return nil
	}
	cov := &SeqCoverage{BuildStatus: string(m.Status), BuildError: m.ErrorMsg, IndexSHA: m.CommitSHA}
	if m.DegradeStats != "" {
		switch m.DegradeStats[0] {
		case '{', '[':
			if err := json.Unmarshal([]byte(m.DegradeStats), &cov.DegradeStats); err != nil {
				cov.DegradeStats = map[string]any{"raw": m.DegradeStats}
			}
		default:
			cov.DegradeStats = map[string]any{"raw": m.DegradeStats}
		}
	}
	return cov
}

// countSeqNodes 展开规模（节点总数——含分支/else/循环/嵌套）。
func countSeqNodes(n *CodeSeqNode) int {
	if n == nil {
		return 0
	}
	total := 1
	for _, c := range n.Nodes {
		total += countSeqNodes(c)
	}
	for _, c := range n.Else {
		total += countSeqNodes(c)
	}
	return total
}

// CodeSequenceRequest query sequence --code 参数。
type CodeSequenceRequest struct {
	Target       string    // 符号名或 canonical ID
	RepoAbs      string    // 仓库绝对路径（源码读取）
	Depth        int       // 嵌套层级（1 = 只本函数；>1 递归展开被调函数内部）
	StopPackages []string  // 停止包列表（cli 从 config.yaml 读取——命中不深入）
	Filter       SeqFilter // S5：导出过滤（按包/文件/函数/正则——命中不生成节点，如 log 调用）
	// Focus 关注目标（todo10-2 part3）：字段路径（pkg.T.F / T.F / F 后缀匹配）
	// 或 "return"（只看返回与返回表达式内调用）。空 = 不过滤。
	Focus []string
	// Scene 场景条件（todo10-5）：静态可判定的键值，用于分支裁剪与依据标注
	Scene Scene
	// Ctx 取消信号（todo10-9）：递归展开每语句检查一次；nil = 不取消
	Ctx context.Context
	// indexSHA 当前索引 commit（todo12-14：证据可复现性——内部填充）
	indexSHA string
	// entryID 入口 canonical ID（证据命令重放整棵分析树，而不是只查被调节点）
	entryID string
	// 内部：递归展开路径防环（R97——grpc 实现内部调接口、接口具体化
	// 回到同一 grpc 实现时自环；路径语义：进入标记、退出清除，不同
	// 分支互不影响）
	visited map[string]bool
	// 内部：阶段耗时累加（todo10-9）
	acc *seqTimingAcc
	// 内部（todo10-2）：当前函数的关注行（字段写入点）+ 具名结果槽 + 焦点配置
	focusLines  map[int]bool
	resultNames []string
	focus       *seqFocus
	// 内部（todo10-5/11-2）：条件传播——调用点实参字面量（按位置）与形参映射
	callArgLits []string
	callArgSrcs []string // todo12-3：实参值来源（定义位置/传播依据）
	// 内部（todo11-2）：当前函数 canonical ID / 文件（值流解析向上找调用点用）
	incomingArgs []string
	incomingSrc  []string // todo12-3：入站实参来源
	curBody      *ast.BlockStmt
	curParams    *ast.FieldList
	curFile      string
	sceneArgs    map[string]string
	sceneSrc     map[string]string // todo12-3：形参 → 值来源（分支判定证据）
	// 内部（todo10-5）：场景统计（meta 汇总；req 按值传递——用指针共享）
	sceneCount *sceneSummary
}

// seqFocus 一次查询的焦点配置（--focus 解析结果，跨递归共享）。
type seqFocus struct {
	validated bool            // todo12-9：目标存在性/歧义已校验
	returns   bool            // 关注返回值
	fields    []string        // 字段路径（后缀匹配）
	fieldIDs  []string        // todo12-9：解析后的类型限定字段身份（pkg.T.F）
	writers   map[string]bool // 写这些字段的函数（canonical ID——被调者命中即关注）
	matched   int             // 命中的节点数（0 = 未命中，meta 标记）
}

// focusActive 是否启用焦点过滤。
func (r CodeSequenceRequest) focusActive() bool {
	return r.focus != nil && (r.focus.returns || len(r.focus.fields) > 0)
}

// ctx 取消上下文（nil 安全）。
func (r CodeSequenceRequest) ctx() context.Context {
	if r.Ctx != nil {
		return r.Ctx
	}
	return context.Background()
}

// CodeSeqNode 代码级时序节点（树——分支/循环/嵌套调用）。
type CodeSeqNode struct {
	Kind     string         // call | branch | loop
	Label    string         // call: 调用名（obj.Method/fn）；branch: 条件；loop: 循环条件
	Actor    string         // R83：call 参与者（调用对象——s.manager/t.repo/ic；函数为函数名）
	Type     string         // R83：参与者短类型名（包最后路径段.类型名——索引实现类型）
	DeclType string         // P0-5：receiver 字段声明类型（接口——"声明接口"）
	ImplType string         // P0-5：数据流实现类型（R97-2 具体化命中时——声明接口 → 数据流实现双行）
	Args     []string       // R83：参数类型短名（消息线内容）
	Returns  []string       // R83：返回类型短名（return 线内容）
	Line     int            // 源码行号
	Nodes    []*CodeSeqNode // 子节点（branch: then 分支；loop: 循环体；call: 嵌套展开的被调函数调用）
	Else     []*CodeSeqNode // branch: else 分支（可选）
	Meta     *SeqMeta       `json:"meta,omitempty"` // 仅根节点：版本/目标/阶段耗时（todo10-9）
	// todo10-2：执行语义与返回语义
	Exec       string   `json:"exec,omitempty"`       // call: sync（缺省）|defer（退出时执行）|async（并发启动，完成顺序未知）
	InReturn   bool     `json:"in_return,omitempty"`  // call: 位于 return 表达式中（求值发生在返回前）
	Values     []string `json:"values,omitempty"`     // return: 返回表达式文本（按书写顺序）
	Slots      []string `json:"slots,omitempty"`      // return: 对应结果槽（具名结果名 / #序号）
	Terminates bool     `json:"terminates,omitempty"` // return: 终止当前路径（其后的语句不属于该路径）
	Focus      bool     `json:"focus,omitempty"`      // todo10-2：命中 --focus 目标（字段写入点/写者调用/返回）
	// todo10-3：展开终点诊断 + 节点证据（canonical ID / 文件 / 行 / 依据）
	Evidence *SeqEvidence `json:"evidence,omitempty"`
	Stop     *SeqStop     `json:"stop,omitempty"`
	// todo10-5：场景条件对分支的判定（taken/excluded/unknown + 依据）
	Scene *SceneFit `json:"scene,omitempty"`
}

// CodeSequence 解析目标函数的代码级时序（读源码 AST——纯语法不依赖
// 类型信息）。depth 为嵌套层级（1 = 只本函数；>1 递归展开被调函数
// 内部——行号对齐索引调用边定位被调符号）。符号解析失败返回 nil。
// R84：接口方法入口（grpc 服务入口接口——动态入口无方法体，如
// (OrderServiceServer).SubmitOrder）→ 具体化到实现方法再解析。
func (a *Actions) CodeSequence(req CodeSequenceRequest) (*CodeSeqNode, error) {
	logger := zap.L()
	logger.Info("enter (Actions).CodeSequence", zap.String("target", req.Target), zap.Int("depth", req.Depth))
	defer logger.Info("exit (Actions).CodeSequence")
	start := time.Now()
	if req.visited == nil {
		req.visited = map[string]bool{}
	}
	if req.acc == nil {
		req.acc = &seqTimingAcc{}
	}
	if req.sceneCount == nil {
		req.sceneCount = &sceneSummary{}
	}
	// todo10-2 part3：--focus 解析（字段路径 / return）——一次性构建
	if req.focus == nil && len(req.Focus) > 0 {
		req.focus = a.newSeqFocus(req.Focus)
	}
	ctx := req.ctx()
	var buildMeta *domain.BuildMeta
	if m, merr := a.repo.GetLatest(); merr == nil {
		buildMeta = m
	}
	if req.indexSHA == "" && buildMeta != nil {
		req.indexSHA = buildMeta.CommitSHA
	}

	// 阶段 1：符号解析（只查库，不读源码——与阶段 2/3 不重叠）
	n, err := a.ResolveSymbol(req.Target)
	if err != nil {
		return nil, err
	}
	// todo12-9：焦点目标校验（不存在/歧义 → 明确错误；合法无写入不报错）
	if req.focus != nil && len(req.Focus) > 0 && !req.focus.validated {
		if verr := a.validateFocusTargets(n, req.focus); verr != nil {
			return nil, verr
		}
		req.focus.validated = true
	}
	tResolved := time.Now()
	// todo12-14：证据命令重放入口分析（不是被调节点本身——focus/scene 属于入口）
	req.entryID = string(n.ID)
	// 阶段 2+3：读源码（acc.read 累加）+ AST 遍历展开
	root, stop := codeSeqForSymbol(a, req, string(n.ID), req.Depth)
	if root == nil && stop != nil && stop.Code == SeqStopCycle {
		stop = nil // 入口本身在栈上不可能；留给下面的 nil 诊断
	}
	if root == nil && strings.Contains(string(n.ID), ":(") {
		if impl, ok := a.InterfaceMethodImpl(string(n.ID)); ok {
			root, stop = codeSeqForSymbol(a, req, impl, req.Depth)
		}
	}
	tWalked := time.Now()
	// 取消优先：ctx 已取消或展开途中被取消 → 错误（不得报告成功）
	if ctx.Err() != nil || req.acc.cancel {
		logger.Warn("seq canceled", zap.String("target", req.Target), zap.Int("depth", req.Depth),
			zap.String("elapsed", time.Since(start).String()))
		return nil, ErrSeqCanceled
	}
	if root == nil {
		// todo10-3：不再返回"含义不明的 nil"——带 code 的诊断让调用方可操作
		if stop == nil {
			stop = &SeqStop{Code: SeqStopFuncNotFound}
		}
		logger.Warn("seq unavailable", zap.String("target", req.Target), zap.String("code", stop.Code),
			zap.String("detail", stop.Detail))
		return nil, &SeqDiagError{Code: stop.Code, Target: req.Target, Detail: stop.Detail}
	}
	// 终止原因（meta 级）：取消 > 深度截断 > 自然结束
	stopCode := SeqStopNone
	if req.acc.depth {
		stopCode = SeqStopDepthLimit
	}
	// 阶段口径（互不重叠，递归展开也不重复计）：
	//   resolve = 解析符号；read = 读源码+语法解析；walk = 遍历展开（含
	//   调用点定位/具体化/子函数递归）——walk 时间要扣掉其中的 read。
	timing := &SeqTiming{
		ResolveMS: millis(tResolved.Sub(start)),
		ReadMS:    millis(req.acc.read),
		WalkMS:    millis(tWalked.Sub(tResolved) - req.acc.read),
		TotalMS:   millis(tWalked.Sub(start)),
		Nodes:     countSeqNodes(root),
		Depth:     req.Depth,
		Stop:      stopCode,
	}
	// todo12-13：原分析诊断先固定下来（焦点/场景投影不得顶替 depth/unknown）
	analysisDiags := aggregateSeqStops(root)
	var projectionDiags []SeqDiagCount
	// todo10-2 part3：焦点过滤（无命中 → 保留全量并在 meta 标记，避免静默空图）
	if req.focusActive() {
		before := countSeqNodes(root)
		filtered := pruneToFocus(root.Nodes, req.focus)
		if req.focus.matched > 0 {
			root.Nodes = filtered
			removed := before - countSeqNodes(root)
			projectionDiags = append(projectionDiags, SeqDiagCount{Code: SeqDiagFocusProjection,
				Count: 1, Detail: fmt.Sprintf("焦点投影移除 %d 个节点；原分析诊断保留（筛选不改变分析完整性）", removed)})
		}
	}
	root.Meta = &SeqMeta{Version: 1, Target: req.Target, Timing: timing,
		Diagnostics: append(analysisDiags, projectionDiags...),
		Coverage:    seqCoverage(buildMeta)}
	// todo12-13：索引降级必须可见（build success 只代表构建流程完成）
	if cov := root.Meta.Coverage; cov != nil && cov.BuildStatus != "" && cov.BuildStatus != string(domain.BuildSuccess) {
		root.Meta.Diagnostics = append(root.Meta.Diagnostics, SeqDiagCount{
			Code: "index_degraded", Count: 1,
			Detail: "构建状态 " + cov.BuildStatus + "：" + cov.BuildError})
		// todo12-13 R3：包/类型检查失败与 SQL 降级分开列出
		if n := coverageNum(cov.DegradeStats, "typecheck_errors"); n > 0 {
			root.Meta.Diagnostics = append(root.Meta.Diagnostics, SeqDiagCount{
				Code: "typecheck_errors", Count: int(n),
				Detail: "构建期类型检查错误；相关包的分析可能回退（与 SQL 解析统计分开）"})
		}
		if n := coverageNum(cov.DegradeStats, "pkg_with_errors"); n > 0 {
			root.Meta.Diagnostics = append(root.Meta.Diagnostics, SeqDiagCount{
				Code: "packages_with_errors", Count: int(n),
				Detail: "存在加载/类型检查失败的包；索引覆盖不完整"})
		}
	}
	if req.focusActive() {
		root.Meta.Focus = &SeqFocusMeta{Targets: req.Focus, Matched: req.focus.matched, Applied: req.focus.matched > 0}
		timing.Nodes = countSeqNodes(root)
	}
	// todo10-5：场景裁剪统计（仅指定了 --scene 时输出）
	if !req.Scene.Empty() {
		sum := *req.sceneCount
		if sum.Applied {
			root.Meta.Diagnostics = append(root.Meta.Diagnostics, SeqDiagCount{Code: SeqDiagSceneProjection,
				Count: sum.Excluded, Detail: fmt.Sprintf("场景投影：taken=%d excluded=%d unknown=%d；排除分支内容被裁剪，原分析诊断保留",
					sum.Taken, sum.Excluded, sum.Unknown)})
		}
		sum.Applied = true
		root.Meta.Scene = &sum
		timing.Nodes = countSeqNodes(root)
	}
	logger.Info("seq timing", zap.String("target", req.Target), zap.Int("depth", req.Depth),
		zap.Int("nodes", timing.Nodes), zap.Float64("resolve_ms", timing.ResolveMS),
		zap.Float64("read_ms", timing.ReadMS), zap.Float64("walk_ms", timing.WalkMS),
		zap.String("stop", stopCode))
	return root, nil
}

// exprTypeName 类型表达式 → 短名（Ident → 名；SelectorExpr pkg.T → T）
// ——P0-5 receiver 字段声明类型提取。
func exprTypeName(t ast.Expr) string {
	if id, ok := t.(*ast.Ident); ok {
		return id.Name
	}
	if sel, ok := t.(*ast.SelectorExpr); ok {
		return sel.Sel.Name
	}
	return ""
}

// SeqFilter 时序图导出过滤（S5）：按包/文件/函数/正则——命中不生成
// 节点（如 log 调用）。cli 从 config.yaml 的 seq.filter_* 读取。
type SeqFilter struct {
	Pkgs  []string // 包路径（完整或短名/末段匹配）
	Files []string // 源码文件（仓库相对路径，末段匹配）
	Fns   []string // 调用名（label 精确或末段 .Method 匹配）
	Regex []string // label 正则
}

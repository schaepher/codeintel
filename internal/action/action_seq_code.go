package action

// R95 迁移：`query sequence --code` 查询逻辑（原 cli/query_sequence_code.go
// + query_sequence_entry.go）——代码级时序图（源码 AST 转时序）。
// 纯语法解析不依赖类型信息；行号对齐索引调用边定位被调符号（嵌套
// 展开）。cli 只做参数解析与输出渲染（mermaid/文本树留 cli）。

import (
	"context"
	"errors"
	"go/ast"
	"strings"
	"time"

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
	Version int        `json:"seq_version"`
	Target  string     `json:"target,omitempty"`
	Timing  *SeqTiming `json:"timing,omitempty"`
}

// seqTimingAcc 展开期间的累加器（req 按值传递——用指针共享）。
type seqTimingAcc struct {
	read   time.Duration // 读源码 + 语法解析（递归展开累加，互不重叠）
	depth  bool          // 有可展开而未展开的调用点（depth 截断）
	cancel bool
}

// millis Duration → 毫秒（保留小数——单次阶段可能远小于 1ms）。
func millis(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

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
	// Ctx 取消信号（todo10-9）：递归展开每语句检查一次；nil = 不取消
	Ctx context.Context
	// 内部：递归展开路径防环（R97——grpc 实现内部调接口、接口具体化
	// 回到同一 grpc 实现时自环；路径语义：进入标记、退出清除，不同
	// 分支互不影响）
	visited map[string]bool
	// 内部：阶段耗时累加（todo10-9）
	acc *seqTimingAcc
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
	ctx := req.ctx()

	// 阶段 1：符号解析（只查库，不读源码——与阶段 2/3 不重叠）
	n, err := a.ResolveSymbol(req.Target)
	if err != nil {
		return nil, err
	}
	tResolved := time.Now()
	// 阶段 2+3：读源码（acc.read 累加）+ AST 遍历展开
	root := codeSeqForSymbol(a, req, string(n.ID), req.Depth)
	if root == nil && strings.Contains(string(n.ID), ":(") {
		if impl, ok := a.InterfaceMethodImpl(string(n.ID)); ok {
			root = codeSeqForSymbol(a, req, impl, req.Depth)
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
		return nil, nil
	}
	// 终止原因：取消 > 深度截断 > 自然结束
	stop := SeqStopNone
	if req.acc.depth {
		stop = SeqStopDepth
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
		Stop:      stop,
	}
	root.Meta = &SeqMeta{Version: 1, Target: req.Target, Timing: timing}
	logger.Info("seq timing", zap.String("target", req.Target), zap.Int("depth", req.Depth),
		zap.Int("nodes", timing.Nodes), zap.Float64("resolve_ms", timing.ResolveMS),
		zap.Float64("read_ms", timing.ReadMS), zap.Float64("walk_ms", timing.WalkMS),
		zap.String("stop", stop))
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

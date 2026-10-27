package action

// R95 迁移：`query sequence --code` 查询逻辑（原 cli/query_sequence_code.go
// + query_sequence_entry.go）——代码级时序图（源码 AST 转时序）。
// 纯语法解析不依赖类型信息；行号对齐索引调用边定位被调符号（嵌套
// 展开）。cli 只做参数解析与输出渲染（mermaid/文本树留 cli）。

import (
	"fmt"
	"strings"
	"time"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// SeqTiming 代码级时序的阶段耗时与展开规模（todo10-9）。诊断用途——
// 只进日志 / 根节点 meta / stderr，不改变 stdout 的既有 JSON 键。

// 调用执行语义（todo10-2）：sync 缺省；defer 函数返回时执行；async 并发
// 启动（完成顺序未知，不得渲染成同步完成）。

// 终止原因（todo10-9；todo10-3 起扩展为按键诊断集）。

// ErrSeqCanceled 时序展开被取消（ctx 取消）——调用方不得当作成功。

// SeqMeta 根节点元信息（只有根 CodeSeqNode 携带）。

// SeqCoverage 构建期覆盖/降级信息（查询携带；与 SQL 解析统计分开）。

// SeqFocusMeta 焦点过滤结果（matched=0 时 applied=false——输出保留全量，
// 调用方据此提示"未命中"而不是把空图当结论）。

// seqTimingAcc 展开期间的累加器（req 按值传递——用指针共享）。

// millis Duration → 毫秒（保留小数——单次阶段可能远小于 1ms）。

// coverageNum degrade_stats 数值键提取（JSON 反序列化为 float64）。

// seqCoverage 构建期覆盖信息（build_metadata → meta.coverage）。

// countSeqNodes 展开规模（节点总数——含分支/else/循环/嵌套）。

// CodeSequenceRequest query sequence --code 参数。

// seqFocus 一次查询的焦点配置（--focus 解析结果，跨递归共享）。

// focusActive 是否启用焦点过滤。

// ctx 取消上下文（nil 安全）。

// CodeSeqNode 代码级时序节点（树——分支/循环/嵌套调用）。

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
	// todo21-14.1/14.2：入口取消检查必须在接触仓储**之前**——已取消的
	// 查询不得打开库/扫表（预算 1ns 这类请求立即失败）。
	if ctx.Err() != nil {
		req.acc.cancel = true
		return nil, ErrSeqCanceled
	}
	var buildMeta *domain.BuildMeta
	sqlStart := time.Now()
	if m, merr := a.repo.GetLatest(); merr == nil {
		buildMeta = m
	}
	req.acc.sqlQueries++
	req.acc.sqlDur += time.Since(sqlStart)
	if buildMeta != nil {
		if req.indexSHA == "" {
			req.indexSHA = buildMeta.CommitSHA
		}
		req.buildID = buildMeta.BuildID
	}

	if req.pkgSet == nil {
		pkgSet, perr := a.indexedPkgSet(ctx)
		if perr != nil {
			// 取消即失败（不把空包集合当完整成功继续输出）；查询/库错误
			// 仍按原行为退化为空集（未知包 → 调用点分类保守标 index_gap）
			if ctx.Err() != nil {
				req.acc.cancel = true
				return nil, ErrSeqCanceled
			}
			logger.Warn("indexed package set unavailable", zap.Error(perr))
			req.pkgSet = map[string]bool{}
		} else {
			// 包清单返回后再次检查取消（扫表期间预算可能已到）
			if ctx.Err() != nil {
				req.acc.cancel = true
				return nil, ErrSeqCanceled
			}
			req.pkgSet = pkgSet
		}
	}
	if req.fileCache == nil {
		req.fileCache = map[string]*seqParsedFile{}
	}
	if req.litVisited == nil {
		req.litVisited = map[string]bool{} // todo22 §2：函数字面量展开防环
	}
	if req.tgtsCache == nil {
		req.tgtsCache = map[string]*seqTargets{} // todo22 §2：按函数缓存调用点映射
	}
	if req.ifaceImpls == nil {
		req.ifaceImpls = map[string]seqIfaceCached{} // todo22 §7.3：接口候选请求内缓存
	}
	if req.extPkgs == nil {
		req.extPkgs = map[string]*seqExternalPkg{} // todo22 §4.2：外部包源码缓存
	}
	if req.rpcFwd == nil {
		req.rpcFwd = map[string]*domain.RPCForward{} // todo22 §4.1：RPC 转发缓存
	}
	if req.rpcSvcs == nil {
		req.rpcSvcs = map[string]*seqRPCSvc{} // todo22 §4.1：RPC 服务端解析缓存
	}
	if req.rpcByFunc == nil {
		req.rpcByFunc = map[string][]*domain.RPCForward{} // todo22 §4.1：调用点 RPC 出站边
	}
	if req.rpcVisited == nil {
		req.rpcVisited = map[string]bool{} // todo22 §4.1：RPC 续接防环
	}
	// 阶段 1：符号解析（只查库，不读源码——与阶段 2/3 不重叠）
	symStart := time.Now()
	n, err := a.ResolveSymbol(req.Target)
	req.acc.symLookups++
	req.acc.sqlQueries++ // ResolveSymbol 实际走到仓储查询
	req.acc.symDur += time.Since(symStart)
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
		ResolveMS:    millis(tResolved.Sub(start)),
		ReadMS:       millis(req.acc.read),
		WalkMS:       millis(tWalked.Sub(tResolved) - req.acc.read),
		TotalMS:      millis(tWalked.Sub(start)),
		Nodes:        countSeqNodes(root),
		Depth:        req.Depth,
		Stop:         stopCode,
		SymLookups:   req.acc.symLookups,
		SymMS:        millis(req.acc.symDur),
		Signature:    req.acc.sigLookups,
		SignatureMS:  millis(req.acc.sigDur),
		Dispatch:     req.acc.dispatch,
		DispatchMS:   millis(req.acc.dispatchDur),
		SQLQueries:   req.acc.sqlQueries,
		SQLMS:        millis(req.acc.sqlDur),
		CacheHits:    req.acc.cacheHits,
		CacheMisses:  req.acc.cacheMisses,
		StatsScope:   "explicit_repo_calls: get_latest/resolve_symbol/get_symbol/callees/interface_impl/implements_edges",
		UncoveredOps: "SSA adapter 内部 SQL 与预计算（计入 walk_ms）；源码读取（read_ms）",
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
	if req.Evidence {
		root.Meta.Evidence = seqEvidenceIdentity(req)
	}
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
		// todo13-13：具体失败包/错误位置 + 目标相关覆盖（计数之外）
		root.Meta.Diagnostics = append(root.Meta.Diagnostics,
			seqCoverageDiagnostics(cov, pkgOfID(n.ID))...)
	}
	if req.focusActive() {
		root.Meta.Focus = &SeqFocusMeta{Targets: req.Focus, Matched: req.focus.matched,
			Applied: req.focus.matched > 0, Partial: req.acc.focusUnresolved > 0}
		timing.Nodes = countSeqNodes(root)
		if req.acc.focusUnresolved > 0 {
			root.Meta.Diagnostics = append(root.Meta.Diagnostics, SeqDiagCount{
				Code: "focus_dependency_unresolved", Count: req.acc.focusUnresolved,
				Detail: "焦点依赖里有类型无法判定的写入（已保守保留，未证明与目标同类型）"})
		}
	}
	// todo10-5：场景裁剪统计（指定了 --scene，或本次判定因未知改写而不完整时输出）
	if !req.Scene.Empty() || len(req.sceneCount.Causes) > 0 {
		sum := *req.sceneCount
		if sum.Applied {
			root.Meta.Diagnostics = append(root.Meta.Diagnostics, SeqDiagCount{Code: SeqDiagSceneProjection,
				Count: sum.Excluded, Detail: fmt.Sprintf("场景投影：taken=%d excluded=%d unknown=%d；排除分支内容被裁剪，原分析诊断保留",
					sum.Taken, sum.Excluded, sum.Unknown)})
		}
		if sum.Partial {
			root.Meta.Diagnostics = append(root.Meta.Diagnostics, SeqDiagCount{Code: "scene_partial",
				Count:  len(sum.Causes),
				Detail: "场景判定不完整（保留两个分支）：" + strings.Join(sum.Causes, "；")})
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

// SeqFilter 时序图导出过滤（S5）：按包/文件/函数/正则——命中不生成
// 节点（如 log 调用）。cli 从 config.yaml 的 seq.filter_* 读取。

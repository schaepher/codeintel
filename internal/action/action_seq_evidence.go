package action

// todo10-3：展开终点的结构化诊断 + 节点证据。
//
// 设计约束（用户确认的 Q2/Q4）：
//   - 只加字段不改结构（MCP/插件消费既有键）
//   - 区分"分析停止"（depth/stop_pkg/cycle/unresolved/index_gap/
//     source_missing/func_not_found/parse_error）与"业务流程结束"（no_calls）
//   - 节点证据：canonical ID、仓库内文件、行号、解析依据、置信度；
//     接口多候选**不得静默选择一个实现**——候选集合 + 依据 + ambiguous
//   - 业务逻辑/结构化结果在 action，渲染在 cli

import (
	"sort"
	"strings"
)

// 展开终点代码（诊断口径）。
const (
	SeqStopNoCalls      = "no_calls"       // 本函数无可展开的项目内调用（业务流程结束）
	SeqStopDepthLimit   = "depth"          // 深度上限（分析停止）
	SeqStopPkg          = "stop_pkg"       // 配置停止包（分析停止）
	SeqStopCycle        = "cycle"          // 路径已在展开栈上（防环）
	SeqStopUnresolved   = "unresolved"     // 调用点未解析到项目内符号（含外部库）
	SeqStopIndexGap     = "index_gap"      // 索引里没有该符号（构建覆盖缺失）
	SeqStopSourceMiss   = "source_missing" // 索引有文件但源码读不到
	SeqStopFuncNotFound = "func_not_found" // 源码里按行号定位不到函数声明
	SeqStopParseError   = "parse_error"    // 源码语法解析失败
	SeqStopFiltered     = "filtered"       // 被时序过滤规则排除
	// todo22 §2：函数值（回调/字面量）在调用点无法确定 → 保留 unknown 节点
	SeqStopFuncValueUnbound = "func_value_unbound"
	// todo13-13：消费者可区分的调用分类（不再把所有未解析笼统称 index_gap）
	SeqStopExternalTarget = "external_target"        // 外部库目标（调用表达式已知，库内实现未展开）
	SeqStopUnknownImpl    = "unknown_implementation" // 接口方法无可证明实现
	// todo22 §3.2：有多个未验证候选——保留候选集合，不选第一个展开
	SeqStopIfaceAmbiguous = "iface_ambiguous"
	// todo22 §4.1：RPC 续接的停止码见 action_seq_rpc_evidence.go（§4.1 专用）
	SeqDegradedPackage = "degraded_package" // 构建期失败的具体包（含错误位置）
	SeqTargetCoverage  = "target_coverage"  // 目标与失败包的隔离判断
	SeqSQLDegraded     = "sql_degraded"     // SQL 解析降级统计（与包失败分开）
	// todo12-13：筛选/投影诊断（与原分析诊断分开——筛选不得顶替原诊断）
	SeqDiagFocusProjection = "focus_projection"
	SeqDiagSceneProjection = "scene_projection"
)

// 解析依据（Evidence.Source）。
const (
	SeqEvidenceCallEdge  = "call_edge"      // 索引 calls 边（line_num/pos 对齐）
	SeqEvidenceAST       = "ast"            // 源码 AST（节点定位）
	SeqEvidenceIface     = "iface"          // 接口方法/类型 → 实现（implements 边 + 方法名）
	SeqEvidenceFieldFlow = "field_dataflow" // receiver 字段赋值来源（SSA 摘要 + 源码）
	SeqEvidenceLocalVar  = "localvar"       // 局部变量初始化来源（构造器/字面量）
	SeqEvidenceGrpcImpl  = "grpc_impl"      // grpc 服务实现方法
	// todo22 §2：函数值调用（IIFE / 闭包变量 / 回调形参绑定）——按调用点
	// 数据流证明执行，证据记录绑定/调用位置与依据。
	SeqEvidenceClosure = "closure"
	// todo22 §4.1：RPC 客户端桩 → 服务端实现（go-micro NewRequest/Call + 注册）
	SeqEvidenceRPC = "rpc"
)

// SeqReasonAggregatedCallEdge 目标由同函数汇总边的调用名唯一匹配恢复
// （todo20-9：重复调用点的 pos 未单独入库）。
const SeqReasonAggregatedCallEdge = "index-call-edge-aggregated"

// SeqStop 展开终点诊断（挂在"在那里停下"的调用节点上）。
type SeqStop struct {
	Code   string `json:"code"`
	Detail string `json:"detail,omitempty"`
}

// SeqEvidence 节点证据：能据此定位源码与判断依据。
type SeqEvidence struct {
	Source     string   `json:"source"`               // 见 SeqEvidence* 常量
	CallFile   string   `json:"call_file,omitempty"`  // 调用点所在文件（仓库相对）
	CallLine   int      `json:"call_line,omitempty"`  // 调用点行号
	SymbolID   string   `json:"symbol_id,omitempty"`  // 被调/展开符号 canonical ID
	File       string   `json:"file,omitempty"`       // 符号定义文件（仓库相对）
	Line       int      `json:"line,omitempty"`       // 符号定义行
	Confidence float64  `json:"confidence,omitempty"` // 复用边置信度（0 = 无）
	Candidates []string `json:"candidates,omitempty"` // 多候选（接口派发/实现）
	Reason     string   `json:"reason,omitempty"`     // unique|first-of-N|dataflow|...
	Ambiguous  bool     `json:"ambiguous,omitempty"`  // 多候选未消歧（不是确定结论）
	// todo22 §3.2：候选展开预算——candidate_depth = 已展开的候选层数（0 =
	// 未展开任何候选）；candidate_budget = 截断/未展开的原因（含候选数）
	CandidateDepth  int    `json:"candidate_depth,omitempty"`
	CandidateBudget string `json:"candidate_budget,omitempty"`
	// todo22 §3.3：DI 注册证据站点（字段注入的 key 在哪个引导函数里注册）
	RegisterSite int    `json:"di_register_line,omitempty"`
	RegisterFile string `json:"di_register_file,omitempty"`
	// todo22 §4.1：RPC 续接证据——服务节点/完整服务身份/协议/方法（远端仓库
	// 续接时 RemoteRepo 记录目标仓库，防止"看起来连上但不是同一个服务"）。
	ServiceID   string `json:"service_id,omitempty"`
	RPCService  string `json:"rpc_service,omitempty"`
	RPCProtocol string `json:"rpc_protocol,omitempty"`
	RPCMethod   string `json:"rpc_method,omitempty"`
	RemoteRepo  string `json:"remote_repo,omitempty"`
	// todo10-8：证据身份——可直接重跑的查询命令 + 支持程度
	Query   string `json:"query,omitempty"`   // 规范化 shell 命令（含 repo/ID/depth/focus/scene）
	Verdict string `json:"verdict,omitempty"` // direct|corroborated|unverified
	// todo12-14：规范化请求快照（结构化 argv 为权威；索引版本变化时可判定
	// 是否仍能复现同一次分析）
	Request *SeqEvidenceRequest `json:"request,omitempty"`
}

// SeqDiagCount 诊断聚合项（根节点 meta 用）。
type SeqDiagCount struct {
	Code   string `json:"code"`
	Count  int    `json:"count"`
	Detail string `json:"detail,omitempty"` // 可操作细节（预算/失败原因/截断范围）
}

// SeqDiagError 查询级失败（无法产出树）——调用方据此给出可操作提示，
// 而不是笼统的"不可用"。
type SeqDiagError struct {
	Code   string // 见 SeqStop* 常量
	Target string
	Detail string
}

func (e *SeqDiagError) Error() string {
	msg := "代码级时序不可用"
	switch e.Code {
	case SeqStopIndexGap:
		msg = "符号不在索引中（可能是新代码未重建索引）——先运行 codeintel update/reindex"
	case SeqStopSourceMiss:
		msg = "符号已索引但源码文件读不到（文件被删除/移动）——先运行 codeintel update"
	case SeqStopFuncNotFound:
		msg = "源码中按索引行号定位不到函数声明（源码与索引不一致）——先运行 codeintel update"
	case SeqStopParseError:
		msg = "源码语法解析失败（文件损坏或未提交的语法错误）"
	}
	if e.Detail != "" {
		msg += "：" + e.Detail
	}
	if e.Target != "" {
		msg += "（" + e.Target + "）"
	}
	return msg
}

// aggregateSeqStops 汇总树上的终点诊断（按 code 计数，排序稳定）。
func aggregateSeqStops(root *CodeSeqNode) []SeqDiagCount {
	counts := map[string]int{}
	var walk func(n *CodeSeqNode)
	walk = func(n *CodeSeqNode) {
		if n == nil {
			return
		}
		if n.Stop != nil && n.Stop.Code != "" {
			counts[n.Stop.Code]++
		}
		for _, c := range n.Nodes {
			walk(c)
		}
		for _, c := range n.Else {
			walk(c)
		}
	}
	for _, c := range root.Nodes {
		walk(c)
	}
	if counts[SeqStopNoCalls] == 0 && len(counts) == 0 {
		// 叶子函数没有任何诊断 → 业务流程结束（根函数无内部调用）
		counts[SeqStopNoCalls] = 1
	}
	out := make([]SeqDiagCount, 0, len(counts))
	for code, n := range counts {
		out = append(out, SeqDiagCount{Code: code, Count: n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

// seqIfaceMethodImpl 接口方法（或接口类型）→ 实现候选 + 选中项（todo10-3）。
// 候选来自 implements 边（接口 → 实现者），方法形态只保留有同名方法的实现；
// 选中项沿用 InterfaceMethodImpl 的顺序（grpc 实现排后）。多候选时调用方
// 必须标记 ambiguous——不得把"第一个"当确定结论。
func (a *Actions) seqIfaceMethodImpl(req CodeSequenceRequest, methodID string) (chosen string, candidates []string, ok bool) {
	ifaceID, methodName := seqIfaceAndMethod(methodID)
	if ifaceID == "" {
		return "", nil, false
	}
	// todo22 §7.3：请求内缓存（同一请求里同一接口的多处派发不重复查询；
	// 命中/未命中都缓存——返回的切片只读共享，调用方不得修改）。
	// todo23 §7.1：缓存键必须含**方法身份**——chosen/candidates 是方法级 ID
	// （`(Store).Insert` vs `(Store).SaveService`），只按接口缓存会把第一个
	// 方法的结果复用给同接口的其它方法。
	// todo25 §7.3：缓存键含**仓库身份**——外层 workspace 请求可能复用同一
	// 请求缓存解析多个仓库的同名接口（canonical ID 相同），只按接口/方法
	// 缓存会把另一个仓库的实现候选串过来。
	repoKey := seqIfaceRepoKey(req.RepoAbs, ifaceID)
	cacheKey := repoKey + "\x00" + methodName
	if req.ifaceImpls != nil {
		if c, hit := req.ifaceImpls[cacheKey]; hit {
			return c.chosen, c.cands, c.ok
		}
	}
	chosen, ok = a.InterfaceMethodImpl(methodID)
	if !ok {
		if req.ifaceImpls != nil {
			req.ifaceImpls[cacheKey] = seqIfaceCached{ok: false}
		}
		return "", nil, false
	}
	// todo22 §7.1：按接口精确查询（整数邻接索引）——不加载全量 implements 边。
	// todo23 §7.1：只缓存实现**类型**（与所选方法无关），候选 ID 按当前
	// 方法重建。
	impls, cached := req.ifaceImplTypes[repoKey]
	if !cached {
		var err error
		impls, err = a.repo.InterfaceImplsExact(ifaceID)
		if err != nil {
			return chosen, nil, true
		}
		if req.ifaceImplTypes != nil {
			req.ifaceImplTypes[repoKey] = impls
		}
	}
	for _, impl := range impls {
		if methodName == "" {
			candidates = append(candidates, impl)
			continue
		}
		// 方法形态：实现类型上的同名方法 ID
		mi := strings.LastIndex(impl, ":")
		if mi < 0 {
			continue
		}
		candidates = append(candidates, impl[:mi+1]+"("+impl[mi+1:]+")."+methodName)
	}
	if req.ifaceImpls != nil {
		req.ifaceImpls[cacheKey] = seqIfaceCached{chosen: chosen, cands: candidates, ok: true}
	}
	return chosen, candidates, true
}

// seqIfaceAndMethod 从接口方法 ID 拆出接口 ID 与方法名；接口类型 ID 直接
// 返回（方法名为空）。形态：symbol:go:<pkg>:(Iface).Method。
func seqIfaceAndMethod(id string) (ifaceID, method string) {
	i := strings.Index(id, ":(")
	if i < 0 {
		return id, "" // 接口类型节点
	}
	pkg := id[:i]
	rest := id[i+2:]
	j := strings.Index(rest, ").")
	if j < 0 {
		return "", ""
	}
	return pkg + ":" + rest[:j], rest[j+2:]
}

// 证据支持程度（todo10-8）：direct = 工具输出（索引边/元数据）直接支持；
// corroborated = 源码/其他分析补证（值流具体化、未解析调用点靠 AST）；
// unverified = 存在歧义或证据缺失（不得当作确定结论）。
const (
	SeqVerdictDirect       = "direct"
	SeqVerdictCorroborated = "corroborated"
	SeqVerdictUnverified   = "unverified"
)

// seqEvidenceVerdict 按证据来源与歧义给出支持程度。
func seqEvidenceVerdict(ev *SeqEvidence) string {
	if ev == nil {
		return SeqVerdictUnverified
	}
	if ev.Ambiguous {
		return SeqVerdictUnverified
	}
	switch ev.Source {
	case SeqEvidenceCallEdge:
		if ev.Reason == SeqReasonAggregatedCallEdge {
			// 位置→目标的对应关系由汇总边推断（不是该位置的直接 metadata）
			return SeqVerdictCorroborated
		}
		if ev.Confidence >= MinConfidence {
			return SeqVerdictDirect
		}
		return SeqVerdictCorroborated
	case SeqEvidenceAST:
		// 只有调用表达式存在、没有目标身份 → 不是已被证明的调用
		// （todo20-9：不能把语法节点存在误读为目标已解析）。
		if ev.SymbolID == "" {
			return SeqVerdictUnverified
		}
		return SeqVerdictCorroborated
	case SeqEvidenceFieldFlow, SeqEvidenceLocalVar, SeqEvidenceGrpcImpl, SeqEvidenceIface:
		return SeqVerdictCorroborated
	case SeqEvidenceClosure:
		// 语法级数据流（绑定证明）：有明确绑定 → corroborated；
		// 未绑定/多值未消歧（Ambiguous）→ 已在前面判为 unverified。
		return SeqVerdictCorroborated
	case SeqEvidenceRPC:
		return seqRPCEvidenceVerdict(ev) // todo22 §4.1（见 action_seq_rpc_evidence.go）
	}
	return SeqVerdictUnverified
}

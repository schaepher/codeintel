package action

import "go/ast"

// todo24 §2：`query sequence --code --continue-segments N`——在原调用上下文
// 里**分段续查 depth 前沿**，把结果接回同一棵分析树（JSON/文本/Mermaid/SVG
// 共用），而不是把前沿当独立根重跑前缀、也不是简单提高入口 depth。
//
// 语义：
//   - 初始展开仍按 `--depth` 进行；每个停在 depth 的调用节点登记一个前沿
//     （携带当时的请求快照：场景/实参/回调绑定/receiver/防环祖先路径）。
//   - T12b：预算单位是**一次前沿展开**（不是一轮/一段）；每个前沿先占 1 次
//     预算，新产生的 depth 前沿留给下一批；预算耗尽保留 depth 并记 budget_stops。
//   - 续查在当前调用上下文退出前同步完成（前沿保存请求值快照 + 祖先集合
//     副本），不延后使用可能被后续赋值改写的共享环境。
//   - 同一前沿被回调与普通调用两层登记时只算一次（按节点指针去重）。
//   - 未知实现/候选未消歧/外部源码未证明的前沿不自动续查（保留原诊断）。

// seqFrontier 一个可续查的 depth 前沿（请求快照 + 恢复函数）。
type seqFrontier struct {
	node *CodeSeqNode
	// base 登记时的**请求值快照**（env/形参实参绑定/receiver/文件上下文/
	// 场景条件）——续查必须用它，不能延后用可能被后续赋值改写的共享环境。
	base     CodeSequenceRequest
	resume   func(sub CodeSequenceRequest)
	ancestor map[string]bool // 登记时的防环祖先（续查时作为栈底，跨段也能截环）
	callFile string
	callLine int
	target   string
	repo     string
	reason   string // 原 depth 诊断（用户的 depth 原因原样保留）
	segment  int    // 登记所属段（0 = 初始展开）
	// atStmt 登记所在语句序号（T12b：语句结束时只处理本语句的前沿，
	// 保证"先绑定调用结果、再处理后续语句"且不改变 BFS 预算顺序）。
	atStmt int
}

// copyVisited 防环路径集合副本（每个前沿独立一份，互不影响）。
func copyVisited(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	return out
}

// seqCanContinueFrontier 该节点是否可作为续查前沿：停在 depth、目标身份
// 明确（无歧义/候选未消歧）、不是外部未证明源码。
func seqCanContinueFrontier(node *CodeSeqNode) bool {
	if node == nil || node.Stop == nil || node.Stop.Code != SeqStopDepthLimit {
		return false
	}
	// U06b：候选分支（含预算截断）不是确定执行——两个续查入口都不登记前沿。
	if node.Candidate {
		return false
	}
	ev := node.Evidence
	if ev == nil || ev.SymbolID == "" {
		return false
	}
	if ev.Ambiguous || len(ev.Candidates) > 1 {
		return false
	}
	switch ev.Source {
	case SeqEvidenceExternal, SeqEvidenceRPC:
		// 外部源码未证明不续查（RPC 前沿由 RPC 分支自己登记，需要 fwd）；
		// RPC 在这里被排除，避免重复登记。
		return false
	}
	return true
}

// seqRegisterFrontier 登记一个 depth 前沿（幂等：同一节点只登记一次）。
func seqRegisterFrontier(req CodeSequenceRequest, node *CodeSeqNode, target string,
	resume func(sub CodeSequenceRequest), reason string) {
	acc := req.acc
	if req.ContinueSegments <= 0 || acc == nil || node == nil || resume == nil {
		return
	}
	// T07：未验证实现候选的后代禁止自动续查（候选不是确定执行）。
	if req.inCandidate || node.Candidate {
		return
	}
	if acc.frontierSeen == nil {
		acc.frontierSeen = map[*CodeSeqNode]bool{}
	}
	if acc.frontierSeen[node] {
		return
	}
	acc.frontierSeen[node] = true
	repo := req.RepoAbs
	callFile, callLine := "", 0
	if ev := node.Evidence; ev != nil {
		callFile, callLine = ev.CallFile, ev.CallLine
		if ev.RemoteRepo != "" {
			repo = ev.RemoteRepo
		}
	}
	acc.frontiers = append(acc.frontiers, &seqFrontier{
		node: node, base: req, resume: resume, ancestor: copyVisited(req.visited),
		callFile: callFile, callLine: callLine, target: target, repo: repo,
		reason: reason, segment: acc.continueSegment, atStmt: acc.stmtSeq,
	})
}

// seqRegisterSymbolFrontier 目标为已证明符号的通用前沿：续查 = 以原请求
// depth 展开该符号的函数体，接到同一节点（不新建根、不重放兄弟候选）。
func seqRegisterSymbolFrontier(a *Actions, req CodeSequenceRequest, node *CodeSeqNode) {
	if !seqCanContinueFrontier(node) {
		return
	}
	target := node.Evidence.SymbolID
	seqRegisterFrontier(req, node, target, func(sub CodeSequenceRequest) {
		child, stop := codeSeqForSymbol(a, sub, target, sub.Depth)
		if child != nil {
			node.Nodes, node.Stop = child.Nodes, child.Stop
			return
		}
		if stop != nil {
			node.Stop = stop
		}
	}, node.Stop.Detail)
}

// seqContinueBatch 展开一批前沿（stmt=-1 表示全部；否则只处理该语句登记的
// 前沿）。每展开一个前沿先占用 1 次总预算（T12b）；本批展开产生的新前沿
// 留到下一批/最终收尾（保持 BFS 顺序与预算口径）。
func seqContinueBatch(a *Actions, req CodeSequenceRequest, stmt int) bool {
	acc := req.acc
	if acc == nil {
		return false
	}
	var batch, rest []*seqFrontier
	if stmt < 0 {
		batch, acc.frontiers = acc.frontiers, nil
	} else {
		for _, f := range acc.frontiers {
			if f.atStmt == stmt {
				batch = append(batch, f)
			} else {
				rest = append(rest, f)
			}
		}
		acc.frontiers = rest
	}
	if len(batch) == 0 {
		return false
	}
	progress := false
	for _, f := range batch {
		// 已被上一层展开（同一节点重复登记）：不消耗预算。
		if f.node.Stop == nil || f.node.Stop.Code != SeqStopDepthLimit {
			continue
		}
		if acc.continueSpent >= req.ContinueSegments {
			// 预算已耗尽：前沿放回队列，最终收尾统一记 budget_stops。
			acc.frontiers = append(acc.frontiers, f)
			continue
		}
		if req.ctx().Err() != nil {
			acc.cancel = true
			return progress
		}
		// 先占用一次预算，再校验身份/展开（失败不虚报追加次数）。
		acc.continueSpent++
		seg := acc.continueExpansions + 1
		acc.continueSegment = seg
		cur := ""
		if f.node.Evidence != nil {
			cur = f.node.Evidence.SymbolID
		}
		if cur != "" && f.target != "" && cur != f.target {
			acc.continuation = append(acc.continuation, SeqContinuationFrontier{
				Segment: seg, CallFile: f.callFile, CallLine: f.callLine,
				TargetID: f.target, Repo: f.repo, DepthReason: f.reason, Continued: false,
				IdentityChanged: true, IdentityReason: "identity_changed: " + f.target + " → " + cur,
			})
			continue
		}
		f.node.Nodes = nil
		f.node.Stop = nil
		sub := f.base
		// 只恢复**祖先**路径；前沿目标自身的 visit key 由 resume 里的
		// codeSeqForSymbol 标记（预先标记会把正常展开误判成 cycle）。
		sub.visited = copyVisited(f.ancestor)
		sub.acc = acc
		acc.inContinuation = true
		f.resume(sub)
		acc.inContinuation = false
		// 包装函数 `return F()` 的槽来源可能在本次展开后才齐备——立即后序
		// 重算该前沿子树，供后续语句/调用方绑定使用。
		seqRefreshReturnOrigins(req, f.node)
		if f.node.Exec != "" {
			seqPropagateExec(f.node, f.node.Exec)
		}
		acc.continuation = append(acc.continuation, SeqContinuationFrontier{
			Segment: seg, CallFile: f.callFile, CallLine: f.callLine,
			TargetID: f.target, Repo: f.repo, DepthReason: f.reason, Continued: true,
		})
		acc.continueExpansions++
		acc.continueUsed = acc.continueExpansions
		progress = true
	}
	return progress
}

// seqContinueSegments 收尾：按 FIFO 依次展开剩余前沿直到总追加次数预算耗尽
// （同步，返回前完成）；预算耗尽的前沿保留 depth 并记 budget_stops。
func seqContinueSegments(a *Actions, req CodeSequenceRequest, root *CodeSeqNode) {
	acc := req.acc
	if req.ContinueSegments <= 0 || acc == nil || root == nil {
		return
	}
	for acc.continueSpent < req.ContinueSegments && len(acc.frontiers) > 0 {
		before := len(acc.frontiers)
		if !seqContinueBatch(a, req, -1) {
			// 本批只有已展开/身份变化记录：队列可能缩短，继续；若长度未变
			// 则避免死循环。
			if len(acc.frontiers) >= before {
				break
			}
		}
		seqResolvePendingResults(req)
	}
	// 预算耗尽时仍停在 depth 的前沿：保留原 depth 诊断 + 记账（同一前沿只计一次）
	for _, f := range acc.frontiers {
		if f.node.Stop != nil && f.node.Stop.Code == SeqStopDepthLimit {
			acc.continueBudgetStops++
			acc.continuation = append(acc.continuation, SeqContinuationFrontier{
				Segment: acc.continueUsed + 1, CallFile: f.callFile, CallLine: f.callLine,
				TargetID: f.target, Repo: f.repo, DepthReason: f.reason, Continued: false,
			})
		}
	}
	acc.frontiers = nil
}

// seqContinuationMeta 组装 meta.continuation（未启用时返回 nil——请求身份不变）。
func seqContinuationMeta(req CodeSequenceRequest) *SeqContinuationMeta {
	acc := req.acc
	if req.ContinueSegments <= 0 || acc == nil {
		return nil
	}
	m := &SeqContinuationMeta{
		SegmentsBudget:   req.ContinueSegments,
		SegmentsUsed:     acc.continueUsed,
		BudgetStops:      acc.continueBudgetStops,
		Frontiers:        append([]SeqContinuationFrontier{}, acc.continuation...),
		CompatMode:       req.continuationCompat,
		CompatMaxSegment: req.ContinueSegments,
		ExtraExpansions:  acc.continueExpansions,
		Expanded:         acc.continueExpansions,
	}
	if len(m.Frontiers) == 0 {
		m.Note = "没有停在 depth 的可续查前沿（无需追加段）"
	}
	return m
}

// seqHasDepthStop 树里是否仍有停在 depth 的节点（续查后 stop 口径重算）。
func seqHasDepthStop(n *CodeSeqNode) bool {
	if n == nil {
		return false
	}
	if n.Stop != nil && n.Stop.Code == SeqStopDepthLimit {
		return true
	}
	for _, c := range n.Nodes {
		if seqHasDepthStop(c) {
			return true
		}
	}
	for _, c := range n.Else {
		if seqHasDepthStop(c) {
			return true
		}
	}
	return false
}

// seqRegisterCallFrontier 登记普通调用前沿时，把**调用点上下文**（工厂来源 /
// 局部实例字段到达定义）一并放进请求快照——续查在 Step/Get 等下游函数里
// 才能得到与完整展开相同的身份（T12/T05）。
func seqRegisterCallFrontier(a *Actions, req CodeSequenceRequest, node *CodeSeqNode,
	fun ast.Expr, targetID string, f *ast.File, curPkg string, atOff int) {
	sub := req
	if origin := seqCallReceiverOrigin(a, req, fun, targetID, f, curPkg); origin != "" {
		sub.curRecvOrigin = origin
	}
	if fields := seqCallReceiverFields(a, req, fun, targetID, f, curPkg, atOff); len(fields) > 0 {
		sub.curRecvFields = fields
	}
	seqRegisterSymbolFrontier(a, sub, node)
}

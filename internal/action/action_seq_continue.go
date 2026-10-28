package action

// todo24 §2：`query sequence --code --continue-segments N`——在原调用上下文
// 里**分段续查 depth 前沿**，把结果接回同一棵分析树（JSON/文本/Mermaid/SVG
// 共用），而不是把前沿当独立根重跑前缀、也不是简单提高入口 depth。
//
// 语义：
//   - 初始展开仍按 `--depth` 进行；每个停在 depth 的调用节点登记一个前沿
//     （携带当时的请求快照：场景/实参/回调绑定/receiver/防环祖先路径）。
//   - 每个追加段对**当前前沿**各展开"原请求 depth 层"，新产生的 depth 前沿
//     留给下一段；段数预算耗尽时保留 depth 诊断，并明确记录 budget_stops。
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
		reason: reason, segment: acc.continueSegment,
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

// seqContinueSegments 依次展开前沿直到段数预算耗尽（同步，返回前完成）。
func seqContinueSegments(a *Actions, req CodeSequenceRequest, root *CodeSeqNode) {
	acc := req.acc
	if req.ContinueSegments <= 0 || acc == nil || root == nil {
		return
	}
	for seg := 1; seg <= req.ContinueSegments; seg++ {
		pending := acc.frontiers
		acc.frontiers = nil
		if len(pending) == 0 {
			break
		}
		acc.continueSegment = seg
		acc.continueUsed = seg
		for _, f := range pending {
			// 已被上一层展开（同一节点重复登记/重复处理）：不再计一次
			if f.node.Stop == nil || f.node.Stop.Code != SeqStopDepthLimit {
				continue
			}
			if req.ctx().Err() != nil {
				acc.cancel = true
				return
			}
			// T12：续查前核对身份——目标身份已变化（同一调用点解析到别的
			// 符号）时保留原前沿，不按新身份展开。
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
			f.resume(sub)
			acc.continuation = append(acc.continuation, SeqContinuationFrontier{
				Segment: seg, CallFile: f.callFile, CallLine: f.callLine,
				TargetID: f.target, Repo: f.repo, DepthReason: f.reason, Continued: true,
			})
			acc.continueExpansions++
		}
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

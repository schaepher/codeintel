package action

// todo22 §3.1/§3.3：调用点的"实现具体化"分支（从 action_seq_walk_call.go
// 拆出——行数治理）。
//
//   - 1b) DI 字段注入：显式字段写者（SSA 数据流，step 1）无结论时才用注册
//     证据；多重/动态/非引导注册 → 明确 unknown（不猜实现）。
//   - 2)  局部变量数据流具体化：`m := NewImpl()` / `m := &Impl{}` → (Impl).Method。
//
// 两者命中后都直接展开目标函数体并返回 true（调用方不再走接口派发/直接展开）。

import (
	"go/ast"
	"go/token"
	"strings"
	"time"

	"github.com/schaepher/codeintel/internal/domain"
)

// seqTryDIAndLocalVar 尝试 DI 字段注入与局部变量具体化；命中时已填好 node。
func seqTryDIAndLocalVar(a *Actions, req CodeSequenceRequest, f *ast.File, sel *ast.SelectorExpr,
	recvType string, recvDecl map[string]string, confidence float64, node *CodeSeqNode,
	depth int, curFile string, pos token.Position) bool {
	// 1b) DI 字段注入（证据：字段注解 + 引导函数里的显式注册）
	if recvType != "" {
		if field, ok := receiverFieldSel(sel, req.curRecvObj, req.curRecvName); ok {
			if di, ok2 := a.diFieldImpl(req, f, recvType, field, sel.Sel.Name); ok2 {
				if di.why != "" {
					node.Evidence = &SeqEvidence{Source: SeqEvidenceDI, CallFile: curFile,
						CallLine: pos.Line, Reason: di.reason, Ambiguous: true}
					node.Stop = &SeqStop{Code: SeqStopFuncValueUnbound, Detail: di.why}
					return true
				}
				node.ImplType = implTypeShort(di.symID)
				node.DeclType = recvDecl[field]
				ev := &SeqEvidence{Source: SeqEvidenceDI, CallFile: curFile, CallLine: pos.Line,
					SymbolID: di.symID, Confidence: confidence, Reason: di.reason,
					Candidates:   []string{di.symID},
					RegisterSite: di.reg.line, RegisterFile: di.reg.file}
				if di.contract != nil {
					ev.DIInjectFile = di.contract.file
					ev.DIInjectLine = di.contract.line
				}
				node.Evidence = ev
				seqExpandInto(a, req, node, di.symID, depth)
				return true
			}
		}
	}
	// 2) 局部变量数据流具体化（构造器/字面量初始化）
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	implMethod, okRT := seqReturnSlotImplMethod(a, req, sel)
	if !okRT {
		implMethod = a.localVarImplObj(req, f, pkgPathOf(req.curSym), id.Name, id.Obj, sel.Sel.Name)
	}
	if implMethod == "" {
		return false
	}
	node.ImplType = implTypeShort(implMethod)
	reason := "localvar-init-dataflow"
	if okRT {
		reason = "localvar-return-slot"
	}
	node.Evidence = &SeqEvidence{Source: SeqEvidenceLocalVar, CallFile: curFile, CallLine: pos.Line,
		SymbolID: implMethod, Confidence: confidence, Reason: reason}
	// T05：局部变量携带的实例来源（工厂调用）要传给子方法——否则
	// `box.Run → b.Step → b.Get().Work` 在 Step 里丢失工厂身份，字段续接
	// 直接放弃。
	sub := req
	if origin := seqCallReceiverOrigin(a, req, sel, implMethod, f, pkgPathOf(req.curSym)); origin != "" {
		sub.curRecvOrigin = origin
	}
	// T05：局部实例的字段到达定义随调用传递（Get().Work() 用最新写入）。
	sub.curRecvFields = seqCallReceiverFields(a, req, sel, implMethod, f, pkgPathOf(req.curSym), pos.Offset)
	seqExpandInto(a, sub, node, implMethod, depth)
	return true
}

// seqExpandInto 展开目标函数体（统一入口：子节点/停止诊断写到 node）。
// todo22 §4.1：目标是 go-micro 客户端桩时改为续接服务端子树。
func seqExpandInto(a *Actions, req CodeSequenceRequest, node *CodeSeqNode, symID string, depth int) {
	if seqExpandRPC(a, req, node, symID, depth, "", 0) {
		return
	}
	if child, stop := codeSeqForSymbol(a, req, symID, depth-1); child != nil {
		node.Nodes, node.Stop = child.Nodes, child.Stop
	} else if stop != nil {
		node.Stop = stop
	}
}

// seqExpandGrpcServiceImpl grpc 服务节点目标 → (Impl).Method 实现方法展开。
// 返回 true 表示已展开（调用方直接返回节点）；否则保持原终点诊断。
func seqExpandGrpcServiceImpl(a *Actions, req CodeSequenceRequest, node *CodeSeqNode,
	fun ast.Expr, tid string, confidence float64, depth int, curFile string, line int) bool {
	if strings.Contains(tid, ":(") {
		return false
	}
	sel, isSel := fun.(*ast.SelectorExpr)
	if !isSel {
		return false
	}
	entry := GrpcMethodEntryID(tid, sel.Sel.Name)
	if child, stop := codeSeqForSymbol(a, req, entry, depth-1); child != nil {
		if node.Evidence == nil || node.Evidence.Source == SeqEvidenceCallEdge {
			node.Evidence = &SeqEvidence{Source: SeqEvidenceGrpcImpl, CallFile: curFile, CallLine: line,
				SymbolID: entry, Confidence: confidence, Reason: "grpc-service-impl"}
		}
		node.Nodes, node.Stop = child.Nodes, child.Stop
		return true
	} else if stop != nil && node.Stop == nil {
		node.Stop = stop
	}
	return false
}

// seqExpandIfaceTarget todo24：接口方法目标 → 实现（多候选保留候选集合、不选
// 第一个；单实现展开；无可证明实现给明确诊断）。返回 true = 已处理该节点。
func seqExpandIfaceTarget(a *Actions, req CodeSequenceRequest, node *CodeSeqNode, tid string,
	fun ast.Expr, recvType, recvName string, recvDecl map[string]string,
	confidence float64, curFile string, pos token.Position, depth int) bool {
	if !a.isIfaceMethodID(tid) {
		return false
	}
	dispStart := time.Now()
	impl, cands, ok := a.seqIfaceMethodImpl(req, tid)
	req.acc.dispatch++
	req.acc.sqlQueries += 2 // InterfaceMethodImpl + InterfaceImplsExact（精确查询）
	req.acc.dispatchDur += time.Since(dispStart)
	if ok {
		// T07：多个未验证候选 → 互斥候选分支（每个候选最多展开 2 层、
		// 最多 12 个实现）；不把任一候选当确定执行顺序。
		all := cands
		expandCands, truncated := capSeqCandidates(all)
		if len(all) > 1 {
			ev := seqIfaceEvidence(curFile, pos.Line, "", all, confidence)
			ev.Reason = "implementation-alternatives"
			ev.CandidateBudget = seqIfaceExpandBudget(len(all), len(expandCands), truncated)
			node.Evidence = ev
			node.Stop = &SeqStop{Code: SeqStopIfaceAmbiguous,
				Detail: "接口实现未消歧；候选分支不是已确定执行顺序"}
			seqExpandCandidateBranches(a, req, node, expandCands, depth, pos, truncated, len(all))
			return true
		}
		// U06/T07：唯一实现不等于实际实例来源——接口派发前先验证接收者
		// 表达式（形参/局部别名/未知返回值/包级变量）。未证明时停在接口边界。
		if detail, unknownRcvr := seqReceiverInstanceUnknown(a, req, fun, recvType, recvName, recvDecl); unknownRcvr {
			markSeqUnknownInstance(node, curFile, pos.Line, confidence, detail)
			return true
		}
		node.ImplType = implTypeShort(impl)
		node.Evidence = seqIfaceEvidence(curFile, pos.Line, impl, cands, confidence)
		node.Evidence.CandidateBudget = seqIfaceBudget(len(cands), truncated)
		// todo22 §4.1：接口消歧命中的实现是 go-micro 客户端桩 → 续接
		// 服务端子树（不展开桩源码）。
		if seqExpandRPC(a, req, node, impl, depth, curFile, pos.Line) {
			return true
		}
		if child, stop := codeSeqForSymbol(a, req, impl, depth-1); child != nil {
			node.Nodes = child.Nodes
			return true
		} else if stop != nil {
			node.Stop = stop
		}
	} else {
		// todo13-13：接口身份与调用位置已知，但无可证明实现——
		// 不得把“调用边已知”当作“实现已验证”，也不冒充索引缺失。
		node.Stop = &SeqStop{Code: SeqStopUnknownImpl,
			Detail: "接口方法 " + tid + " 无可证明实现（调用边/接口身份已知，实现未验证）"}
		return true
	}
	return false
}

// seqExpandSceneIfaceChoice todo11-2：接口方法调用的实现选择（场景守卫优先；
// 未唯一确定时保留完整候选、不展开单一实现）。返回 true = 已处理该节点。
func seqExpandSceneIfaceChoice(a *Actions, req CodeSequenceRequest, node *CodeSeqNode,
	fun ast.Expr, fset *token.FileSet, pos token.Position, tid string, confidence float64,
	curFile string, depth int) bool {
	if !strings.Contains(tid, ":(") {
		return false
	}
	sel, isSel := fun.(*ast.SelectorExpr)
	if !isSel {
		return false
	}
	recv, isID := sel.X.(*ast.Ident)
	if !isID {
		return false
	}
	dispStart := time.Now()
	f, okF := req.curCtx.file, req.curCtx != nil
	if !okF {
		return false
	}
	impl, reason, cands, guard, ok := a.sceneImplChoice(req, fset, pos.Offset, f, recv.Obj, recv.Name,
		sel.Sel.Name, pkgOfID(domain.CanonicalID(tid)))
	req.acc.dispatch++
	req.acc.dispatchDur += time.Since(dispStart)
	if !ok {
		return false
	}
	ev := seqIfaceEvidence(curFile, pos.Line, impl, cands, confidence)
	if !guard {
		// 到达定义存在但场景未唯一确定——保留完整候选集合，不展开单一实现。
		ev.Reason = "reaching-guard-unknown"
		ev.Ambiguous = true
		ev.Candidates = cands
		ev.SymbolID = ""
		ev.CandidateBudget = seqIfaceBudget(len(cands), false)
		node.Evidence = ev
		return true
	}
	node.ImplType = implTypeShort(impl)
	ev.Reason = reason
	ev.Ambiguous = false
	ev.Candidates = []string{impl}
	node.Evidence = ev
	if seqExpandRPC(a, req, node, impl, depth, curFile, pos.Line) {
		return true
	}
	// T05：接口变量/局部实例的字段到达定义随调用传递（Get().Work() 用
	// 调用点之前最后一次写入；场景守卫已在上方选择实现）。
	sub := req
	sub.curRecvFields = seqCallReceiverFields(a, req, fun, impl, req.curCtx.file,
		pkgOfID(domain.CanonicalID(tid)), pos.Offset)
	if child, stop := codeSeqForSymbol(a, sub, impl, depth-1); child != nil {
		node.Nodes, node.Stop = child.Nodes, child.Stop
		return true
	} else if stop != nil {
		node.Stop = stop
	}
	return false
}

// T07：未验证实现候选的互斥分支展开（每个候选 ≤2 层、≤12 个；后代标
// Candidate=true——不计确定执行、不登记续查前沿）。
func seqExpandCandidateBranches(a *Actions, req CodeSequenceRequest, node *CodeSeqNode,
	cands []string, depth int, pos token.Position, truncated bool, total int) {
	const candDepth = 2
	candReq := req
	candReq.inCandidate = true
	cd := depth - 1
	if cd < 1 {
		// depth=1 也要展开候选体一层（Guard/实现子调用可见；候选仍不计
		// 确定执行）。
		cd = 1
	}
	if cd > candDepth {
		cd = candDepth
	}
	var build func(i int) []*CodeSeqNode
	build = func(i int) []*CodeSeqNode {
		if i >= len(cands) {
			return nil
		}
		b := &CodeSeqNode{Kind: "branch", Label: "unverified implementation: " + cands[i],
			Line: pos.Line, Candidate: true}
		if child, stop := codeSeqForSymbol(a, candReq, cands[i], cd); child != nil {
			b.Nodes = child.Nodes
			markSeqCandidateSubtree(b.Nodes)
		} else if stop != nil {
			b.Stop = stop
		}
		if i+1 < len(cands) {
			b.Else = build(i + 1)
		}
		return []*CodeSeqNode{b}
	}
	if len(cands) > 0 {
		node.Evidence.CandidateDepth = candDepth
	}
	node.Nodes = build(0)
	if truncated {
		// U06b：预算截断本身是候选边界——明确标 candidate（不计确定执行）。
		node.Nodes = append(node.Nodes, &CodeSeqNode{Kind: "branch",
			Label: "candidate budget", Line: pos.Line, Candidate: true,
			Stop: &SeqStop{Code: SeqStopCandidateBudget,
				Detail: "候选实现超过展开预算（" + itoaSeq(len(cands)) + "/" + itoaSeq(total) + "）；其余候选未展开"}})
	}
}

// markSeqCandidateSubtree 把候选分支内全部节点标为 Candidate（后代统计口径：
// 不计确定执行）。
func markSeqCandidateSubtree(nodes []*CodeSeqNode) {
	for _, n := range nodes {
		if n == nil {
			continue
		}
		n.Candidate = true
		markSeqCandidateSubtree(n.Nodes)
		markSeqCandidateSubtree(n.Else)
	}
}

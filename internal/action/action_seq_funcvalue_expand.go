package action

import "go/ast"

// seqFillFuncValueCall 把函数值调用的展开结果写进调用节点（node 已含
// label/actor/line）。
func seqFillFuncValueCall(a *Actions, req CodeSequenceRequest, call *ast.CallExpr,
	fv seqValue, node *CodeSeqNode, depth int) {
	switch fv.kind {
	case seqValueUnknown:
		node.Evidence = &SeqEvidence{Source: SeqEvidenceClosure, CallFile: req.curFile,
			CallLine: node.Line, Reason: seqReasonUnknown, Ambiguous: true}
		node.Stop = &SeqStop{Code: SeqStopFuncValueUnbound, Detail: fv.why}
		return
	case seqValueMulti:

		node.Evidence = &SeqEvidence{Source: SeqEvidenceClosure, CallFile: req.curFile,
			CallLine: node.Line, Reason: seqReasonMulti, Ambiguous: true,
			Candidates: seqValueCandidates(fv)}
		for _, cand := range fv.cands {
			child := &CodeSeqNode{Kind: "call", Label: cand.label(), Actor: seqValueActor(cand), Line: node.Line}
			seqExpandSingleValue(a, req, call, cand, child, depth)
			node.Nodes = append(node.Nodes, child)
		}
		if len(node.Nodes) == 0 {
			node.Stop = &SeqStop{Code: SeqStopFuncValueUnbound, Detail: fv.why}
		}
		return
	case seqValueList:

		node.Evidence = &SeqEvidence{Source: SeqEvidenceClosure, CallFile: req.curFile,
			CallLine: node.Line, Reason: seqReasonList, Candidates: seqValueCandidates(fv)}
		if fv.why != "" {
			node.Stop = &SeqStop{Code: SeqStopFuncValueUnbound, Detail: fv.why}
			node.Evidence.Ambiguous = true
			return
		}
		for _, elem := range fv.elems {
			child := &CodeSeqNode{Kind: "call", Label: elem.label(), Actor: seqValueActor(elem), Line: node.Line}
			seqExpandSingleValue(a, req, call, elem, child, depth)
			node.Nodes = append(node.Nodes, child)
		}
		if len(node.Nodes) == 0 {
			node.Stop = &SeqStop{Code: SeqStopNoCalls, Detail: "空函数列表（确定无元素执行）"}
		}
	default:
		seqExpandSingleValue(a, req, call, fv, node, depth)
	}
}

// seqExpandSingleValue 展开**一个**函数值：字面量走内联遍历，具名函数走
// codeSeqForSymbol（两者都把本次调用的实参绑定到被展开函数的形参）。
func seqExpandSingleValue(a *Actions, req CodeSequenceRequest, call *ast.CallExpr,
	fv seqValue, node *CodeSeqNode, depth int) {
	reason := seqReasonForOrigin(fv.origin)

	args := captureSeqClosureArgs(a, req, req.curCtx, call)
	switch fv.kind {
	case seqValueLit:
		node.Evidence = &SeqEvidence{Source: SeqEvidenceClosure, CallFile: req.curFile,
			CallLine: node.Line, Reason: reason + "-literal"}
		if depth <= 1 {
			req.acc.depth = true
			node.Stop = &SeqStop{Code: SeqStopDepthLimit,
				Detail: "depth 上限（--depth " + itoaSeq(req.Depth) + "），未展开该回调内部"}

			seqRegisterFrontier(req, node, fv.symID, func(sub CodeSequenceRequest) {
				seqExpandSingleValue(a, sub, call, fv, node, sub.Depth+1)
			}, node.Stop.Detail)
			return
		}
		if fv.ctx == nil || fv.lit == nil || fv.lit.Body == nil {
			node.Stop = &SeqStop{Code: SeqStopFuncValueUnbound, Detail: "函数字面量缺失源码上下文"}
			return
		}
		key := seqLitVisitKey(fv)
		if key != "" {
			if req.litVisited[key] {
				node.Stop = &SeqStop{Code: SeqStopCycle, Detail: "该函数字面量已在展开路径上（防环）"}
				return
			}
			req.litVisited[key] = true
			defer delete(req.litVisited, key)
		}
		sub := req
		if fv.callbackList {
			sub.inCallbackList = true
		}
		// U04j：调度（defer/go）的闭包体读取外层字段时不能沿用当前值；
		// 立即求值的实参（IIFE 参数）不受影响（req.schedFieldMask 只在目标
		// 调用上置位）。
		if req.scheduled && req.schedFieldMask {
			mask := newSeqEnv(fv.env)
			mask.fieldsUnknown = true
			sub.env = newSeqEnv(mask)
		} else {
			sub.env = newSeqEnv(fv.env)
		}
		bindSeqParams(sub.env, fv.lit.Type.Params, args)

		sub.curSym = fv.enclosing
		if sub.curSym == "" {
			sub.curSym = req.curSym
		}
		sub.curTgts = seqTargetsCached(a, req, fv.enclosing)
		sub.curCtx = fv.ctx

		sub.curFile = fv.ctx.rel

		sub.callbackCtx = seqCallbackVisitCtx(fv)
		sub.curRecvType = fv.recvType

		sub.curRecvName = fv.recvName
		sub.curRecvObj = fv.recvObj
		sub.curRecvDecl = fv.recvDecl
		sub.sceneConsts = seqConstValues(a, req, fv.ctx.rel)

		sub.sceneArgs = copyStrMap(req.sceneArgs)
		sub.sceneSrc = copyStrMap(req.sceneSrc)
		sub.sceneWhy = copyStrMap(req.sceneWhy)
		applySceneFieldArgs(sub.sceneArgs, sub.sceneSrc, sub.sceneWhy, fv.lit.Type.Params, req.callArgFields)
		sub.paramValues = nil
		node.Nodes = walkStmts(a, sub, fv.ctx.fset, fv.ctx.src, fv.lit.Body.List,
			sub.curTgts, depth, fv.recvType, fv.recvDecl, fv.ctx.file, fv.pkgPath, fv.ctx.rel)
		// U04x：立即执行的函数字面量（IIFE）对捕获变量的写入回到外层环境
		// （参数/内部局部不回写）；go/defer 调度的闭包不合并。
		if lit, ok := call.Fun.(*ast.FuncLit); ok && lit == fv.lit && req.env != nil {
			seqMergeImmediateClosureEnv(req.env, sub.env, lit)
		}
		if len(node.Nodes) == 0 {
			node.Stop = &SeqStop{Code: SeqStopNoCalls}
		}
	case seqValueNamed:
		node.Evidence = &SeqEvidence{Source: SeqEvidenceClosure, CallFile: req.curFile,
			CallLine: node.Line, SymbolID: fv.symID, Reason: reason + "-named"}
		if depth <= 1 {
			req.acc.depth = true
			node.Stop = &SeqStop{Code: SeqStopDepthLimit,
				Detail: "depth 上限（--depth " + itoaSeq(req.Depth) + "），未展开该调用内部"}
			seqRegisterFrontier(req, node, fv.symID, func(sub CodeSequenceRequest) {
				seqExpandSingleValue(a, sub, call, fv, node, sub.Depth+1)
			}, node.Stop.Detail)
			return
		}
		sub := req
		sub.paramValues = args

		sub.callbackCtx = seqCallbackVisitCtx(fv)
		if child, stop := codeSeqForSymbol(a, sub, fv.symID, depth-1); child != nil {
			node.Nodes, node.Stop = child.Nodes, child.Stop
		} else if stop != nil {
			node.Stop = stop
		}
	case seqValueList:

		node.Evidence = &SeqEvidence{Source: SeqEvidenceClosure, CallFile: req.curFile,
			CallLine: node.Line, Reason: seqReasonList, Candidates: seqValueCandidates(fv)}
		for _, elem := range fv.elems {
			child := &CodeSeqNode{Kind: "call", Label: elem.label(), Actor: seqValueActor(elem), Line: node.Line}
			seqExpandSingleValue(a, req, call, elem, child, depth)
			node.Nodes = append(node.Nodes, child)
		}
		if len(node.Nodes) == 0 {
			node.Stop = &SeqStop{Code: SeqStopNoCalls, Detail: "空函数列表（确定无元素执行）"}
		}
	default:
		node.Evidence = &SeqEvidence{Source: SeqEvidenceClosure, CallFile: req.curFile,
			CallLine: node.Line, Reason: seqReasonUnknown, Ambiguous: true}
		node.Stop = &SeqStop{Code: SeqStopFuncValueUnbound, Detail: "函数值未解析"}
	}
}

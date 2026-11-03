package action

// 已解析调用的具体化展开（接口/字段/DI/grpc/工厂/直接展开——行数治理）。

import (
	"go/ast"
	"go/token"
)

func seqExpandResolvedCall(a *Actions, req CodeSequenceRequest, node *CodeSeqNode, fun ast.Expr,
	f *ast.File, curPkg, curFile string, fset *token.FileSet, src []byte, tgts *seqTargets,
	tg seqTarget, recvType string, recvDecl map[string]string, call *ast.CallExpr,
	depth int, pos token.Position) bool {
	tid := tg.tid
	// 0) 场景/守卫优先的接口实现选择（todo11-2；见 walk_impl.go）
	if seqExpandSceneIfaceChoice(a, req, node, fun, fset, pos, tid, tg.confidence,
		curFile, depth) {
		return true
	}

	// 1) receiver 字段数据流具体化（R97-2 / U06 边界）
	unknownField := ""
	if sel, isSel := fun.(*ast.SelectorExpr); isSel && recvType != "" {
		if field, ok2 := receiverFieldSel(sel, req.curRecvObj, req.curRecvName); ok2 {
			impls, unknownWriters := a.receiverFieldImplsEx(req, recvType, field, sel.Sel.Name)
			// 写者来源只证明到接口本身（如工厂返回接口）→ 仍无实例来源。
			if len(impls) == 1 && a.isIfaceMethodID(impls[0]) {
				impls = nil
			}
			if unknownWriters {
				// U06：字段存在无法证明的写者（形参/未知方法）——即便还有具体
				// 写入来源也不能当唯一实例。
				impls = nil
			}
			if len(impls) > 1 {
				node.Evidence = &SeqEvidence{Source: SeqEvidenceFieldFlow, CallFile: curFile, CallLine: pos.Line,
					Confidence: tg.confidence, Reason: "receiver-field-dataflow", Candidates: impls,
					Ambiguous: true, CandidateBudget: seqIfaceBudget(len(impls), false)}
				node.Stop = &SeqStop{Code: SeqStopIfaceAmbiguous,
					Detail: "字段 " + recvType + "." + field + " 有 " + itoaSeq(len(impls)) + " 个具体实现写入来源（未消歧，未展开）"}
				return true
			}
			// U06：无写入证明先记下（DI 证明在下一步检查）。字段声明类型
			// 是接口时，即使调用边/唯一实现指向具体类型也不得借用——
			// 实例来源未知就保留接口调用与未验证边界。
			if len(impls) == 0 {
				if a.isIfaceMethodID(tid) || seqDeclaredTypeIsIface(a, req, recvDecl[field]) {
					unknownField = field
				}
			}
			if len(impls) == 1 {
				implMethod := impls[0]
				node.ImplType = implTypeShort(implMethod)
				if decl, ok := recvDecl[field]; ok {
					node.DeclType = decl
				}
				node.Evidence = &SeqEvidence{Source: SeqEvidenceFieldFlow, CallFile: curFile, CallLine: pos.Line,
					SymbolID: implMethod, Confidence: tg.confidence, Reason: "receiver-field-dataflow"}
				if child, stop := codeSeqForSymbol(a, req, implMethod, depth-1); child != nil {
					node.Nodes, node.Stop = child.Nodes, child.Stop
					return true
				} else if stop != nil {
					node.Stop = stop
				}
			}
		}
	}

	// 1b) DI 字段注入（§3.3）+ 2) 局部变量数据流具体化（P0-6）
	if sel, isSel := fun.(*ast.SelectorExpr); isSel {
		if seqTryDIAndLocalVar(a, req, f, sel, recvType, recvDecl, tg.confidence,
			node, depth, curFile, pos) {
			return true
		}
	}
	// U06：接口字段既无字段写入、也无 DI/局部变量证明——唯一实现不等于
	// 实际实例来源；保留接口调用与未验证边界，不接成确定调用。
	if unknownField != "" {
		markSeqUnknownReceiver(node, curFile, pos.Line, tg.confidence, recvType, unknownField)
		return true
	}
	// 3) 接口方法 → 实现方法（implements 边 + 方法名；候选可多个）。
	// todo11-2：实参字面量走有界值流解析（≤2 层），解析不到保持空串。
	args := a.resolveSceneArgLiterals(fset, src, call, req.curBody, req.curParams,
		req.incomingArgs, req.incomingSrc, req.sceneConsts, maxSceneFlowDepth)
	req.callArgLits, req.callArgSrcs, req.callArgWhy = sceneArgValues(args)
	req.callArgFields = a.resolveSceneArgFields(req, fset, src, call, req.curBody, req.curParams)
	// T04：`Get().Work()` —— 先按 getter 返回来源具体化；来源未知时明确
	// unknown_receiver（不借全库唯一实现）。
	if a.isIfaceMethodID(tid) {
		if seqExpandGetterIfaceCall(a, req, node, fun, f, curPkg, depth, curFile, pos) {
			return true
		}
	}
	// U06/T07：候选集保留在接口派发里；这里把接收者表达式带下去，单实现
	// 也要先验证实例来源（形参/别名/未知返回值的唯一实现不算证据）。
	if seqExpandIfaceTarget(a, req, node, tid, fun, recvType, req.curRecvName, recvDecl,
		tg.confidence, curFile, pos, depth) {
		return true
	}

	// 4) grpc 服务的实现方法（服务节点 → (Impl).Method；见 walk_impl.go）
	if seqExpandGrpcServiceImpl(a, req, node, fun, tid, tg.confidence, depth, curFile, pos.Line) {
		return true
	}

	// T04：接收者是工厂调用结果（`FromField(1).Work()`）——先按工厂
	// return 的守卫裁剪具体来源；无法证明时再给 unknown（调用边指向的
	// 具体方法不能当实例来源证明）。
	if seqExpandFactoryCallReceiver(a, req, node, fun, f, curPkg, depth, curFile, pos) {
		return true
	}
	if detail, unproven := seqReceiverCallUnproven(a, req, fun, f, curPkg); unproven {
		markSeqUnknownInstance(node, curFile, pos.Line, tg.confidence, detail)
		return true
	}

	// 5) 直接展开（todo25 §3.4：把接收者实例来源传给子方法）。
	subReq := req
	if origin := seqCallReceiverOrigin(a, req, fun, tid, f, curPkg); origin != "" {
		subReq.curRecvOrigin = origin
	}
	// T05：局部实例的字段到达定义（顺序敏感）也传给子方法。
	subReq.curRecvFields = seqCallReceiverFields(a, req, fun, tid, f, curPkg, pos.Offset)
	if child, stop := codeSeqForSymbol(a, subReq, tid, depth-1); child != nil {
		node.Nodes, node.Stop = child.Nodes, child.Stop
	} else if stop != nil && node.Stop == nil {
		node.Stop = stop
	}
	return false
}

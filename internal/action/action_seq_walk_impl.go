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
	implMethod := a.localVarImpl(req, f, pkgPathOf(req.curSym), id.Name, sel.Sel.Name)
	if implMethod == "" {
		return false
	}
	node.ImplType = implTypeShort(implMethod)
	node.Evidence = &SeqEvidence{Source: SeqEvidenceLocalVar, CallFile: curFile, CallLine: pos.Line,
		SymbolID: implMethod, Confidence: confidence, Reason: "localvar-init-dataflow"}
	seqExpandInto(a, req, node, implMethod, depth)
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

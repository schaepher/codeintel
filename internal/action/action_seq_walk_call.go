package action

// todo10-2/10-3：调用节点构造（含接口/字段/局部变量具体化与嵌套展开）。
// 从 walkStmt 的闭包提升为顶层函数——行数治理（walkStmt 曾超 300 行）。
//
// todo10-3 起每个展开点都写节点证据（canonical ID/文件/行/依据/置信度），
// 并给"在那里停下"的调用点写终点诊断（depth/stop_pkg/cycle/…），
// 接口派发多候选时记录候选集合 + 依据 + ambiguous（不静默选一个）。

import (
	"go/ast"
	"go/token"
	"strings"
)

// newSeqCallNode 返回"位置 → 调用节点"的构造函数（闭包捕获遍历上下文，
// 保持 walkStmt 的调用点写法不变）。
func newSeqCallNode(a *Actions, req CodeSequenceRequest, fset *token.FileSet, src []byte,
	tgts *seqTargets, depth int, recvType string, recvDecl map[string]string,
	f *ast.File, curPkg, curFile string) func(call *ast.CallExpr) *CodeSeqNode {
	return func(call *ast.CallExpr) (node *CodeSeqNode) {
		// T02：纯内建（append/len/cap/copy/make/new/...）不是代码图里的函数
		// 调用——不产生调用节点，也不把回调实参误判成"在未解析调用中逃逸"。
		if seqPureBuiltinCall(call.Fun) {
			return nil
		}
		// 类型转换（`API(&Two{})` / `(*T)(v)`）不是函数调用——不产生调用节点。
		if seqPureTypeConversionCall(call) {
			return nil
		}
		defer func() {
			seqAnnotateCallEvidence(a, req, node, call, f, fset, src, curPkg, curFile)
		}()
		fun := call.Fun
		pos := fset.Position(call.Lparen)
		node = &CodeSeqNode{Kind: "call", Label: callLabel(fset, src, fun), Actor: callActor(fset, src, fun), Line: pos.Line}
		// U04k：AST 调用 → 最新节点（调用方绑定返回槽来源用）。
		if req.acc != nil {
			if req.acc.callNodes == nil {
				req.acc.callNodes = map[*ast.CallExpr]*CodeSeqNode{}
			}
			req.acc.callNodes[call] = node
		}
		// todo22 P1：本次调用的结构体字段已知值（函数值/回调路径也要用）
		req.callArgFields = a.resolveSceneArgFields(req, fset, src, call, req.curBody, req.curParams)

		// U04m：`result.RowsAffected()`（接收者是已证明的 Exec 返回槽 0）——
		// 准确的 database/sql 方法身份，作为影响行数来源终端。
		if seqMethodNameOfCall(call) == "RowsAffected" && seqSQLResultOf(req, selectorReceiver(call)) != nil {
			node.Evidence = &SeqEvidence{Source: SeqEvidenceAST, CallFile: curFile, CallLine: pos.Line,
				SymbolID: seqSQLRowsAffected, Reason: "sql-result-rows-affected",
				Verdict: SeqVerdictCorroborated}
			node.Stop = &SeqStop{Code: SeqStopExternalTarget,
				Detail: "标准库方法终端（影响行数来源；计数读取错误与事务提交未证明）"}
			return node
		}

		// todo25 §4.3：标准 HTTP 客户端调用（net/http.Get/Head/Post）——按
		// 静态 URL + 服务端注册证明续接 handler；动态/HTTPS/未证明保持诊断。
		if seqExpandHTTPAt(a, req, node, call, f, depth, curFile, pos) {
			return node
		}

		// todo22 §4.1：本调用点是否有 RPC 出站（go-micro 客户端接口调用）——
		// 直接续接服务端子树，不进入接口派发（结构相同的多个客户端接口会
		// 互相"实现"，接口候选无法消歧；调用点签名已证明是 go-micro 协议）。
		if seqExpandRPCAt(a, req, node, req.curSym, pos, seqCallMethodName(fun), depth, curFile) {
			return node
		}
		// todo22 §2：被调者本身是函数值（IIFE / 闭包变量 / 回调形参）——按
		// 调用点的数据流展开；不是函数值时继续走索引调用边/接口派发路径。
		if fv, isFV := seqCallFuncValue(a, req, call); isFV {
			seqFillFuncValueCall(a, req, call, fv, node, depth)
			return node
		}
		// todo22 §2：本次调用的实参值（按位置）——被展开函数绑定形参后，
		// 形参调用处才能证明执行的是哪个回调。
		req.paramValues = captureSeqClosureArgs(a, req, req.curCtx, call)
		// U04c：实参的标量来源（同一求值位置立即捕获）。
		req.callArgOrigins = captureSeqArgOrigins(req, call)
		// U04h：实参携带的写入错误结果（形参透传）。
		req.callArgErrResults = captureSeqArgErrResults(req, call)
		// U04k：实参标识符对象（depth=1 的延迟来源透传给形参）。
		req.callArgObjs = nil
		for _, arg := range call.Args {
			req.callArgObjs = append(req.callArgObjs, seqIdentObjOf(arg))
		}
		// todo24 §11.5：`Replace(&f)` 这类取址实参可能改写回调槽——先降级为
		// 未知，后续同作用域使用不得沿用旧绑定。
		seqInvalidateAddrTaken(req, call)

		// todo20-9：索引 calls 边按 (caller,target,kind) 汇总，重复调用
		// 只有首个位置的 metadata——按本函数目标集合 + 调用名回退。
		// todo23 §3.1：旧行号命中也要核对调用名/包/种类（同行多包不串）。
		tg, resolved := tgts.lookupCall(pos, fun, f, curPkg, recvType, req.curRecvName, recvDecl)
		if resolved {
			tid := seqRefineInterfaceTarget(a, req, fun, tg.tid)
			if seqFilterHit(req.Filter, tid, curFile, node.Label) {
				return nil
			}
			sigID := tid
			if !strings.Contains(tid, ":(") {
				if sel, isSel := fun.(*ast.SelectorExpr); isSel {
					sigID = GrpcMethodEntryID(tid, sel.Sel.Name)
				}
			}
			node.Type = implTypeShort(tid)
			// todo10-3：调用点证据（calls 边 + 置信度 + 调用位置）
			reason := "index-call-edge"
			if tg.aggregated {
				// 目标身份来自同函数汇总边（调用名唯一匹配），不是该位置的
				// pos 直接命中——证据只能标 corroborated。
				reason = SeqReasonAggregatedCallEdge
			}
			node.Evidence = &SeqEvidence{Source: SeqEvidenceCallEdge, CallFile: curFile,
				CallLine: pos.Line, SymbolID: tid, Confidence: tg.confidence, Reason: reason}
			// todo10-2 part3：被调者写焦点字段 → 该调用点命中
			if req.focus != nil && req.focus.writers[tid] {
				node.Focus = true
			}
			// todo12-9：依赖闭包里的调用（返回值/字段值的必要来源）命中
			if req.focusLines != nil && req.focusLines[pos.Line] {
				node.Focus = true
			}
			// todo22 §4.1：目标是 go-micro 客户端桩（NewRequest+Call）→ 在原
			// RPC 调用点续接服务端子树（不展开桩源码噪声）。depth=1 时保留
			// 准确目标 + 深度诊断。
			if seqExpandRPC(a, req, node, tid, depth, curFile, pos.Line) {
				return node
			}
			// todo10-9：有可解析的目标却因 depth 上限未展开 → 记"depth 截断"
			// （终止原因不是"流程结束"）
			if depth <= 1 {
				req.acc.depth = true
				node.Stop = &SeqStop{Code: SeqStopDepthLimit,
					Detail: "depth 上限（--depth " + itoaSeq(req.Depth) + "），未展开该调用内部"}
			}
			seqAttachCallSignature(a, req, node, sigID)
			if depth <= 1 {
				// todo24 §3.2/§11.2：depth 是展开预算，不是身份解析预算——先
				// 完成场景/工厂/字段/DI/接口消歧，再按 depth 停止展开。
				seqIdentityBeforeDepth(a, req, f, fset, fun, tid, node, recvType,
					req.curRecvName, recvDecl, tg.confidence, curFile, pos)
				// todo24 §2：停在 depth 的已证明调用登记为续查前沿
				seqRegisterCallFrontier(a, req, node, fun, tid, f, curPkg, pos.Offset)
				return node
			}
			if seqStopPkgHit(tid, req.StopPackages) {
				node.Stop = &SeqStop{Code: SeqStopPkg, Detail: pkgPathOf(tid) + "（seq.stop_packages）"}
				return node
			}
		} else if symID, ok := seqASTCallTarget(fun, f, curPkg); ok && seqTargetIndexed(req, curPkg, symID) {
			// todo22 §2.2：实参位置的调用（工厂/回调实参）在索引里没有 calls
			// 边——按 AST 对象身份/导入路径恢复**准确**符号后照常展开；
			// 不做模糊名称搜索。
			// T01：AST 身份恢复的调用也要给出参与者类型（与索引边路径一致）。
			if node.Type == "" {
				node.Type = implTypeShort(symID)
			}
			node.Evidence = &SeqEvidence{Source: SeqEvidenceAST, CallFile: curFile,
				CallLine: pos.Line, SymbolID: symID, Reason: "ast-symbol-identity"}
			if depth <= 1 {
				req.acc.depth = true
				node.Stop = &SeqStop{Code: SeqStopDepthLimit,
					Detail: "depth 上限（--depth " + itoaSeq(req.Depth) + "），未展开该调用内部"}
				// todo24 §2：AST 身份恢复的目标同样可续查
				seqRegisterCallFrontier(a, req, node, fun, symID, f, curPkg, pos.Offset)
				return node
			}
			if child, stop := codeSeqForSymbol(a, req, symID, depth-1); child != nil {
				node.Nodes, node.Stop = child.Nodes, child.Stop
			} else if stop != nil {
				node.Stop = stop
			}
			return node
		} else {
			if seqExpandUnresolvedCall(a, req, node, f, fun, call, depth, curPkg, curFile, pos.Line) {
				return node
			}
		}

		if depth <= 1 {
			return node
		}
		// todo25 §3.4：`b.Get().Work()`——被调者来自方法返回的字段时，沿
		// 工厂 setter 的实参证明具体实现（守卫保留为互斥分支）。这类调用
		// 常没有索引边（接口方法不建节点）——必须在 resolved 判断之前。
		if seqExpandFactoryFieldCall(a, req, node, fun, f, curPkg, depth, curFile, pos) {
			return node
		}
		if !resolved {
			return node
		}
		// todo20-9：重复调用点恢复目标后，仍要遵守 --focus return 的调用点
		// 依赖——不在返回依赖行上的调用不展开（否则被调者内部的字段写入
		// 会把无关实例的调用误判为命中）。
		if req.focusActive() && req.focus.returns && len(req.focus.fields) == 0 &&
			req.focusLines != nil && !req.focusLines[pos.Line] {
			return node
		}

		if seqExpandResolvedCall(a, req, node, fun, f, curPkg, curFile, fset, src, tgts, tg,
			recvType, recvDecl, call, depth, pos) {
			return node
		}
		return node
	}
}

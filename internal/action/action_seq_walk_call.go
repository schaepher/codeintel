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
	"time"

	"github.com/schaepher/codeintel/internal/domain"
)

// newSeqCallNode 返回"位置 → 调用节点"的构造函数（闭包捕获遍历上下文，
// 保持 walkStmt 的调用点写法不变）。
func newSeqCallNode(a *Actions, req CodeSequenceRequest, fset *token.FileSet, src []byte,
	tgts *seqTargets, depth int, recvType string, recvDecl map[string]string,
	f *ast.File, curPkg, curFile string) func(call *ast.CallExpr) *CodeSeqNode {
	return func(call *ast.CallExpr) (node *CodeSeqNode) {
		defer func() {
			attachRuntimeEvidence(req, node, call)
			finishSeqEvidence(req, node)
		}()
		fun := call.Fun
		pos := fset.Position(call.Lparen)
		node = &CodeSeqNode{Kind: "call", Label: callLabel(fset, src, fun), Actor: callActor(fset, src, fun), Line: pos.Line}
		// todo22 P1：本次调用的结构体字段已知值（函数值/回调路径也要用）
		req.callArgFields = a.resolveSceneArgFields(req, fset, src, call, req.curBody, req.curParams)

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
		// todo24 §11.5：`Replace(&f)` 这类取址实参可能改写回调槽——先降级为
		// 未知，后续同作用域使用不得沿用旧绑定。
		seqInvalidateAddrTaken(req, call)

		// todo20-9：索引 calls 边按 (caller,target,kind) 汇总，重复调用
		// 只有首个位置的 metadata——按本函数目标集合 + 调用名回退。
		// todo23 §3.1：旧行号命中也要核对调用名/包/种类（同行多包不串）。
		tg, resolved := tgts.lookupCall(pos, fun, f, curPkg, recvType, req.curRecvName, recvDecl)
		if resolved {
			tid := tg.tid
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
			sigStart := time.Now()
			args, rets, ok2 := sigTypesOf(a, sigID)
			req.acc.sigLookups++
			req.acc.symLookups++ // 签名解析内部 GetSymbol（精确查询，14.1）
			req.acc.sqlQueries++
			req.acc.symDur += time.Since(sigStart)
			req.acc.sigDur += time.Since(sigStart)
			if ok2 {
				node.Args, node.Returns = args, rets
			}
			if depth <= 1 {
				// todo24 §3.2/§11.2：depth 是展开预算，不是身份解析预算——先
				// 完成场景/工厂/字段/DI/接口消歧，再按 depth 停止展开。
				seqIdentityBeforeDepth(a, req, f, fset, fun, tid, node, recvType,
					req.curRecvName, recvDecl, tg.confidence, curFile, pos)
				// todo24 §2：停在 depth 的已证明调用登记为续查前沿
				seqRegisterSymbolFrontier(a, req, node)
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
				seqRegisterSymbolFrontier(a, req, node)
				return node
			}
			if child, stop := codeSeqForSymbol(a, req, symID, depth-1); child != nil {
				node.Nodes, node.Stop = child.Nodes, child.Stop
			} else if stop != nil {
				node.Stop = stop
			}
			return node
		} else {
			// 未解析到项目内符号：按消费者可区分的类别给出原因（todo13-13）
			// todo22 §4.2：外部依赖的按需源码解析（depth>1 才 go list；离线只读、
			// 请求内缓存）。展开成功时按外部函数自己的形参重新绑定回调。
			if seqExpandExternalCall(a, req, node, f, fun, call, depth, curFile, pos.Line) {
				return node
			}
			if seqExpandExternalMethodCall(a, req, node, call, depth, curFile, pos.Line) {
				return node
			}
			// todo12-13：AST 调用表达式存在 ≠ 目标实现已解析——明确终点诊断；
			// todo22 §2：函数值参数逃逸在分类里一并标注（不展开内部执行）。
			seqUnresolvedCallFallback(a, req, node, f, fun, call, curPkg, curFile)
		}

		if depth <= 1 {
			return node
		}
		if !resolved {
			return node
		}
		tid := tg.tid
		// todo20-9：重复调用点恢复目标后，仍要遵守 --focus return 的调用点
		// 依赖——不在返回依赖行上的调用不展开（否则被调者内部的字段写入
		// 会把无关实例的调用误判为命中）。
		if req.focusActive() && req.focus.returns && len(req.focus.fields) == 0 &&
			req.focusLines != nil && !req.focusLines[pos.Line] {
			return node
		}

		// todo25 §3.4：`b.Get().Work()`——被调者来自方法返回的字段时，沿
		// 工厂 setter 的实参证明具体实现（守卫保留为互斥分支）。
		if seqExpandFactoryFieldCall(a, req, node, fun, f, curPkg, depth, curFile, pos) {
			return node
		}

		// 0) todo11-2：接口方法调用的实现选择——场景守卫优先；多候选标 ambiguous
		if strings.Contains(tid, ":(") {
			if sel, isSel := fun.(*ast.SelectorExpr); isSel {
				if recv, isID := sel.X.(*ast.Ident); isID {
					dispStart := time.Now()
					impl, reason, cands, guard, ok := a.sceneImplChoice(req, fset, pos.Offset, f, recv.Obj, recv.Name,
						sel.Sel.Name, pkgOfID(domain.CanonicalID(tid)))
					req.acc.dispatch++
					req.acc.dispatchDur += time.Since(dispStart)
					if ok {
						ev := seqIfaceEvidence(curFile, pos.Line, impl, cands, tg.confidence)
						if !guard {
							// todo13-4：到达定义存在但场景未唯一确定——**不把首个实现
							// 当结论、不展开单一实现**；保留完整候选集合供 JSON/文本/
							// 图共用（未知候选不得绘成已确定调用）。
							ev.Reason = "reaching-guard-unknown"
							ev.Ambiguous = true
							ev.Candidates = cands
							ev.SymbolID = ""
							ev.CandidateBudget = seqIfaceBudget(len(cands), false)
							node.Evidence = ev
							return node
						}
						node.ImplType = implTypeShort(impl)
						ev.Reason = reason
						ev.Ambiguous = false
						ev.Candidates = []string{impl}
						node.Evidence = ev
						// todo22 §4.1：场景消歧后命中的实现若是 RPC 客户端桩，
						// 续接服务端子树（桩源码不是业务流程）。
						if seqExpandRPC(a, req, node, impl, depth, curFile, pos.Line) {
							return node
						}
						if child, stop := codeSeqForSymbol(a, req, impl, depth-1); child != nil {
							node.Nodes, node.Stop = child.Nodes, child.Stop
							return node
						} else if stop != nil {
							node.Stop = stop
						}
					}
				}
			}
		}

		// 1) receiver 字段数据流具体化（R97-2）
		if sel, isSel := fun.(*ast.SelectorExpr); isSel && recvType != "" {
			if field, ok2 := receiverFieldSel(sel, req.curRecvObj, req.curRecvName); ok2 {
				impls := a.receiverFieldImpls(req, recvType, field, sel.Sel.Name)
				if len(impls) > 1 {
					node.Evidence = &SeqEvidence{Source: SeqEvidenceFieldFlow, CallFile: curFile, CallLine: pos.Line,
						Confidence: tg.confidence, Reason: "receiver-field-dataflow", Candidates: impls,
						Ambiguous: true, CandidateBudget: seqIfaceBudget(len(impls), false)}
					node.Stop = &SeqStop{Code: SeqStopIfaceAmbiguous,
						Detail: "字段 " + recvType + "." + field + " 有 " + itoaSeq(len(impls)) + " 个具体实现写入来源（未消歧，未展开）"}
					return node
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
						return node
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
				return node
			}
		}

		// 3) 接口方法 → 实现方法（implements 边 + 方法名；候选可多个）
		// todo11-2：实参字面量走有界值流解析（变量链 + 形参向上，深度 ≤2）——
		// 一层字面量只是它的特例；解析不到保持空串（不猜）
		args := a.resolveSceneArgLiterals(fset, src, call, req.curBody, req.curParams,
			req.incomingArgs, req.incomingSrc, req.sceneConsts, maxSceneFlowDepth)
		req.callArgLits, req.callArgSrcs, req.callArgWhy = sceneArgValues(args)
		req.callArgFields = a.resolveSceneArgFields(req, fset, src, call, req.curBody, req.curParams)
		if seqExpandIfaceTarget(a, req, node, tid, tg.confidence, curFile, pos, depth) {
			return node
		}

		// 4) grpc 服务的实现方法（服务节点 → (Impl).Method；见 walk_impl.go）
		if seqExpandGrpcServiceImpl(a, req, node, fun, tid, tg.confidence, depth, curFile, pos.Line) {
			return node
		}

		// 5) 直接展开（索引 calls 边的目标）——todo25 §3.4：把"接收者实例
		// 来自哪个工厂"沿 b.Run→b.Step 传给子方法，字段续接才能区分实例。
		subReq := req
		if origin := seqCallReceiverOrigin(a, req, fun, tid, f, curPkg); origin != "" {
			subReq.curRecvOrigin = origin
		}
		if child, stop := codeSeqForSymbol(a, subReq, tid, depth-1); child != nil {
			node.Nodes, node.Stop = child.Nodes, child.Stop
		} else if stop != nil && node.Stop == nil {
			node.Stop = stop
		}
		return node
	}
}

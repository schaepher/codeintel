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
		defer func() { finishSeqEvidence(req, node) }()
		fun := call.Fun
		pos := fset.Position(call.Lparen)
		node = &CodeSeqNode{Kind: "call", Label: callLabel(fset, src, fun), Actor: callActor(fset, src, fun), Line: pos.Line}

		// todo22 §2：被调者本身是函数值（IIFE / 闭包变量 / 回调形参）——按
		// 调用点的数据流展开；不是函数值时继续走索引调用边/接口派发路径。
		if fv, isFV := seqCallFuncValue(req, call); isFV {
			seqFillFuncValueCall(a, req, call, fv, node, depth)
			return node
		}
		// todo22 §2：本次调用的实参值（按位置）——被展开函数绑定形参后，
		// 形参调用处才能证明执行的是哪个回调。
		req.paramValues = seqBindCallArgs(req, req.curCtx, call)

		tg, resolved := tgts.lookup(pos)
		if !resolved {
			// todo20-9：索引 calls 边按 (caller,target,kind) 汇总，重复调用
			// 只有首个位置的 metadata——按本函数目标集合 + 调用名回退。
			tg, resolved = tgts.matchAggregated(fun, f, curPkg, recvType, recvDecl)
		}
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
			node.Evidence = &SeqEvidence{Source: SeqEvidenceAST, CallFile: curFile,
				CallLine: pos.Line, SymbolID: symID, Reason: "ast-symbol-identity"}
			if depth <= 1 {
				req.acc.depth = true
				node.Stop = &SeqStop{Code: SeqStopDepthLimit,
					Detail: "depth 上限（--depth " + itoaSeq(req.Depth) + "），未展开该调用内部"}
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
			req.acc.unresolved++
			code, detail := classifyUnresolvedCall(f, fun, curPkg, req.pkgSet)
			// todo22 §2：函数值参数在未解析/外部调用里逃逸——无法确定是否、
			// 何时被调用，明确标注原因（不展开内部执行）。
			if esc := seqFuncArgEscape(req, call); esc != "" {
				detail += "；" + esc
			}
			node.Evidence = &SeqEvidence{Source: SeqEvidenceAST, CallFile: curFile, CallLine: pos.Line}
			// todo12-13：AST 调用表达式存在 ≠ 目标实现已解析——明确终点诊断
			node.Stop = &SeqStop{Code: code, Detail: detail}
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

		// 0) todo11-2：接口方法调用的实现选择——场景守卫优先；多候选标 ambiguous
		if strings.Contains(tid, ":(") {
			if sel, isSel := fun.(*ast.SelectorExpr); isSel {
				if recv, isID := sel.X.(*ast.Ident); isID {
					dispStart := time.Now()
					impl, reason, cands, guard, ok := a.sceneImplChoice(req, fset, pos.Offset, f, recv.Name,
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
							node.Evidence = ev
							return node
						}
						node.ImplType = implTypeShort(impl)
						ev.Reason = reason
						ev.Ambiguous = false
						ev.Candidates = []string{impl}
						node.Evidence = ev
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
			if field, ok2 := receiverFieldSel(sel, recvType); ok2 {
				if implMethod := a.receiverFieldImpl(req, recvType, field, sel.Sel.Name); implMethod != "" {
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

		// 2) 局部变量数据流具体化（P0-6）
		if sel, isSel := fun.(*ast.SelectorExpr); isSel {
			if id, ok := sel.X.(*ast.Ident); ok {
				if implMethod := a.localVarImpl(req, f, curPkg, id.Name, sel.Sel.Name); implMethod != "" {
					node.ImplType = implTypeShort(implMethod)
					node.Evidence = &SeqEvidence{Source: SeqEvidenceLocalVar, CallFile: curFile, CallLine: pos.Line,
						SymbolID: implMethod, Confidence: tg.confidence, Reason: "localvar-init-dataflow"}
					if child, stop := codeSeqForSymbol(a, req, implMethod, depth-1); child != nil {
						node.Nodes, node.Stop = child.Nodes, child.Stop
						return node
					} else if stop != nil {
						node.Stop = stop
					}
				}
			}
		}

		// 3) 接口方法 → 实现方法（implements 边 + 方法名；候选可多个）
		// todo11-2：实参字面量走有界值流解析（变量链 + 形参向上，深度 ≤2）——
		// 一层字面量只是它的特例；解析不到保持空串（不猜）
		args := a.resolveSceneArgLiterals(fset, src, call, req.curBody, req.curParams,
			req.incomingArgs, req.incomingSrc, maxSceneFlowDepth)
		req.callArgLits, req.callArgSrcs, req.callArgWhy = sceneArgValues(args)
		if a.isIfaceMethodID(tid) {
			dispStart := time.Now()
			impl, cands, ok := a.seqIfaceMethodImpl(tid)
			req.acc.dispatch++
			req.acc.sqlQueries += 2 // InterfaceMethodImpl + GetImplementsEdges
			req.acc.dispatchDur += time.Since(dispStart)
			if ok {
				node.ImplType = implTypeShort(impl)
				node.Evidence = seqIfaceEvidence(curFile, pos.Line, impl, cands, tg.confidence)
				if child, stop := codeSeqForSymbol(a, req, impl, depth-1); child != nil {
					node.Nodes = child.Nodes
					return node
				} else if stop != nil {
					node.Stop = stop
				}
			} else {
				// todo13-13：接口身份与调用位置已知，但无可证明实现——
				// 不得把“调用边已知”当作“实现已验证”，也不冒充索引缺失。
				node.Stop = &SeqStop{Code: SeqStopUnknownImpl,
					Detail: "接口方法 " + tid + " 无可证明实现（调用边/接口身份已知，实现未验证）"}
				return node
			}
		}

		// 4) grpc 服务的实现方法（服务节点 → (Impl).Method）
		if !strings.Contains(tid, ":(") {
			if sel, isSel := fun.(*ast.SelectorExpr); isSel {
				entry := GrpcMethodEntryID(tid, sel.Sel.Name)
				if child, stop := codeSeqForSymbol(a, req, entry, depth-1); child != nil {
					if node.Evidence == nil || node.Evidence.Source == SeqEvidenceCallEdge {
						node.Evidence = &SeqEvidence{Source: SeqEvidenceGrpcImpl, CallFile: curFile, CallLine: pos.Line,
							SymbolID: entry, Confidence: tg.confidence, Reason: "grpc-service-impl"}
					}
					node.Nodes, node.Stop = child.Nodes, child.Stop
					return node
				} else if stop != nil && node.Stop == nil {
					node.Stop = stop
				}
			}
		}

		// 5) 直接展开（索引 calls 边的目标）
		if child, stop := codeSeqForSymbol(a, req, tid, depth-1); child != nil {
			node.Nodes, node.Stop = child.Nodes, child.Stop
		} else if stop != nil && node.Stop == nil {
			node.Stop = stop
		}
		return node
	}
}

// seqIfaceEvidence 接口派发的证据：多候选时保留集合 + 依据 + ambiguous
// （要求：不能静默选择一个实现）。
func seqIfaceEvidence(callFile string, callLine int, chosen string, candidates []string, confidence float64) *SeqEvidence {
	ev := &SeqEvidence{Source: SeqEvidenceIface, CallFile: callFile, CallLine: callLine,
		SymbolID: chosen, Confidence: confidence, Candidates: candidates}
	switch len(candidates) {
	case 0, 1:
		ev.Reason = "unique"
	default:
		ev.Reason = "first-of-" + itoaSeq(len(candidates))
		ev.Ambiguous = true
	}
	return ev
}

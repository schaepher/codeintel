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

		tg, resolved := tgts.lookup(pos)
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
			node.Evidence = &SeqEvidence{Source: SeqEvidenceCallEdge, CallFile: curFile,
				CallLine: pos.Line, SymbolID: tid, Confidence: tg.confidence, Reason: "index-call-edge"}
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
			if args, rets, ok2 := sigTypesOf(a, sigID); ok2 {
				node.Args, node.Returns = args, rets
			}
			if depth <= 1 {
				return node
			}
			if seqStopPkgHit(tid, req.StopPackages) {
				node.Stop = &SeqStop{Code: SeqStopPkg, Detail: pkgPathOf(tid) + "（seq.stop_packages）"}
				return node
			}
		} else {
			// 未解析到项目内符号：外部库调用或索引覆盖缺失
			req.acc.unresolved++
			node.Evidence = &SeqEvidence{Source: SeqEvidenceAST, CallFile: curFile, CallLine: pos.Line}
			// todo12-13：AST 调用表达式存在 ≠ 目标实现已解析——明确终点诊断
			node.Stop = &SeqStop{Code: SeqStopUnresolved,
				Detail: "调用点未解析（外部库或索引覆盖缺失）——仅证明表达式存在"}
		}

		if depth <= 1 {
			return node
		}
		if !resolved {
			return node
		}
		tid := tg.tid

		// 0) todo11-2：接口方法调用的实现选择——场景守卫优先；多候选标 ambiguous
		if strings.Contains(tid, ":(") {
			if sel, isSel := fun.(*ast.SelectorExpr); isSel {
				if recv, isID := sel.X.(*ast.Ident); isID {
					if impl, reason, cands, guard, ok := a.sceneImplChoice(req, fset, pos.Offset, f, recv.Name,
						sel.Sel.Name, pkgOfID(domain.CanonicalID(tid))); ok {
						node.ImplType = implTypeShort(impl)
						ev := seqIfaceEvidence(curFile, pos.Line, impl, cands, tg.confidence)
						if guard {
							ev.Reason = reason
							ev.Ambiguous = false
							ev.Candidates = []string{impl}
						} else {
							// 到达定义存在但场景无法唯一确定：保留候选并明确 ambiguous
							ev.Reason = "reaching-guard-unknown"
							ev.Ambiguous = true
							ev.Candidates = cands
						}
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
		req.callArgLits, req.callArgSrcs = sceneArgValues(args)
		if strings.Contains(tid, ":(") {
			if impl, cands, ok := a.seqIfaceMethodImpl(tid); ok {
				node.ImplType = implTypeShort(impl)
				node.Evidence = seqIfaceEvidence(curFile, pos.Line, impl, cands, tg.confidence)
				if child, stop := codeSeqForSymbol(a, req, impl, depth-1); child != nil {
					node.Nodes = child.Nodes
					return node
				} else if stop != nil {
					node.Stop = stop
				}
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

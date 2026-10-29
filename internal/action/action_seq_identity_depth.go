package action

// todo24 §3.2/§11.2：depth=1 也要先完成**身份消歧**再应用展开预算。
//
// 规则：场景守卫 → 工厂/字段/DI/局部变量数据流 → 接口已证明实现；只写选定
// 目标的准确 canonical ID 与证据，不展开其内部（depth 诊断保留）。没消歧
// 证据时保持原样（接口方法 ID / 候选集合），不制造具体实现。

import (
	"go/ast"
	"go/token"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// seqIdentityBeforeDepth 在 depth 上限处做"仅身份"的消歧；返回 true 表示
// 已写入选定目标（node.Evidence/node.ImplType）。
func seqIdentityBeforeDepth(a *Actions, req CodeSequenceRequest, f *ast.File, fset *token.FileSet,
	fun ast.Expr, tid string, node *CodeSeqNode, recvType, recvName string,
	recvDecl map[string]string, confidence float64, curFile string, pos token.Position) bool {
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	// 0) 场景守卫（接口方法调用的到达定义 + 条件分支）
	if strings.Contains(tid, ":(") {
		if recv, isID := sel.X.(*ast.Ident); isID {
			impl, reason, cands, guard, ok := a.sceneImplChoice(req, fset, pos.Offset, f, recv.Obj,
				recv.Name, sel.Sel.Name, pkgOfID(domain.CanonicalID(tid)))
			if ok && guard {
				node.ImplType = implTypeShort(impl)
				ev := seqIfaceEvidence(curFile, pos.Line, impl, cands, confidence)
				ev.Reason = reason
				node.Evidence = ev
				return true
			}
			if ok && !guard {
				ev := seqIfaceEvidence(curFile, pos.Line, "", cands, confidence)
				ev.Reason = "reaching-guard-unknown"
				ev.Ambiguous = true
				ev.CandidateBudget = seqIfaceBudget(len(cands), false)
				node.Evidence = ev
				return true
			}
		}
	}
	// 1) receiver 字段数据流（工厂/构造器返回实例的字段方法调用）
	unknownField := ""
	if recvType != "" {
		if field, ok2 := receiverFieldSel(sel, req.curRecvObj, recvName); ok2 {
			impls, unknownWriters := a.receiverFieldImplsEx(req, recvType, field, sel.Sel.Name)
			if len(impls) == 1 && a.isIfaceMethodID(impls[0]) {
				impls = nil
			}
			if unknownWriters {
				impls = nil
			}
			if len(impls) == 0 && (a.isIfaceMethodID(tid) || seqDeclaredTypeIsIface(a, req, recvDecl[field])) {
				unknownField = field
			}
			if len(impls) > 1 {
				node.Evidence = &SeqEvidence{Source: SeqEvidenceFieldFlow, CallFile: curFile,
					CallLine: pos.Line, Confidence: confidence, Reason: "receiver-field-dataflow",
					Candidates: impls, Ambiguous: true, CandidateBudget: seqIfaceBudget(len(impls), false)}
				return true
			}
			if len(impls) == 1 {
				impl := impls[0]
				node.ImplType = implTypeShort(impl)
				node.DeclType = recvDecl[field]
				node.Evidence = &SeqEvidence{Source: SeqEvidenceFieldFlow, CallFile: curFile,
					CallLine: pos.Line, SymbolID: impl, Confidence: confidence,
					Reason: "receiver-field-dataflow"}
				return true
			}
		}
	}
	// 2) DI 注入字段
	if recvType != "" {
		if field, ok2 := receiverFieldSel(sel, req.curRecvObj, recvName); ok2 {
			if di, ok3 := a.diFieldImpl(req, f, recvType, field, sel.Sel.Name); ok3 && di.why == "" {
				node.ImplType = implTypeShort(di.symID)
				node.DeclType = recvDecl[field]
				node.Evidence = &SeqEvidence{Source: SeqEvidenceDI, CallFile: curFile, CallLine: pos.Line,
					SymbolID: di.symID, Confidence: confidence, Reason: di.reason,
					Candidates: []string{di.symID}, RegisterSite: di.reg.line, RegisterFile: di.reg.file}
				return true
			}
		}
	}
	// 3) 局部变量数据流（构造器/字面量初始化）
	if id, isID := sel.X.(*ast.Ident); isID {
		if impl := a.localVarImplObj(req, f, pkgPathOf(req.curSym), id.Name, id.Obj, sel.Sel.Name); impl != "" {
			node.ImplType = implTypeShort(impl)
			node.Evidence = &SeqEvidence{Source: SeqEvidenceLocalVar, CallFile: curFile,
				CallLine: pos.Line, SymbolID: impl, Confidence: confidence,
				Reason: "localvar-init-dataflow"}
			return true
		}
	}
	// U06：字段既无写入、也无 DI/局部变量证明——实例来源未知（depth=1 与
	// 展开路径同口径，唯一实现不接成确定调用）。
	if unknownField != "" {
		markSeqUnknownReceiver(node, curFile, pos.Line, confidence, recvType, unknownField)
		return true
	}
	// 4) 接口方法 → 唯一已证明实现（多候选保留候选集合并标 ambiguous）
	// U06/T07：先用与 depth>1 相同的口径验证实例来源——方法 getter
	// （`s.Get().M()`）按 receiver 字段写入证明具体化；形参/别名/未知返回
	// 值等无来源证明时停在接口边界（唯一实现不等于实际实例来源）。
	if a.isIfaceMethodID(tid) {
		if inner, isCall := sel.X.(*ast.CallExpr); isCall {
			// T12/T05：工厂来源（Build→Run→Step→Get）在 depth=1 也要解析身份
			// ——与完整展开同一口径（kind 守卫候选 / 单一具体实现）。
			if req.curRecvOrigin != "" {
				if impls, field := a.seqFactoryFieldImplsAtDepth1(req, recvType, sel.Sel.Name); len(impls) > 0 {
					if len(impls) > 1 {
						node.Evidence = &SeqEvidence{Source: SeqEvidenceFieldFlow, CallFile: curFile,
							CallLine: pos.Line, Candidates: impls, Ambiguous: true,
							Reason: "factory-field-dataflow", CandidateBudget: seqIfaceBudget(len(impls), false)}
						node.Stop = &SeqStop{Code: SeqStopIfaceAmbiguous,
							Detail: "工厂字段有 " + itoaSeq(len(impls)) + " 个候选实现（未消歧，未展开）"}
						return true
					}
					node.ImplType = implTypeShort(impls[0])
					node.Evidence = &SeqEvidence{Source: SeqEvidenceFieldFlow, CallFile: curFile,
						CallLine: pos.Line, SymbolID: impls[0], Confidence: confidence,
						Reason: "factory-field-dataflow"}
					_ = field
					return true
				}
			}
			if impls, field, unknown, ok3 := a.seqGetterFieldImpls(req, recvType, inner, sel.Sel.Name); ok3 {
				if unknown || len(impls) == 0 {
					markSeqUnknownReceiver(node, curFile, pos.Line, confidence, recvType, field)
					return true
				}
				if len(impls) > 1 {
					node.Evidence = &SeqEvidence{Source: SeqEvidenceFieldFlow, CallFile: curFile,
						CallLine: pos.Line, Candidates: impls, Ambiguous: true,
						Reason: "receiver-field-dataflow", CandidateBudget: seqIfaceBudget(len(impls), false)}
					return true
				}
				node.ImplType = implTypeShort(impls[0])
				node.Evidence = &SeqEvidence{Source: SeqEvidenceFieldFlow, CallFile: curFile,
					CallLine: pos.Line, SymbolID: impls[0], Confidence: confidence,
					Reason: "receiver-field-dataflow"}
				return true
			}
		}
		impl, cands, ok := a.seqIfaceMethodImpl(req, tid)
		if !ok {
			return false
		}
		cands, truncated := capSeqCandidates(cands)
		if len(cands) > 1 {
			ev := seqIfaceEvidence(curFile, pos.Line, "", cands, confidence)
			ev.CandidateBudget = seqIfaceBudget(len(cands), truncated)
			node.Evidence = ev
			node.Stop = &SeqStop{Code: SeqStopIfaceAmbiguous,
				Detail: "接口方法 " + tid + " 有 " + itoaSeq(len(cands)) + " 个未验证实现候选（无消歧证据，未展开）"}
			return true
		}
		// U06/T07：唯一实现也要先有实例来源证明（与 depth>1 同口径）。
		if detail, unknown := seqReceiverInstanceUnknown(a, req, fun, recvType, recvName, recvDecl); unknown {
			markSeqUnknownInstance(node, curFile, pos.Line, confidence, detail)
			return true
		}
		node.ImplType = implTypeShort(impl)
		node.Evidence = seqIfaceEvidence(curFile, pos.Line, impl, cands, confidence)
		node.Evidence.CandidateBudget = seqIfaceBudget(len(cands), truncated)
		return true
	}
	return false
}

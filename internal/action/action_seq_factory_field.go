package action

// todo25 §3.4：工厂返回实例的字段/方法续接。
//
// 形态：`b, _ := Build(kind)` → `b.Run()` → `b.Step()` → `b.Get().Work()`。
// 证明链（保守版）：
//   - `b.Get()` 的方法体必须 **return <receiver>.<field>**（唯一样本）；
//   - b 的实例来源是构造它的工厂函数（`b` 的赋值 RHS 调用的工厂），沿
//     b.Run→b.Step 传递（`curRecvOrigin`）；
//   - 在工厂函数里找到该字段的 setter 调用，按 setter 实参类型与所在
//     switch/if 守卫生成互斥候选。
// 无法证明（无工厂来源/字段返回不唯一/setter 形态不支持）→ 返回 false，
// 保留原接口候选诊断，不借用其他实例/同名字段。

import (
	"go/ast"
	"go/token"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// seqFieldCandidate 一个工厂 setter 实参证据。
type seqFieldCandidate struct {
	pkgPath  string
	typeName string
	guard    string
}

// seqCallReceiverOrigin 调用点接收者实例的工厂来源（函数 canonical ID）。
func seqCallReceiverOrigin(a *Actions, req CodeSequenceRequest, fun ast.Expr,
	targetID string, f *ast.File, curPkg string) string {
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	recvType := seqTargetRecvType(targetID)
	if recvType == "" {
		return ""
	}
	switch x := sel.X.(type) {
	case *ast.Ident:
		if req.curRecvObj != nil && x.Obj == req.curRecvObj && req.curRecvOrigin != "" {
			return req.curRecvOrigin
		}
		return seqVarFactoryOrigin(a, req, f, curPkg, x, recvType)
	case *ast.CallExpr:
		if tid, ok := seqASTCallTarget(x.Fun, f, curPkg); ok {
			return tid
		}
	}
	return ""
}

// seqVarFactoryOrigin 局部变量 b 的赋值 RHS 为工厂调用且返回类型匹配。
func seqVarFactoryOrigin(a *Actions, req CodeSequenceRequest, f *ast.File,
	curPkg string, id *ast.Ident, recvType string) string {
	if id.Obj == nil || req.curBody == nil {
		return ""
	}
	imports := importAliases(f)
	for _, st := range req.curBody.List {
		as, ok := st.(*ast.AssignStmt)
		if !ok {
			continue
		}
		for i, lhs := range as.Lhs {
			lid, ok := lhs.(*ast.Ident)
			if !ok || lid.Obj != id.Obj || i >= len(as.Rhs) {
				continue
			}
			call, ok := as.Rhs[i].(*ast.CallExpr)
			if !ok {
				continue
			}
			types := callReturnTypes(a, req, f, imports, call.Fun, 3, curPkg)
			if len(types) == 0 || types[0].typeName != recvType {
				continue
			}
			if tid, ok := seqASTCallTarget(call.Fun, f, curPkg); ok {
				return tid
			}
		}
	}
	return ""
}

// seqExpandFactoryFieldCall `x.Get().Work()` → 工厂字段来源的具体实现。
// 已处理返回 true（无论单候选/多候选）；无法证明返回 false。
func seqExpandFactoryFieldCall(a *Actions, req CodeSequenceRequest, node *CodeSeqNode,
	fun ast.Expr, f *ast.File, curPkg string, depth int, curFile string, pos token.Position) bool {
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	inner, ok := sel.X.(*ast.CallExpr)
	if !ok || len(inner.Args) > 0 {
		return false
	}
	recvType, ok := seqCallSiteReceiverType(inner.Fun, req.curRecvType, req.curRecvName, req.curRecvDecl)
	if (!ok || recvType == "") && req.curCtx != nil && req.curCtx.file != nil {
		// T04：`NewBoxA().Get().Work()` —— getter 的 receiver 本身是工厂调用时，
		// 类型取构造器返回（唯一具体类型），字段写入证明按该类型查。
		if innerSel0, isSel := inner.Fun.(*ast.SelectorExpr); isSel {
			if c, isCall := innerSel0.X.(*ast.CallExpr); isCall {
				srcs := rhsTypes(a, req, req.curCtx.file, importAliases(req.curCtx.file), c, 3, curPkg)
				known := knownWriteSources(srcs)
				if len(known) == 1 && !hasUnknownWriteSource(srcs) {
					recvType, ok = known[0].typeName, true
				}
			}
		}
	}
	if !ok || recvType == "" {
		return false
	}
	innerSel, ok := inner.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	// todo25 §3.4：getter 可能是嵌入提升方法（Box 自身没有 Get）——沿嵌入
	// 链找声明类型；多条提升路径（>1）保守放弃，不挑第一个。
	methodOwners := seqMethodOwnerTypes(a, req, req.curFile, recvType, innerSel.Sel.Name)
	if len(methodOwners) != 1 {
		return false
	}
	getID := "symbol:go:" + pkgOfID(domain.CanonicalID(req.curSym)) + ":(" + methodOwners[0] + ")." + innerSel.Sel.Name
	field, ok := seqMethodReturnField(a, req, getID, methodOwners[0])
	if !ok || field == "" {
		return false
	}
	origin := req.curRecvOrigin
	if origin == "" {
		// T05：当前 receiver 携带**顺序敏感**的字段到达定义（局部实例经方法
		// 调用传递）——调用前覆写取最后一次写入；调用后的写入不倒推。
		if id, isID := innerSel.X.(*ast.Ident); isID && req.curRecvFields != nil &&
			(id.Obj == req.curRecvObj || id.Obj == nil) {
			if st, okF := req.curRecvFields[field]; okF {
				if st.unknown || len(st.types) == 0 {
					markSeqUnknownReceiver(node, curFile, pos.Line, 0.8, recvType, field)
					return true
				}
				var impls []string
				for _, c := range st.types {
					impls = appendUniqueStr(impls, "symbol:go:"+c.pkgPath+":("+c.typeName+")."+sel.Sel.Name)
				}
				return seqExpandFieldImpls(a, req, node, impls, depth, curFile, pos)
			}
		}
		// U06：没有工厂来源时仍可用 receiver 自身字段的写入证明
		// （`s.Get()` 的方法体 return `s.<field>`）：无写入或未知写者 → 实例
		// 来源未知，不借全库唯一实现。
		impls, unknown := a.receiverFieldImplsEx(req, recvType, field, sel.Sel.Name)
		if unknown || len(impls) == 0 {
			markSeqUnknownReceiver(node, curFile, pos.Line, 0.8, recvType, field)
			return true
		}
		return seqExpandFieldImpls(a, req, node, impls, depth, curFile, pos)
	}
	cands, ok := seqFactoryFieldCandidates(a, req, origin, recvType, field, curPkg)
	if !ok || len(cands) == 0 {
		// T05/U06：getter 返回的字段在工厂里没有可证明的写入来源（写别的
		// 对象/未知方法改写/无 setter）——实例来源未知，保留接口调用与
		// 未验证边界，不回退到"唯一实现候选"。
		markSeqUnknownReceiver(node, curFile, pos.Line, 0.8, recvType, field)
		return true
	}
	method := sel.Sel.Name
	// T05：只有**具体类型**能作为工厂字段候选——setter 实参本身是接口
	// （`Set(NewPick())` 返回 API）时无法证明实际实例，不能把接口方法当
	// 具体实现展开。
	concrete := make([]seqFieldCandidate, 0, len(cands))
	impls := make([]string, 0, len(cands))
	for _, c := range cands {
		impl := "symbol:go:" + c.pkgPath + ":(" + c.typeName + ")." + method
		if a.isIfaceMethodID(impl) {
			continue
		}
		concrete = append(concrete, c)
		impls = append(impls, impl)
	}
	if len(concrete) == 0 {
		return false
	}
	cands = concrete
	ev := &SeqEvidence{Source: SeqEvidenceFieldFlow, CallFile: curFile, CallLine: pos.Line,
		Reason: "factory-field-dataflow", Candidates: impls, Ambiguous: len(cands) > 1,
		CandidateBudget: seqIfaceBudget(len(cands), false)}
	node.Evidence = ev
	node.Stop = nil
	if depth <= 1 {
		req.acc.depth = true
		node.Stop = &SeqStop{Code: SeqStopDepthLimit,
			Detail: "depth 上限（--depth " + itoaSeq(req.Depth) + "），未展开工厂字段实现"}
		return true
	}
	for i, c := range cands {
		impl := impls[i]
		if _, err := a.repo.GetSymbol(domain.CanonicalID(impl)); err != nil {
			continue
		}
		// T05/U07：候选实现调用要有稳定参与者（receiver 具体类型）——否则
		// 渲染出空发送方（`->>P6`），证据也缺左端。
		child := &CodeSeqNode{Kind: "call", Label: method, Actor: c.typeName,
			Type: implTypeShort(impl), Line: node.Line,
			Evidence: &SeqEvidence{Source: SeqEvidenceFieldFlow, CallFile: curFile,
				CallLine: pos.Line, SymbolID: impl, Confidence: 1.0,
				Reason: "factory-field-dataflow", Verdict: SeqVerdictCorroborated}}
		if child2, stop := codeSeqForSymbol(a, req, impl, depth-1); child2 != nil {
			child.Nodes, child.Stop = child2.Nodes, child2.Stop
		} else if stop != nil {
			child.Stop = stop
		}
		br := &CodeSeqNode{Kind: "branch", Label: c.guard, Line: node.Line,
			Nodes: []*CodeSeqNode{child}}
		node.Nodes = append(node.Nodes, br)
	}
	if len(node.Nodes) == 0 {
		node.Stop = &SeqStop{Code: SeqStopUnknownImpl,
			Detail: "工厂字段候选在索引中不存在（" + strings.Join(impls, ", ") + "）"}
	}
	return true
}

// seqGetterFieldImpls `x.Get().M()` → receiver 字段（`s.Get()` 返回
// `s.<field>`）的写入来源实现候选。ok=false 表示不是"receiver 方法 getter"
// 形态（交给其它路径处理）。
//
// U06：depth=1 的身份解析要用同一口径——字段无写入/未知写者时 impls 为空，
// 调用方必须标 unknown_receiver，而不是回退到"全库唯一实现"。
func (a *Actions) seqGetterFieldImpls(req CodeSequenceRequest, recvType string,
	inner *ast.CallExpr, method string) (impls []string, field string, unknown bool, ok bool) {
	if inner == nil || len(inner.Args) > 0 || recvType == "" {
		return nil, "", false, false
	}
	innerSel, isSel := inner.Fun.(*ast.SelectorExpr)
	if !isSel {
		return nil, "", false, false
	}
	owners := seqMethodOwnerTypes(a, req, req.curFile, recvType, innerSel.Sel.Name)
	if len(owners) != 1 {
		return nil, "", false, false
	}
	getID := "symbol:go:" + pkgOfID(domain.CanonicalID(req.curSym)) + ":(" + owners[0] + ")." + innerSel.Sel.Name
	f, okF := seqMethodReturnField(a, req, getID, owners[0])
	if !okF || f == "" {
		return nil, "", false, false
	}
	impls, unknown = a.receiverFieldImplsEx(req, recvType, f, method)
	return impls, f, unknown, true
}

// seqExpandFactoryCallReceiver 接收者是工厂调用结果（`FromField(k).M()`）：
// 先按工厂 return 的守卫裁剪具体来源（T04），命中即已展开/已标未知。
func seqExpandFactoryCallReceiver(a *Actions, req CodeSequenceRequest, node *CodeSeqNode,
	fun ast.Expr, f *ast.File, curPkg string, depth int, curFile string, pos token.Position) bool {
	sel, isSel := fun.(*ast.SelectorExpr)
	if !isSel {
		return false
	}
	if _, isCall := sel.X.(*ast.CallExpr); !isCall {
		return false
	}
	return seqExpandGetterIfaceCall(a, req, node, fun, f, curPkg, depth, curFile, pos)
}

// seqExpandFieldImpls 字段来源实现候选 → 展开/歧义/未知（单实现也要求具体
// 类型；接口方法 ID 不是实例来源）。返回 true 表示已处理节点。
func seqExpandFieldImpls(a *Actions, req CodeSequenceRequest, node *CodeSeqNode,
	impls []string, depth int, curFile string, pos token.Position) bool {
	concrete := make([]string, 0, len(impls))
	for _, impl := range impls {
		if a.isIfaceMethodID(impl) {
			continue
		}
		concrete = appendUniqueStr(concrete, impl)
	}
	if len(concrete) == 0 {
		markSeqUnknownReceiver(node, curFile, pos.Line, 0.8, "字段", "实例来源未知")
		return true
	}
	if len(concrete) > 1 {
		node.Evidence = &SeqEvidence{Source: SeqEvidenceFieldFlow, CallFile: curFile,
			CallLine: pos.Line, Candidates: concrete, Ambiguous: true,
			Reason: "receiver-field-dataflow", CandidateBudget: seqIfaceBudget(len(concrete), false)}
		node.Stop = &SeqStop{Code: SeqStopIfaceAmbiguous,
			Detail: "字段实现来源有 " + itoaSeq(len(concrete)) + " 个候选（未消歧，未展开）"}
		seqExpandCandidateBranches(a, req, node, concrete, depth, pos)
		return true
	}
	impl := concrete[0]
	node.ImplType = implTypeShort(impl)
	node.Evidence = &SeqEvidence{Source: SeqEvidenceFieldFlow, CallFile: curFile,
		CallLine: pos.Line, SymbolID: impl, Confidence: 0.8,
		Reason: "receiver-field-dataflow"}
	seqExpandInto(a, req, node, impl, depth)
	return true
}

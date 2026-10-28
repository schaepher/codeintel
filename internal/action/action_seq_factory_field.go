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
		return false
	}
	cands, ok := seqFactoryFieldCandidates(a, req, origin, recvType, field, curPkg)
	if !ok || len(cands) == 0 {
		return false
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

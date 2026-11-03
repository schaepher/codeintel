package action

// U06/T07：接口调用前的**实例来源**校验（从只判断"字段有无写入"扩展到
// 接收者表达式本身：形参、局部别名、未知返回值、包级变量）。
//
// 规则：唯一实现不等于实际实例来源——接口派发（`InterfaceMethodImpl`）只能
// 在已经证明"这个变量持有哪个具体实例"时使用；否则保留接口身份 + 明确
// unknown_receiver 诊断，不展开任何实现。

import (
	"go/ast"
	"go/token"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// markSeqUnknownInstance 标记"实例来源未证明"的接口调用（保留接口边界）。
func markSeqUnknownInstance(node *CodeSeqNode, curFile string, line int, confidence float64, detail string) {
	node.Evidence = &SeqEvidence{Source: SeqEvidenceFieldFlow, CallFile: curFile,
		CallLine: line, Confidence: confidence, Reason: "unknown_receiver", Ambiguous: true}
	node.Stop = &SeqStop{Code: SeqStopUnknownReceiver, Detail: detail}
}

// seqReceiverInstanceUnknown 判断接口调用的接收者表达式是否缺少实例来源
// 证明。返回 (detail, true) 表示必须停在接口边界。
//
// 调用时机：所有正向证明路径（场景守卫 / receiver 字段 / DI / 局部变量 /
// getter / 工厂字段 / RPC）都已尝试且未命中。
func seqReceiverInstanceUnknown(a *Actions, req CodeSequenceRequest, fun ast.Expr,
	recvType, recvName string, recvDecl map[string]string) (string, bool) {
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	switch x := sel.X.(type) {
	case *ast.Ident:
		return seqIdentInstanceUnknown(a, req, x, recvType, recvName)
	case *ast.CallExpr:
		// getter / 工厂字段 / 类型构造器路径都已尝试仍未消歧。
		return "调用返回值（" + seqExprBrief(x) + "）的实例来源未证明（唯一实现不等于实际实例来源，未展开）", true
	case *ast.TypeAssertExpr:
		// `v.(*Impl).M()`：断言目标类型已写明（不是"唯一实现"推测）。
		return "", false
	}
	return "", false
}

// seqIdentInstanceUnknown 接收者是标识符时的来源判定。
func seqIdentInstanceUnknown(a *Actions, req CodeSequenceRequest, id *ast.Ident,
	recvType, recvName string) (string, bool) {
	if id == nil {
		return "", false
	}
	// 当前方法的 receiver（s.Method）：receiver 类型已知时来源成立。
	if recvType != "" && (id.Obj == req.curRecvObj ||
		(req.curRecvObj == nil && recvName != "" && id.Name == recvName)) {
		if a.isIfaceTypeName(req, recvType) {
			return "接收者 " + id.Name + " 声明为接口 " + recvType + "（动态入口无实例来源）", true
		}
		return "", false
	}
	if seqIdentIsParam(req, id) {
		if seqParamInstanceProven(a, req, id) {
			return "", false
		}
		return "形参 " + id.Name + " 的实例来源未证明（唯一实现不等于实际实例来源，未展开）", true
	}
	if seqIdentAliasOfParam(req, id, 4) {
		if seqParamInstanceProven(a, req, id) {
			return "", false
		}
		return "局部变量 " + id.Name + " 是形参的别名（实例来源未证明，未展开）", true
	}
	// 其余形态（包级变量 / 无初始化局部变量 / 无法解析的标识符）都缺少
	// 到达定义证明——不借唯一实现。
	return "标识符 " + id.Name + " 的实例来源未证明（唯一实现不等于实际实例来源，未展开）", true
}

// isIfaceTypeName 类型名是否已索引为接口（同包）。
func (a *Actions) isIfaceTypeName(req CodeSequenceRequest, typeName string) bool {
	name := strings.TrimPrefix(strings.TrimSpace(typeName), "*")
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	if name == "" || a == nil || a.repo == nil {
		return false
	}
	n, err := a.repo.GetSymbol(domain.CanonicalID("symbol:go:" + pkgOfID(domain.CanonicalID(req.curSym)) + ":" + name))
	if err != nil || n == nil {
		return false
	}
	return n.Kind == domain.KindInterface
}

// seqIdentIsParam 标识符是否是当前函数声明的形参（按 AST 对象身份，名字兜底）。
func seqIdentIsParam(req CodeSequenceRequest, id *ast.Ident) bool {
	if id == nil || req.curParams == nil {
		return false
	}
	for _, fl := range req.curParams.List {
		for _, nm := range fl.Names {
			if nm.Obj != nil && id.Obj != nil {
				if nm.Obj == id.Obj {
					return true
				}
				continue
			}
			if nm.Name == id.Name {
				return true
			}
		}
	}
	return false
}

// seqIdentAliasOfParam 标识符是否经由赋值链（`x := v` / `var x = v`）来自
// 形参。depth 限制递归（防自引用环）。
func seqIdentAliasOfParam(req CodeSequenceRequest, id *ast.Ident, depth int) bool {
	if id == nil || req.curBody == nil || depth <= 0 {
		return false
	}
	found := false
	matchLHS := func(lhs ast.Expr) bool {
		lid, ok := lhs.(*ast.Ident)
		if !ok {
			return false
		}
		if lid.Obj != nil && id.Obj != nil {
			return lid.Obj == id.Obj
		}
		return lid.Name == id.Name
	}
	fromParam := func(rhs ast.Expr) bool {
		rid, ok := rhs.(*ast.Ident)
		if !ok {
			return false
		}
		return seqIdentIsParam(req, rid) || seqIdentAliasOfParam(req, rid, depth-1)
	}
	ast.Inspect(req.curBody, func(n ast.Node) bool {
		if found {
			return false
		}
		switch s := n.(type) {
		case *ast.AssignStmt:
			if len(s.Lhs) != len(s.Rhs) {
				return true
			}
			for i, lhs := range s.Lhs {
				if matchLHS(lhs) && fromParam(s.Rhs[i]) {
					found = true
					return false
				}
			}
		case *ast.ValueSpec:
			if len(s.Names) != len(s.Values) {
				return true
			}
			for i, nm := range s.Names {
				if matchLHS(nm) && fromParam(s.Values[i]) {
					found = true
					return false
				}
			}
		}
		return true
	})
	return found
}

// seqExprBrief 表达式的最简源码文本（诊断用；不追求完整还原）。
func seqExprBrief(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.CallExpr:
		return seqExprBrief(x.Fun) + "(...)"
	case *ast.SelectorExpr:
		return seqExprBrief(x.X) + "." + x.Sel.Name
	case *ast.Ident:
		return x.Name
	}
	return "…"
}

// codeSeqIfaceEntryRoot 接口方法作为查询入口（无方法体）→ 以接口身份为根，
// 实现方法作为**未验证候选**子树（U06/T07：接口根入口不任选实现）。
func (a *Actions) codeSeqIfaceEntryRoot(req CodeSequenceRequest, n *domain.CodeEntity) (*CodeSeqNode, *SeqStop) {
	ifaceID := string(n.ID)
	_, method := seqIfaceAndMethod(ifaceID)
	if method == "" {
		method = ifaceID
	}
	root := &CodeSeqNode{Kind: "call", Label: method, Line: n.LineStart,
		Evidence: &SeqEvidence{Source: SeqEvidenceFieldFlow, SymbolID: ifaceID,
			File: n.FilePath, Line: n.LineStart, Reason: "interface-entry"}}
	_, cands, ok := a.seqIfaceMethodImpl(req, ifaceID)
	if !ok || len(cands) == 0 {
		// 不是"接口方法 + 可证明实现"的动态入口（普通无方法体节点/未实现
		// 接口）——保持原有 nil 语义，由调用方回退索引调用链。
		return nil, nil
	}
	expandCands, truncated := capSeqCandidates(cands)
	// 接口入口没有实例来源证明：派发本身是 unverified，实现只能是候选
	// 子树（后代不计确定执行、不参与自动续查）。
	ev := &SeqEvidence{Source: SeqEvidenceFieldFlow, SymbolID: ifaceID,
		Reason: "interface-entry", Ambiguous: true, Candidates: cands,
		CandidateBudget: seqIfaceExpandBudget(len(cands), len(expandCands), truncated),
		Verdict:         SeqVerdictUnverified}
	if n.LineStart > 0 {
		ev.CallFile, ev.CallLine = n.FilePath, n.LineStart
	}
	dispatch := &CodeSeqNode{Kind: "call", Label: method, Actor: method,
		Line: n.LineStart, Candidate: true, Evidence: ev,
		Stop: &SeqStop{Code: SeqStopIfaceAmbiguous,
			Detail: "接口方法入口（无实例来源证明）；实现仅作未验证候选，不计确定执行、不参与续查"}}
	seqExpandCandidateBranches(a, req, dispatch, expandCands, req.Depth,
		token.Position{Line: n.LineStart}, len(expandCands) < len(ev.Candidates), len(ev.Candidates))
	root.Nodes = []*CodeSeqNode{dispatch}
	return root, nil
}

// seqParamInstanceProven 形参的实例来源证明：当前函数**全部已索引调用点**在
// 对应实参位置都给出同一个具体类型来源（字面量/构造器）。跨函数形参绑定
// 是真实来源（如 `Entry(){Run(&Store{})}`），此时可以继续用接口实现候选；
// 无调用点/调用点未知/多来源 → 未证明。

// seqReceiverCallUnproven 接收者是**调用结果**（`f(...).M()`）时，被调函数
// 的声明返回类型必须是已索引的具体类型；返回接口 → 实例来源未证明（T04：
// 不能凭"调用边已指向某个具体方法"就把接口返回值当确定实例）。
// 类型未在本仓索引（标准库/外部）→ 不在此判定，保持原行为。

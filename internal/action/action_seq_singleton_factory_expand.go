package action

// U07：单例 getter 的条件工厂候选分支展开。

import (
	"go/ast"
	"go/token"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// seqExpandSingletonGetterCall 单例 getter（`region.Get().Work()`）→ 条件
// 工厂候选分支（保留原调用参与者与工厂守卫）。返回 true = 已处理。
func seqExpandSingletonGetterCall(a *Actions, req CodeSequenceRequest, node *CodeSeqNode,
	fun ast.Expr, f *ast.File, curPkg string, depth int, curFile string, pos token.Position) bool {
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	inner, ok := sel.X.(*ast.CallExpr)
	if !ok || len(inner.Args) > 0 {
		return false
	}
	cands, factoryID, ok := seqSingletonCandidates(a, req, f, inner, curPkg)
	if !ok || len(cands) == 0 {
		return false
	}
	method := sel.Sel.Name
	node.Evidence = &SeqEvidence{Source: SeqEvidenceFieldFlow, CallFile: curFile, CallLine: pos.Line}
	var branches []*CodeSeqNode
	for _, c := range cands {
		impl := "symbol:go:" + c.pkgPath + ":(" + c.typeName + ")." + method
		if _, err := a.repo.GetSymbol(domain.CanonicalID(impl)); err != nil {
			continue
		}
		label := "factory " + factoryID
		if c.guard != "" {
			label += ": " + c.guard
		}
		child := &CodeSeqNode{Kind: "call", Label: node.Label, Actor: c.typeName,
			Type: implTypeShort(impl), Line: node.Line,
			Evidence: &SeqEvidence{Source: SeqEvidenceFieldFlow, CallFile: curFile,
				CallLine: pos.Line, SymbolID: impl, Confidence: 1.0,
				Reason: "singleton-factory-switch-field", Verdict: SeqVerdictCorroborated}}
		if c.guard != "" {
			child.Exec = ""
		}
		if child2, stop := codeSeqForSymbol(a, req, impl, depth-1); child2 != nil {
			child.Nodes, child.Stop = child2.Nodes, child2.Stop
		} else if stop != nil {
			child.Stop = stop
		}
		branches = append(branches, &CodeSeqNode{Kind: "branch", Label: label,
			Line: pos.Line, Nodes: []*CodeSeqNode{child}})
		if len(node.Nodes) > 0 {
			// 只在第一个分支后串联（互斥候选）。
			node.Nodes[len(node.Nodes)-1].Else = branches[len(branches)-1:]
		}
	}
	if len(branches) == 0 {
		return false
	}
	_ = depth
	node.Nodes = nil
	// 互斥候选：依次串联（保持渲染为嵌套 alt，顺序稳定）。
	for i := len(branches) - 1; i >= 0; i-- {
		branches[i].Else = nil
		if i+1 < len(branches) {
			branches[i].Else = []*CodeSeqNode{branches[i+1]}
		}
	}
	node.Nodes = []*CodeSeqNode{branches[0]}
	return true
}

// seqIsPkgIdent 标识符是否为文件导入的包名（parser 不一定给 Obj=ast.Pkg）。
func seqIsPkgIdent(f *ast.File, id *ast.Ident) bool {
	if f == nil || id == nil {
		return false
	}
	if id.Obj != nil && id.Obj.Kind == ast.Pkg {
		return true
	}
	return seqImportPath(f, id.Name) != ""
}

// seqResolveGuardLocalTag switch 的 tag 常是局部变量（`k := region();
// switch k {...}`）——守卫文本用其初始化表达式（`region() == "ID"`），与
// 工厂守卫的语义一致。
func seqResolveGuardLocalTag(guard string, fn *ast.FuncDecl) string {
	if guard == "" || fn == nil || fn.Body == nil {
		return guard
	}
	subst := map[string]string{}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
			return true
		}
		id, ok := as.Lhs[0].(*ast.Ident)
		if !ok || id.Name == "" {
			return true
		}
		if _, ok := as.Rhs[0].(*ast.CallExpr); !ok {
			return true
		}
		if _, seen := subst[id.Name]; !seen {
			subst[id.Name] = seqNodeText(as.Rhs[0])
		}
		return true
	})
	for name, expr := range subst {
		guard = strings.ReplaceAll(guard, name+" == ", expr+" == ")
		guard = strings.ReplaceAll(guard, name+" != ", expr+" != ")
	}
	return guard
}

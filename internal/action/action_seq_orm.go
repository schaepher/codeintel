package action

// U04f：XORM 终端操作与显式构建器证据（evidence.orm）。
//
// 只按**准确包/方法身份**识别（Session 的 Insert/Update/Delete/Find/Get），
// 不凭方法短名；显式 Table/Where 链逐调用核对 owner。模型表达式不是字段/
// 列映射，不能据此证明生成 ID、资源、写入成功或跨入口因果。

import (
	"go/ast"
	"strings"

	"vitess.io/vitess/go/vt/sqlparser"
)

// seqXormTerminal 节点证据是否为准确的 XORM Session 终端操作。
func seqXormTerminal(node *CodeSeqNode) (pkg, method, op string, ok bool) {
	if node == nil || node.Evidence == nil {
		return "", "", "", false
	}
	id := node.Evidence.SymbolID
	i := strings.Index(id, ":(")
	j := strings.LastIndex(id, ").")
	if i < 0 || j < 0 || j <= i || !strings.HasPrefix(id, "symbol:go:") {
		return "", "", "", false
	}
	if id[i+2:j] != "Session" {
		return "", "", "", false
	}
	pkg = id[len("symbol:go:"):i]
	if !seqXormPkgPaths[pkg] {
		return "", "", "", false
	}
	method = id[j+2:]
	op = seqXormOps[method]
	if op == "" {
		return "", "", "", false
	}
	return pkg, method, op, true
}

// seqXormChain 终端调用的显式构建器链（从内向外收集 Table/Where 等）。
type seqXormChain struct {
	table        string
	tableExpr    string
	unknown      bool
	whereCalls   []*ast.CallExpr
	whereOffsets []int
}

// seqXormCollectChain 遍历接收者链（不越过非本链的调用/字段）。
func seqXormCollectChain(call *ast.CallExpr, c *seqPersistCtx) *seqXormChain {
	out := &seqXormChain{}
	cur := call
	first := true
	for {
		sel, ok := cur.Fun.(*ast.SelectorExpr)
		if !ok {
			return out
		}
		name := sel.Sel.Name
		if !first && !seqXormBuilders[name] {
			// 链上出现未证明的构建器（Rename 等）可能改变表/条件语义。
			out.unknown = true
		}
		first = false
		if name != "Table" && name != "Where" && name != "And" && name != "Or" &&
			name != "Context" && name != "Alias" && name != "In" {
			// 终端/未知方法：继续沿接收者（终端自身是 call，这里到达者是 receiver）。
			if inner, ok2 := sel.X.(*ast.CallExpr); ok2 {
				cur = inner
				continue
			}
			break
		}
		switch name {
		case "Table":
			if out.table != "" || out.tableExpr != "" {
				out.unknown = true
			}
			if len(cur.Args) == 1 {
				if text, ok := seqXormStaticString(cur.Args[0], c); ok && seqXormSimpleIdent(text) {
					out.table = text
				} else {
					out.tableExpr = seqExprTextOf(c, cur.Args[0])
				}
			} else {
				out.unknown = true
			}
		case "Where", "And", "Or":
			if name != "Where" {
				// And/Or 会放宽条件：等式子集不再是完整 AND 语义 → 表列仍可读，
				// 但覆盖率只标 partial（不猜等价条件）。
				out.unknown = true
			}
			out.whereCalls = append(out.whereCalls, cur)
		case "Alias", "In":
			out.unknown = true
		}
		inner, ok2 := sel.X.(*ast.CallExpr)
		if !ok2 {
			// 链底：必须是已证明的 *xorm.Session 句柄（外来 builder 不算）。
			if !seqXormSessionExpr(sel.X, c) {
				out.unknown = true
			}
			break
		}
		cur = inner
	}
	return out
}

// seqXormSessionExpr 表达式是否为已证明的 *xorm.Session（声明类型 + 导入路径）。
func seqXormSessionExpr(e ast.Expr, c *seqPersistCtx) bool {
	id, ok := e.(*ast.Ident)
	if !ok || c == nil || c.file == nil {
		return false
	}
	// 跨文件包级句柄：单文件 parser 不解析其它文件的 var（Obj == nil）。
	if id.Obj == nil {
		return seqPkgLevelXormSession(c, id.Name)
	}
	var typ ast.Expr
	switch d := id.Obj.Decl.(type) {
	case *ast.ValueSpec:
		typ = d.Type
	case *ast.Field:
		typ = d.Type
	}
	if typ == nil {
		return false
	}
	if star, ok := typ.(*ast.StarExpr); ok {
		typ = star.X
	}
	sel, ok := typ.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Session" {
		return false
	}
	pkgID, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return seqXormPkgPaths[seqImportPath(c.file, pkgID.Name)]
}

// seqXormStaticString 字面量/已证明静态字符串（拼接折叠）。
func seqXormStaticString(e ast.Expr, c *seqPersistCtx) (string, bool) {
	if lit, ok := e.(*ast.BasicLit); ok {
		if v := seqPersistenceSQLText(&ast.CallExpr{Args: []ast.Expr{lit}}, 0); v != "" {
			return v, true
		}
		return "", false
	}
	if c == nil || c.originOf == nil {
		return "", false
	}
	v := seqFoldSQLText(c.originOf(e))
	return v, v != ""
}

// seqXormSimpleIdent 表名形态（拒绝表达式/带引号/空）。
func seqXormSimpleIdent(s string) bool {
	if s == "" || strings.ContainsAny(s, " \t\n()'\"`;,") {
		return false
	}
	return true
}

func seqExprTextOf(c *seqPersistCtx, e ast.Expr) string {
	if c == nil {
		return ""
	}
	return c.exprText(e)
}

// seqXormEvidence 已证明的 XORM 终端调用 → 结构化证据（含 Where 谓词子集）。
func seqXormEvidence(node *CodeSeqNode, call *ast.CallExpr, c *seqPersistCtx) *SeqORM {
	_, _, op, ok := seqXormTerminal(node)
	if !ok || call == nil {
		return nil
	}
	orm := &SeqORM{Framework: "xorm", Operation: op, Outcome: "unproven"}
	chain := seqXormCollectChain(call, c)
	// B03e.4：链上没有显式 Table 时，按实际模型的静态 TableName() 求表名
	// （模型变体/动态命名/未知链修饰器都不借用）。
	orm.Models = seqORMModelsFor(call, c)
	if chain.table != "" && !chain.unknown {
		orm.Table = chain.table
	} else if chain.table == "" && chain.tableExpr == "" && !chain.unknown {
		if table, ok := seqORMStaticModelTable(c, orm.Models); ok {
			orm.Table = table
		}
	}
	if orm.Table == "" {
		if chain.tableExpr != "" {
			orm.TableExpression = chain.tableExpr
		}
		orm.Boundary = SeqORMTableUnproven
	}
	// 终端实参（表达式 + 已有标量来源）。
	for i, arg := range call.Args {
		v := SeqStorageValue{Kind: SeqStorageValueArgument, Argument: i,
			Expression: seqExprTextOf(c, arg)}
		if c != nil && i < len(c.argOrigins) {
			v.Origin = cloneSeqScalarOrigin(c.argOrigins[i])
		}
		orm.Arguments = append(orm.Arguments, v)
	}
	// Where 的 AND 等式子集（覆盖率 partial；不补造完整过滤规则）。
	preds, ok := seqXormWherePredicates(chain.whereCalls, c)
	if len(preds) > 0 {
		orm.Predicates = preds
	}
	if ok && !chain.unknown {
		orm.PredicateCoverage = SeqORMCoveragePartial
	} else if len(chain.whereCalls) > 0 || chain.unknown {
		orm.PredicateCoverage = SeqORMCoveragePartial
	}
	if orm.Boundary == "" {
		orm.Boundary = SeqORMStateUnproven
	}
	if c != nil && c.guard != "" {
		orm.Guard = c.guard
	}
	return orm
}

// seqXormWherePredicates 解析每个 Where 的条件（`SELECT 1 FROM t WHERE ...`），
// 参数按该 Where 调用的实参来源绑定。
func seqXormWherePredicates(calls []*ast.CallExpr, c *seqPersistCtx) ([]SeqStoragePredicate, bool) {
	var out []SeqStoragePredicate
	for _, call := range calls {
		if len(call.Args) == 0 {
			return out, false
		}
		cond, ok := seqXormStaticString(call.Args[0], c)
		if !ok || strings.TrimSpace(cond) == "" {
			return out, false
		}
		stmt, err := runtimeSQLParser.Parse("SELECT 1 FROM t WHERE " + cond)
		if err != nil {
			return out, false
		}
		sel, ok := stmt.(*sqlparser.Select)
		if !ok {
			return out, false
		}
		binder := &seqSQLBinder{args: call.Args[1:], ctx: c}
		if c != nil && c.originOf != nil {
			origins := make([]*SeqStorageOrigin, 0, len(call.Args)-1)
			for _, arg := range call.Args[1:] {
				origins = append(origins, c.originOf(arg))
			}
			binder.origins = origins
		}
		preds, code := seqStorageWhere(sel.Where, binder)
		if code != "" {
			return out, false
		}
		if binder.done() != "" {
			return out, false
		}
		out = append(out, preds...)
	}
	return out, true
}

package action

// todo10-7：副作用 AST 扫描（事务范围 + 返回结果 + 可疑错误覆盖）。
// 与 action_effects.go 拆分（行数治理/关注点分离）。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// scanEffectsAST 读本函数源码，扫事务调用、返回语句与错误变量赋值。
func (a *Actions) scanEffectsAST(repoAbs string, n *domain.CodeEntity, scene Scene, args map[string]string, budget int) effectsScanResult {
	out := effectsScanResult{
		tx:     TxScope{Kind: "none", Verdict: SeqVerdictUnverified, Detail: "本函数未观察到事务调用"},
		result: ResultEffect{Verdict: SeqVerdictUnverified, Detail: "未观察到 return（无源码或方法体缺失）"},
	}
	if n.FilePath == "" {
		out.tx.Detail = "节点无 file_path（无法扫描源码）"
		return out
	}
	src, err := os.ReadFile(filepath.Join(repoAbs, n.FilePath))
	if err != nil {
		out.tx.Detail = "源码读不到：" + n.FilePath
		return out
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, n.FilePath, src, 0)
	if err != nil {
		out.tx.Detail = "源码解析失败"
		return out
	}
	body, decl := findFuncDecl(fset, f, n.LineStart)
	if body == nil {
		out.tx.Detail = "按索引行号定位不到函数声明"
		return out
	}
	out.fset, out.src, out.fn = fset, src, decl
	out.varTypes = effectVarTypes(fset, decl, src)
	out.pkgPath = pkgPathOf(string(n.ID))
	// 1) 事务调用（Begin/Commit/Rollback/Transaction）
	var beginLine, commitLine, rollbackLine, wrapperLine int
	ast.Inspect(body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := strings.ToLower(callName(call))
		line := fset.Position(call.Lparen).Line
		switch {
		case txBeginNames[name]:
			if beginLine == 0 {
				beginLine = line
			}
		case txCommitNames[name]:
			if commitLine == 0 {
				commitLine = line
			}
		case txRollbackNames[name]:
			if rollbackLine == 0 {
				rollbackLine = line
			}
		case txWrapperNames[name]:
			if wrapperLine == 0 {
				wrapperLine = line
			}
		}
		return true
	})
	switch {
	case beginLine > 0:
		out.tx = TxScope{Kind: "begin_commit", BeginLine: beginLine, CommitLine: commitLine,
			RollbackLine: rollbackLine, Verdict: SeqVerdictCorroborated,
			Detail: "观察到 Begin/Commit 调用（回滚路径只在 Rollback 分支成立，不能推断全部写入回滚）"}
	case wrapperLine > 0:
		out.tx = TxScope{Kind: "tx_wrapper", BeginLine: wrapperLine, CommitLine: commitLine,
			RollbackLine: rollbackLine, Verdict: SeqVerdictUnverified,
			Detail: "观察到 Tx/Transaction 包装调用——回调内部边界未展开（不推断）"}
	}

	// 2) 路径枚举（todo11-4/12-7）：分支两侧各一条；return 终止当前路径
	out.paths, out.stats = a.effectPaths(fset, src, body, scene, args, budget)
	// 3) 返回结果 + 4) 可疑错误覆盖
	names := resultNamesOf(decl.Type.Results)
	out.resultNames = names
	var returns []string
	var slots []string
	retLine := 0
	errVars := map[string]bool{}
	var suspicious []string
	assigned := map[string][]int{} // 变量 → 赋值行
	ast.Inspect(body, func(node ast.Node) bool {
		switch x := node.(type) {
		case *ast.AssignStmt:
			for _, lhs := range x.Lhs {
				if id, ok := lhs.(*ast.Ident); ok && errLikeName(id.Name) {
					assigned[id.Name] = append(assigned[id.Name], fset.Position(x.Pos()).Line)
					errVars[id.Name] = true
					if isNilIdent(x.Rhs) && len(assigned[id.Name]) > 1 {
						suspicious = append(suspicious, fileLine(n.FilePath, fset.Position(x.Pos()).Line))
					}
				}
			}
		case *ast.ReturnStmt:
			if retLine == 0 {
				retLine = fset.Position(x.Pos()).Line
			}
			for _, r := range x.Results {
				returns = append(returns, strings.TrimSpace(nodeText(fset, src, r)))
				if id, ok := r.(*ast.Ident); ok && errLikeName(id.Name) {
					errVars[id.Name] = true
				}
				if id, ok := r.(*ast.Ident); ok && isNilIdent([]ast.Expr{id}) {
					// 返回 nil：若此前给错误变量赋过值 → 可能被吞
					if len(assigned) > 0 {
						suspicious = append(suspicious, fileLine(n.FilePath, fset.Position(x.Pos()).Line))
					}
				}
			}
			for i := range x.Results {
				slots = append(slots, seqResultSlot(i, len(x.Results), names))
			}
			if len(x.Results) == 0 && len(names) > 0 {
				for i := range names {
					slots = append(slots, seqResultSlot(i, len(names), names))
				}
			}
		}
		return true
	})
	var errNames []string
	for v := range errVars {
		errNames = append(errNames, v)
	}
	sortStrings(errNames)
	out.result = ResultEffect{Returns: returns, Slots: slots, Line: retLine,
		ErrorVars: errNames, Suspicious: dedupStrings(suspicious)}
	if retLine > 0 {
		out.result.Verdict = SeqVerdictCorroborated
		out.result.Detail = "返回值/错误来自源码 return 语句"
	}
	if len(out.result.Suspicious) > 0 {
		out.result.Verdict = SeqVerdictUnverified
		out.result.Detail = "存在错误被覆盖/被返回 nil 吞掉的形态（可疑，需人工确认）"
	}
	return out
}

// findFuncBody 按索引行号定位函数体（行号落在声明区间内即命中）。
func findFuncBody(fset *token.FileSet, f *ast.File, line int) *ast.BlockStmt {
	body, _ := findFuncDecl(fset, f, line)
	return body
}

// findFuncDecl 按索引行号定位函数声明（body + 声明——返回槽/参数类型需要）。
func findFuncDecl(fset *token.FileSet, f *ast.File, line int) (*ast.BlockStmt, *ast.FuncDecl) {
	var body *ast.BlockStmt
	var decl *ast.FuncDecl
	ast.Inspect(f, func(node ast.Node) bool {
		fd, ok := node.(*ast.FuncDecl)
		if !ok || fd.Body == nil || body != nil {
			return true
		}
		start := fset.Position(fd.Pos()).Line
		end := fset.Position(fd.End()).Line
		if line >= start && line <= end {
			body, decl = fd.Body, fd
			return false
		}
		return true
	})
	return body, decl
}

// effectVarTypes 形参/接收者/局部变量的类型短名（字段身份匹配用）。
func effectVarTypes(fset *token.FileSet, fn *ast.FuncDecl, src []byte) map[string]string {
	out := map[string]string{}
	addFields := func(fl *ast.FieldList) {
		if fl == nil {
			return
		}
		for _, f := range fl.List {
			typ := starTypeName(f.Type)
			for _, n := range f.Names {
				out[n.Name] = typ
			}
		}
	}
	addFields(fn.Recv)
	addFields(fn.Type.Params)
	if fn.Body == nil {
		return out
	}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.DeclStmt:
			gd, ok := x.Decl.(*ast.GenDecl)
			if !ok {
				return true
			}
			for _, sp := range gd.Specs {
				vs, ok := sp.(*ast.ValueSpec)
				if !ok {
					continue
				}
				typ := exprTypeName(vs.Type)
				for i, name := range vs.Names {
					t := typ
					if t == "" && len(vs.Values) == len(vs.Names) {
						t = typeFromValue(vs.Values[i])
					}
					if t != "" {
						out[name.Name] = t
					}
				}
			}
		case *ast.AssignStmt:
			if len(x.Lhs) != len(x.Rhs) {
				return true
			}
			for i, lhs := range x.Lhs {
				id, ok := lhs.(*ast.Ident)
				if !ok || id.Name == "_" {
					continue
				}
				if t := typeFromValue(x.Rhs[i]); t != "" {
					out[id.Name] = t
				}
			}
		}
		return true
	})
	return out
}

// starTypeName 类型表达式 → 短类型名（剥指针/括号）。
func starTypeName(t ast.Expr) string {
	for {
		switch x := t.(type) {
		case *ast.StarExpr:
			t = x.X
			continue
		case *ast.ParenExpr:
			t = x.X
			continue
		}
		return exprTypeName(t)
	}
}

// typeFromValue 赋值右值的类型短名（复合字面量/构造器/类型转换）。
func typeFromValue(e ast.Expr) string {
	if t := implTypeFromExpr(e); t != "" {
		return t
	}
	switch x := e.(type) {
	case *ast.CallExpr:
		if id, ok := x.Fun.(*ast.Ident); ok && len(x.Args) == 1 {
			// T(v) 类型转换：仅当 T 是已知类型名时由调用方接受（保守）
			return id.Name
		}
	case *ast.UnaryExpr:
		return typeFromValue(x.X)
	case *ast.ParenExpr:
		return typeFromValue(x.X)
	}
	return ""
}

// errLikeName 变量名是否像错误（err / *Err / e）。
func errLikeName(name string) bool {
	l := strings.ToLower(name)
	return l == "err" || l == "e" || strings.HasSuffix(l, "err") || strings.HasSuffix(l, "error")
}

// isNilIdent 表达式是否为 nil 字面量。
func isNilIdent(exprs []ast.Expr) bool {
	if len(exprs) != 1 {
		return false
	}
	id, ok := exprs[0].(*ast.Ident)
	return ok && id.Name == "nil"
}

// callName 调用表达式的被调名（Ident / Selector 末段）。
func callName(call *ast.CallExpr) string {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return fun.Name
	case *ast.SelectorExpr:
		return fun.Sel.Name
	}
	return ""
}

// fileLine "file:line" 文本。
func fileLine(file string, line int) string {
	return file + ":" + itoaSeq(line)
}

// nodeText 源码片段（按位置取原文——保留字面形态）。
func nodeText(fset *token.FileSet, src []byte, n ast.Node) string {
	start := fset.Position(n.Pos()).Offset
	end := fset.Position(n.End()).Offset
	if start < 0 || end > len(src) || start >= end {
		return ""
	}
	return string(src[start:end])
}

// dedupStrings 去重（保持顺序）。
func dedupStrings(xs []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		if x == "" || seen[x] {
			continue
		}
		seen[x] = true
		out = append(out, x)
	}
	return out
}

// sortStrings 就地字典序（小工具，避免为此引入 sort 依赖顺序混乱）。
func sortStrings(xs []string) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j] < xs[j-1]; j-- {
			xs[j], xs[j-1] = xs[j-1], xs[j]
		}
	}
}

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
	out.paths, out.stats = a.effectPaths(fset, src, body, scene, args, budget, n.FilePath)
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
			for i, lhs := range x.Lhs {
				id, ok := lhs.(*ast.Ident)
				if !ok {
					continue
				}
				rhs := x.Rhs
				if len(x.Rhs) == len(x.Lhs) {
					rhs = []ast.Expr{x.Rhs[i]}
				}
				// todo14-8：错误变量按类型/构造识别（不只 err 名字）
				if !isErrorVarAssignment(id.Name, rhs, out.varTypes, assigned) {
					continue
				}
				assigned[id.Name] = append(assigned[id.Name], fset.Position(x.Pos()).Line)
				errVars[id.Name] = true
				if isNilIdent(rhs) && len(assigned[id.Name]) > 1 {
					suspicious = append(suspicious, fileLine(n.FilePath, fset.Position(x.Pos()).Line))
				}
			}
		case *ast.ReturnStmt:
			if retLine == 0 {
				retLine = fset.Position(x.Pos()).Line
			}
			for _, r := range x.Results {
				returns = append(returns, strings.TrimSpace(nodeText(fset, src, r)))
				if id, ok := r.(*ast.Ident); ok {
					if errLikeName(id.Name) || isErrorTypeName(out.varTypes[id.Name]) || len(assigned[id.Name]) > 0 {
						errVars[id.Name] = true
					}
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

// findFuncDecl 按索引行号定位函数声明（body + 声明——返回槽/参数类型需要）。

// effectVarTypes 形参/接收者/局部变量的类型短名（字段身份匹配用）。

// starTypeName 类型表达式 → 短类型名（剥指针/括号）。

// typeFromValue 赋值右值的类型短名（复合字面量/构造器/类型转换）。

// errLikeName 变量名是否像错误（err / *Err / e）。

// isNilIdent 表达式是否为 nil 字面量。

// callName 调用表达式的被调名（Ident / Selector 末段）。

// fileLine "file:line" 文本。

// nodeText 源码片段（按位置取原文——保留字面形态）。

// dedupStrings 去重（保持顺序）。

// sortStrings 就地字典序（小工具，避免为此引入 sort 依赖顺序混乱）。

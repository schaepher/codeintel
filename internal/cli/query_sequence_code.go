package cli

// R81 `query sequence --code <符号>`——代码级时序图（源码 AST 转时序）。

import (
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
)

// codeSeqNode 代码级时序节点（树——分支/循环/嵌套调用）。
type codeSeqNode struct {
	Kind    string         // call | branch | loop
	Label   string         // call: 调用名（obj.Method/fn）；branch: 条件；loop: 循环条件
	Actor   string         // R83：call 参与者（调用对象——s.manager/t.repo/ic；函数为函数名）
	Type    string         // R83：参与者短类型名（包最后路径段.类型名——索引实现类型）
	Args    []string       // R83：参数类型短名（消息线内容）
	Returns []string       // R83：返回类型短名（return 线内容）
	Line    int            // 源码行号
	Nodes   []*codeSeqNode // 子节点（branch: then 分支；loop: 循环体；call: 嵌套展开的被调函数调用）
	Else    []*codeSeqNode // branch: else 分支（可选）
}

// codeSequence 解析目标函数的代码级时序（读源码 AST——纯语法不依赖
// 类型信息）。depth 为嵌套层级（1 = 只本函数；>1 递归展开被调函数
// 内部——行号对齐索引调用边定位被调符号）。符号解析失败返回 nil。
// R84：接口方法入口（grpc 服务入口接口——动态入口无方法体，如
// (OrderServiceServer).SubmitOrder）→ 具体化到实现方法再解析。

// codeSeqForSymbol 按符号 ID 解析函数体（递归入口——嵌套展开用
// canonical ID 直接解析）。

// walkStmts 遍历语句列表生成步骤（调用语句 / 分支 / 循环 / 块递归）。
func walkStmts(acts *action.Actions, abs string, fset *token.FileSet, src []byte, stmts []ast.Stmt, lineTargets map[int]string, depth int) []*codeSeqNode {
	var out []*codeSeqNode
	for _, st := range stmts {
		out = append(out, walkStmt(acts, abs, fset, src, st, lineTargets, depth)...)
	}
	return out
}

// walkStmt 单条语句 → 步骤（可能多条：赋值内多个调用）。
func walkStmt(acts *action.Actions, abs string, fset *token.FileSet, src []byte, stmt ast.Stmt, lineTargets map[int]string, depth int) []*codeSeqNode {
	callNode := func(fun ast.Expr, line int) *codeSeqNode {
		node := &codeSeqNode{Kind: "call", Label: callLabel(fset, src, fun), Actor: callActor(fset, src, fun), Line: line}
		// R83：签名提取（类型节点用 AST 方法名构造 (Impl).Method 再查）
		if tid, ok := lineTargets[line]; ok {
			sigID := tid
			if !strings.Contains(tid, ":(") {
				if sel, isSel := fun.(*ast.SelectorExpr); isSel {
					sigID = action.GrpcMethodEntryID(tid, sel.Sel.Name)
				}
			}
			node.Type = implTypeShort(tid)
			if args, rets, ok2 := sigTypesOf(acts, sigID); ok2 {
				node.Args, node.Returns = args, rets
			}
		}
		// R81：嵌套展开（类型节点用 AST 方法名构造 (Impl).Method；函数失败回退原 ID）
		if depth > 1 {
			if tid, ok := lineTargets[line]; ok {
				// R83：停止包配置——命中不深入（节点保留，Nodes 空）
				if seqStopPkgHit(tid) {
					return node
				}
				if !strings.Contains(tid, ":(") {
					if sel, isSel := fun.(*ast.SelectorExpr); isSel {
						if child := codeSeqForSymbol(acts, abs, action.GrpcMethodEntryID(tid, sel.Sel.Name), depth-1); child != nil {
							node.Nodes = child.Nodes
							return node
						}
					}
				}
				if child := codeSeqForSymbol(acts, abs, tid, depth-1); child != nil {
					node.Nodes = child.Nodes
				}
			}
		}
		return node
	}
	switch s := stmt.(type) {
	case *ast.ExprStmt:
		if call, ok := s.X.(*ast.CallExpr); ok {
			return []*codeSeqNode{callNode(call.Fun, fset.Position(call.Pos()).Line)}
		}
	case *ast.AssignStmt:
		var out []*codeSeqNode
		for _, rhs := range s.Rhs {
			if call, ok := rhs.(*ast.CallExpr); ok {
				out = append(out, callNode(call.Fun, fset.Position(call.Pos()).Line))
			}
		}
		return out
	case *ast.IfStmt:
		node := &codeSeqNode{Kind: "branch", Label: exprText(fset, src, s.Cond), Line: fset.Position(s.Cond.Pos()).Line}
		node.Nodes = walkStmts(acts, abs, fset, src, s.Body.List, lineTargets, depth)
		if blk, ok := s.Else.(*ast.BlockStmt); ok {
			node.Else = walkStmts(acts, abs, fset, src, blk.List, lineTargets, depth)
		} else if elseIf, ok := s.Else.(*ast.IfStmt); ok {
			node.Else = walkStmt(acts, abs, fset, src, elseIf, lineTargets, depth) // else if 链（渲染为 else 内分支）
		}
		return []*codeSeqNode{node}
	case *ast.ForStmt:
		label := exprText(fset, src, s.Cond)
		if label == "" {
			label = "无限循环"
		}
		return []*codeSeqNode{{Kind: "loop", Label: label, Line: fset.Position(s.Pos()).Line,
			Nodes: walkStmts(acts, abs, fset, src, s.Body.List, lineTargets, depth)}}
	case *ast.RangeStmt:
		return []*codeSeqNode{{Kind: "loop", Label: "range " + exprText(fset, src, s.X), Line: fset.Position(s.Pos()).Line,
			Nodes: walkStmts(acts, abs, fset, src, s.Body.List, lineTargets, depth)}}
	case *ast.BlockStmt:
		return walkStmts(acts, abs, fset, src, s.List, lineTargets, depth)
	case *ast.DeferStmt:
		return []*codeSeqNode{callNode(s.Call.Fun, fset.Position(s.Call.Pos()).Line)}
	case *ast.GoStmt:
		return []*codeSeqNode{callNode(s.Call.Fun, fset.Position(s.Call.Pos()).Line)}
	case *ast.ReturnStmt:
		var out []*codeSeqNode
		for _, r := range s.Results {
			if call, ok := r.(*ast.CallExpr); ok {
				out = append(out, callNode(call.Fun, fset.Position(call.Pos()).Line))
			}
		}
		return out
	case *ast.SwitchStmt:
		// R82：switch 分派（manager.SubmitOrder 实测——data.Type 分支里
		// 才是真实业务调用；每 case 一个子分支）
		tag := exprText(fset, src, s.Tag)
		if tag == "" {
			tag = "switch"
		}
		node := &codeSeqNode{Kind: "branch", Label: tag, Line: fset.Position(s.Pos()).Line}
		for _, st := range s.Body.List {
			if cc, ok := st.(*ast.CaseClause); ok {
				caseNode := &codeSeqNode{Kind: "branch", Label: "case " + caseExpr(fset, src, cc), Line: fset.Position(cc.Pos()).Line}
				caseNode.Nodes = walkStmts(acts, abs, fset, src, cc.Body, lineTargets, depth)
				node.Nodes = append(node.Nodes, caseNode)
			}
		}
		return []*codeSeqNode{node}
	case *ast.TypeSwitchStmt:
		node := &codeSeqNode{Kind: "branch", Label: "type switch", Line: fset.Position(s.Pos()).Line}
		for _, st := range s.Body.List {
			if cc, ok := st.(*ast.CaseClause); ok {
				caseNode := &codeSeqNode{Kind: "branch", Label: "case " + caseExpr(fset, src, cc), Line: fset.Position(cc.Pos()).Line}
				caseNode.Nodes = walkStmts(acts, abs, fset, src, cc.Body, lineTargets, depth)
				node.Nodes = append(node.Nodes, caseNode)
			}
		}
		return []*codeSeqNode{node}
	}
	return nil
}

// caseExpr case 子句条件文本（多值逗号连接；default 为空）。
func caseExpr(fset *token.FileSet, src []byte, cc *ast.CaseClause) string {
	if len(cc.List) == 0 {
		return "default"
	}
	var parts []string
	for _, e := range cc.List {
		parts = append(parts, exprText(fset, src, e))
	}
	return strings.Join(parts, ", ")
}

// callLabel 调用目标文本（obj.Method / fn() / (T).m——源码形态）。
func callLabel(fset *token.FileSet, src []byte, fun ast.Expr) string {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return exprText(fset, src, f.X) + "." + f.Sel.Name
	default:
		return exprText(fset, src, fun)
	}
}

// callActor 调用参与者（R83：对象而非方法）——s.manager.SubmitOrder →
// s.manager；t.repo.CreateOrder → t.repo；ic.Put → ic；链式
// s.memberRepo.GetMember(...).GetAccount → s.memberRepo（取调用链
// receiver）；纯函数（Ident/其他）→ 调用名本身。
func callActor(fset *token.FileSet, src []byte, fun ast.Expr) string {
	switch f := fun.(type) {
	case *ast.SelectorExpr:
		// 链式调用（X 是调用）：取被调方法的 receiver（GetMember 的
		// receiver = s.memberRepo）
		if call, ok := f.X.(*ast.CallExpr); ok {
			if sel, ok2 := call.Fun.(*ast.SelectorExpr); ok2 {
				return exprText(fset, src, sel.X)
			}
		}
		return exprText(fset, src, f.X)
	case *ast.Ident:
		return f.Name
	default:
		return callLabel(fset, src, fun)
	}
}

// exprText 表达式源码文本（fset 位置取原文——保持源码形态）。
func exprText(fset *token.FileSet, src []byte, e ast.Expr) string {
	if e == nil {
		return ""
	}
	start := fset.Position(e.Pos()).Offset
	end := fset.Position(e.End()).Offset
	if start < 0 || end > len(src) || start >= end {
		return ""
	}
	s := string(src[start:end])
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}

// cmdQuerySequenceCode 实现 `query sequence --code <符号> [--depth N] [--format mermaid] [--json]`。
// depth：嵌套层级（1 = 只本函数；>1 递归展开被调函数内部）。
func cmdQuerySequenceCode(acts *action.Actions, abs, target string, depth int, mermaid bool, jsonOut bool) int {
	if depth <= 0 {
		depth = 1
	}
	root := codeSequence(acts, abs, target, depth)
	if root == nil {
		fmt.Fprintf(os.Stderr, "error: 代码级时序不可用（符号解析失败或源码缺失——可尝试不带 --code 用索引调用链）\n")
		return 1
	}
	if jsonOut {
		encodeJSON(root)
		return 0
	}
	if mermaid {
		fmt.Print(renderCodeSeqMermaid(root))
		return 0
	}
	// 默认文本：缩进树（源码顺序 + 分支/循环标注）
	fmt.Printf("== %s 代码级时序 ==\n", root.Label)
	writeSeqText(root.Nodes, 1)
	return 0
}

// writeSeqText 缩进文本渲染（默认输出）。

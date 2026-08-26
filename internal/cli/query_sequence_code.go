package cli

// R81 `query sequence --code <符号>`——代码级时序图：解析目标函数的
// 源码 AST，把整个函数体的调用语句按源码顺序转成时序图（支持基本
// 分支 if/else 与循环 for/range——mermaid alt/loop 块）。消息线写
// 具体调用（obj.Method / fn()——源码调用名），不是实现类型。
// 与索引调用链（CalleesConcrete）互补：索引链回答"调用了谁"，
// 代码级回答"函数内部怎么调用"（顺序/分支/循环）。

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
)

// codeSeqNode 代码级时序节点（树——分支/循环/嵌套调用）。
type codeSeqNode struct {
	Kind	string		// call | branch | loop
	Label	string		// call: 调用名（obj.Method/fn）；branch: 条件；loop: 循环条件
	Line	int		// 源码行号
	Nodes	[]*codeSeqNode	// 子节点（branch: then 分支；loop: 循环体；call: 嵌套展开的被调函数调用）
	Else	[]*codeSeqNode	// branch: else 分支（可选）
}

// codeSequence 解析目标函数的代码级时序（读源码 AST——纯语法不依赖
// 类型信息）。depth 为嵌套层级（1 = 只本函数；>1 递归展开被调函数
// 内部——行号对齐索引调用边定位被调符号）。符号解析失败返回 nil。
func codeSequence(acts *action.Actions, abs, target string, depth int) *codeSeqNode {
	n, err := acts.ResolveSymbol(target)
	if err != nil {
		return nil
	}
	return codeSeqForSymbol(acts, abs, string(n.ID), depth)
}

// codeSeqForSymbol 按符号 ID 解析函数体（递归入口——嵌套展开用
// canonical ID 直接解析）。
func codeSeqForSymbol(acts *action.Actions, abs, symID string, depth int) *codeSeqNode {
	n, err := acts.ResolveSymbol(symID)
	if err != nil || n.FilePath == "" {
		return nil
	}
	src, err := os.ReadFile(filepath.Join(abs, n.FilePath))
	if err != nil {
		return nil
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, n.FilePath, src, 0)
	if err != nil {
		return nil
	}
	// 定位目标函数（LineStart 在函数体范围内）
	var fn *ast.FuncDecl
	ast.Inspect(f, func(node ast.Node) bool {
		if fd, ok := node.(*ast.FuncDecl); ok {
			start := fset.Position(fd.Pos()).Line
			end := fset.Position(fd.End()).Line
			if n.LineStart >= start && n.LineStart <= end {
				fn = fd
				return false
			}
		}
		return true
	})
	if fn == nil {
		return nil
	}
	// 行号 → 被调符号 ID（索引调用边 line_num 对齐——嵌套展开定位）
	lineTargets := map[int]string{}
	if depth > 1 {
		if facts, err := acts.CalleesConcrete(domain.CanonicalID(symID), 1); err == nil {
			for _, f := range facts {
				if ln, ok := f.Metadata["line_num"].(float64); ok {
					lineTargets[int(ln)] = string(f.TargetID)
				}
			}
		}
	}
	root := &codeSeqNode{Kind: "call", Label: fn.Name.Name, Line: n.LineStart}
	root.Nodes = walkStmts(acts, abs, fset, src, fn.Body.List, lineTargets, depth)
	return root
}

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
		node := &codeSeqNode{Kind: "call", Label: callLabel(fset, src, fun), Line: line}
		// R81：嵌套展开——行号对齐被调符号，递归解析其函数体（depth-1）。
		// 接口调用具体化到实现**类型**节点（无方法名——调用点未记录）
		// 时，用 AST 调用方法名构造 (Impl).Method 再解析（R75 形态）；
		// 函数节点先试方法形态失败后回退原 ID（LoadItems → 非方法）
		if depth > 1 {
			if tid, ok := lineTargets[line]; ok {
				if !strings.Contains(tid, ":(") {
					if sel, isSel := fun.(*ast.SelectorExpr); isSel {
						if child := codeSeqForSymbol(acts, abs, grpcMethodEntryID(tid, sel.Sel.Name), depth-1); child != nil {
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
			node.Else = walkStmt(acts, abs, fset, src, elseIf, lineTargets, depth)	// else if 链（渲染为 else 内分支）
		}
		return []*codeSeqNode{node}
	case *ast.ForStmt:
		label := exprText(fset, src, s.Cond)
		if label == "" {
			label = "无限循环"
		}
		return []*codeSeqNode{{Kind: "loop", Label: label, Line: fset.Position(s.Pos()).Line,
			Nodes:	walkStmts(acts, abs, fset, src, s.Body.List, lineTargets, depth)}}
	case *ast.RangeStmt:
		return []*codeSeqNode{{Kind: "loop", Label: "range " + exprText(fset, src, s.X), Line: fset.Position(s.Pos()).Line,
			Nodes:	walkStmts(acts, abs, fset, src, s.Body.List, lineTargets, depth)}}
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
	}
	return nil
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

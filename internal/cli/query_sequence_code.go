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
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
)

// codeSeqNode 代码级时序节点（树——分支/循环嵌套）。
type codeSeqNode struct {
	Kind  string        // call | branch | loop
	Label string        // call: 调用名（obj.Method/fn）；branch: 条件；loop: 循环条件
	Line  int           // 源码行号
	Nodes []*codeSeqNode // 子节点（branch: then 分支；loop: 循环体）
	Else  []*codeSeqNode // branch: else 分支（可选）
}

// codeSequence 解析目标函数的代码级时序（读源码 AST——纯语法不依赖
// 类型信息）。符号解析失败/文件缺失返回 nil（不阻塞——fallback 索引链）。
func codeSequence(acts *action.Actions, abs, target string) *codeSeqNode {
	n, err := acts.ResolveSymbol(target)
	if err != nil {
		return nil
	}
	if n.FilePath == "" {
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
	root := &codeSeqNode{Kind: "call", Label: fn.Name.Name, Line: n.LineStart}
	root.Nodes = walkStmts(fset, src, fn.Body.List)
	return root
}

// walkStmts 遍历语句列表生成步骤（调用语句 / 分支 / 循环 / 块递归）。
func walkStmts(fset *token.FileSet, src []byte, stmts []ast.Stmt) []*codeSeqNode {
	var out []*codeSeqNode
	for _, st := range stmts {
		out = append(out, walkStmt(fset, src, st)...)
	}
	return out
}

// walkStmt 单条语句 → 步骤（可能多条：赋值内多个调用）。
func walkStmt(fset *token.FileSet, src []byte, stmt ast.Stmt) []*codeSeqNode {
	switch s := stmt.(type) {
	case *ast.ExprStmt:
		if call, ok := s.X.(*ast.CallExpr); ok {
			return []*codeSeqNode{{Kind: "call", Label: callLabel(fset, src, call.Fun), Line: fset.Position(call.Pos()).Line}}
		}
	case *ast.AssignStmt:
		var out []*codeSeqNode
		for _, rhs := range s.Rhs {
			if call, ok := rhs.(*ast.CallExpr); ok {
				out = append(out, &codeSeqNode{Kind: "call", Label: callLabel(fset, src, call.Fun), Line: fset.Position(call.Pos()).Line})
			}
		}
		return out
	case *ast.IfStmt:
		node := &codeSeqNode{Kind: "branch", Label: exprText(fset, src, s.Cond), Line: fset.Position(s.Cond.Pos()).Line}
		node.Nodes = walkStmts(fset, src, s.Body.List)
		if blk, ok := s.Else.(*ast.BlockStmt); ok {
			node.Else = walkStmts(fset, src, blk.List)
		} else if elseIf, ok := s.Else.(*ast.IfStmt); ok {
			node.Else = walkStmt(fset, src, elseIf) // else if 链（渲染为 else 内分支）
		}
		return []*codeSeqNode{node}
	case *ast.ForStmt:
		label := exprText(fset, src, s.Cond)
		if label == "" {
			label = "无限循环"
		}
		return []*codeSeqNode{{Kind: "loop", Label: label, Line: fset.Position(s.Pos()).Line,
			Nodes: walkStmts(fset, src, s.Body.List)}}
	case *ast.RangeStmt:
		return []*codeSeqNode{{Kind: "loop", Label: "range " + exprText(fset, src, s.X), Line: fset.Position(s.Pos()).Line,
			Nodes: walkStmts(fset, src, s.Body.List)}}
	case *ast.BlockStmt:
		return walkStmts(fset, src, s.List)
	case *ast.DeferStmt:
		return []*codeSeqNode{{Kind: "call", Label: "defer " + callLabel(fset, src, s.Call.Fun), Line: fset.Position(s.Call.Pos()).Line}}
	case *ast.GoStmt:
		return []*codeSeqNode{{Kind: "call", Label: "go " + callLabel(fset, src, s.Call.Fun), Line: fset.Position(s.Call.Pos()).Line}}
	case *ast.ReturnStmt:
		var out []*codeSeqNode
		for _, r := range s.Results {
			if call, ok := r.(*ast.CallExpr); ok {
				out = append(out, &codeSeqNode{Kind: "call", Label: callLabel(fset, src, call.Fun), Line: fset.Position(call.Pos()).Line})
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

// renderCodeSeqMermaid 代码级步骤树 → mermaid sequenceDiagram
// （参与者 = 调用目标 + 入口；消息线 = 调用名；branch → alt/else；
// loop → loop 块）。
func renderCodeSeqMermaid(root *codeSeqNode) string {
	if root == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("sequenceDiagram\n")
	// 参与者：入口 + 全部调用目标（确定性排序）
	parts := []string{root.Label}
	seen := map[string]bool{root.Label: true}
	collectParts(root.Nodes, &parts, seen)
	sort.Slice(parts[1:], func(i, j int) bool { return parts[i+1] < parts[j+1] })
	alias := map[string]string{}
	for i, p := range parts {
		alias[p] = fmt.Sprintf("P%d", i)
		b.WriteString(fmt.Sprintf("  participant %s as %s\n", alias[p], p))
	}
	writeSeqNode(&b, alias, root.Label, root.Nodes)
	return b.String()
}

// collectParts 收集参与者（调用目标去重）。
func collectParts(nodes []*codeSeqNode, parts *[]string, seen map[string]bool) {
	for _, n := range nodes {
		if n.Kind == "call" && !seen[n.Label] {
			seen[n.Label] = true
			*parts = append(*parts, n.Label)
		}
		collectParts(n.Nodes, parts, seen)
		collectParts(n.Else, parts, seen)
	}
}

// writeSeqNode 递归渲染节点块（call 消息 / branch alt / loop）。
func writeSeqNode(b *strings.Builder, alias map[string]string, from string, nodes []*codeSeqNode) {
	for _, n := range nodes {
		switch n.Kind {
		case "call":
			to := alias[n.Label]
			if to == "" {
				to = alias[from] // 未注册参与者（安全兜底）
			}
			b.WriteString(fmt.Sprintf("  %s->>%s: %s\n", alias[from], to, n.Label))
		case "branch":
			b.WriteString(fmt.Sprintf("  alt %s\n", n.Label))
			writeSeqNode(b, alias, from, n.Nodes)
			if len(n.Else) > 0 {
				b.WriteString("  else\n")
				writeSeqNode(b, alias, from, n.Else)
			}
			b.WriteString("  end\n")
		case "loop":
			b.WriteString(fmt.Sprintf("  loop %s\n", n.Label))
			writeSeqNode(b, alias, from, n.Nodes)
			b.WriteString("  end\n")
		}
	}
}

// cmdQuerySequenceCode 实现 `query sequence --code <符号> [--format mermaid] [--json]`。
func cmdQuerySequenceCode(acts *action.Actions, abs, target string, mermaid bool, jsonOut bool) int {
	root := codeSequence(acts, abs, target)
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
func writeSeqText(nodes []*codeSeqNode, depth int) {
	pad := strings.Repeat("  ", depth)
	for _, n := range nodes {
		switch n.Kind {
		case "call":
			fmt.Printf("%s%d. %s\n", pad, n.Line, n.Label)
		case "branch":
			fmt.Printf("%sif %s\n", pad, n.Label)
			writeSeqText(n.Nodes, depth+1)
			if len(n.Else) > 0 {
				fmt.Printf("%selse\n", pad)
				writeSeqText(n.Else, depth+1)
			}
		case "loop":
			fmt.Printf("%sloop %s\n", pad, n.Label)
			writeSeqText(n.Nodes, depth+1)
		}
	}
}

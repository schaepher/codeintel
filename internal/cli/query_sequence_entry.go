package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
)

// codeSequence 解析目标函数的代码级时序（读源码 AST——纯语法不依赖
// 类型信息）。depth 为嵌套层级（1 = 只本函数；>1 递归展开被调函数
// 内部——行号对齐索引调用边定位被调符号）。符号解析失败返回 nil。
// R84：接口方法入口（grpc 服务入口接口——动态入口无方法体，如
// (OrderServiceServer).SubmitOrder）→ 具体化到实现方法再解析。
func codeSequence(acts *action.Actions, abs, target string, depth int) *codeSeqNode {
	n, err := acts.ResolveSymbol(target)
	if err != nil {
		return nil
	}
	root := codeSeqForSymbol(acts, abs, string(n.ID), depth)
	if root == nil && strings.Contains(string(n.ID), ":(") {
		if impl, ok := acts.InterfaceMethodImpl(string(n.ID)); ok {
			root = codeSeqForSymbol(acts, abs, impl, depth)
		}
	}
	return root
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

	lineTargets := map[int]string{}
	if facts, err := acts.CalleesConcrete(domain.CanonicalID(symID), 1); err == nil {
		for _, f := range facts {
			if ln, ok := f.Metadata["line_num"].(float64); ok {
				lineTargets[int(ln)] = string(f.TargetID)
			}
		}
	}
	root := &codeSeqNode{Kind: "call", Label: fn.Name.Name, Line: n.LineStart}
	root.Nodes = walkStmts(acts, abs, fset, src, fn.Body.List, lineTargets, depth)
	return root
}

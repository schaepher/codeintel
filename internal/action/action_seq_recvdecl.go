package action

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"

	"github.com/schaepher/codeintel/internal/domain"
)

// codeSeqForSymbol 按符号 ID 解析函数体（递归入口——嵌套展开用
// canonical ID 直接解析；depth = 当前剩余嵌套层级）。
func codeSeqForSymbol(a *Actions, req CodeSequenceRequest, symID string, depth int) *CodeSeqNode {

	if req.visited[symID] {
		return nil
	}
	req.visited[symID] = true
	defer delete(req.visited, symID)
	n, err := a.repo.GetSymbol(domain.CanonicalID(symID))
	if err != nil || n.FilePath == "" {
		return nil
	}
	src, err := os.ReadFile(filepath.Join(req.RepoAbs, n.FilePath))
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
	if facts, err := a.CalleesConcrete(domain.CanonicalID(symID), 1); err == nil {
		for _, f := range facts {
			if ln, ok := f.Metadata["line_num"].(float64); ok {
				lineTargets[int(ln)] = string(f.TargetID)
			}
		}
	}

	recvType := ""

	recvDecl := map[string]string{}
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		recvType = recvTypeName(fn.Recv.List[0].Type)
		ast.Inspect(f, func(node ast.Node) bool {
			ts, ok := node.(*ast.TypeSpec)
			if !ok || ts.Name.Name != recvType {
				return true
			}
			if st, ok := ts.Type.(*ast.StructType); ok {
				for _, fl := range st.Fields.List {
					if len(fl.Names) == 1 {
						recvDecl[fl.Names[0].Name] = exprTypeName(fl.Type)
					}
				}
				return false
			}
			return true
		})
	}
	root := &CodeSeqNode{Kind: "call", Label: fn.Name.Name, Line: n.LineStart}
	root.Nodes = walkStmts(a, req, fset, src, fn.Body.List, lineTargets, depth, recvType, recvDecl, f, pkgPathOf(symID))
	return root
}

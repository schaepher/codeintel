package action

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

type parsedFunc struct {
	file string
	src  []byte
	fset *token.FileSet
	decl *ast.FuncDecl
}

func (a *Actions) parseFuncNode(id string) *parsedFunc {
	n, err := a.repo.GetSymbol(domain.CanonicalID(id))
	if err != nil || n == nil || n.FilePath == "" {
		return nil
	}
	full := filepath.Join(a.repo.RepoPath(), filepath.FromSlash(n.FilePath))
	src, err := readFileCached(full)
	if err != nil {
		return nil
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, n.FilePath, src, 0)
	if err != nil {
		return nil
	}
	_, decl := findFuncDecl(fset, f, n.LineStart)
	if decl == nil || decl.Body == nil {
		return nil
	}
	return &parsedFunc{file: n.FilePath, src: src, fset: fset, decl: decl}
}

// readFileCached 读取源码文件（边界分析一次查询内复用；失败返回错误）。
func readFileCached(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// fieldLiteralAssignments 记录键字段被赋的字面量（字段赋值 + 复合字面量）。
func fieldLiteralAssignments(pf *parsedFunc, leaf string) []int2string {
	var out []int2string
	ast.Inspect(pf.decl.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignStmt:
			for i, lhs := range x.Lhs {
				sel, ok := lhs.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != leaf || len(x.Rhs) != len(x.Lhs) {
					continue
				}
				if lit, ok := x.Rhs[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					out = append(out, int2string{line: pf.fset.Position(x.Pos()).Line,
						value: strings.Trim(lit.Value, "`\"")})
				}
			}
		case *ast.CompositeLit:
			for _, el := range x.Elts {
				kv, ok := el.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				id, ok := kv.Key.(*ast.Ident)
				if !ok || id.Name != leaf {
					continue
				}
				if lit, ok := kv.Value.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					out = append(out, int2string{line: pf.fset.Position(x.Pos()).Line,
						value: strings.Trim(lit.Value, "`\"")})
				}
			}
		}
		return true
	})
	return out
}

type int2string struct {
	line  int
	value string
}

// boundaryCall 一条候选边界调用（producer 或 consumer）。
type boundaryCall struct {
	line    int
	api     string
	topic   string
	key     string // 实参里的键字面量（可能为空）
	keyExpr string // 实参里的键表达式文本（用于依据）
}

func collectBoundaryCalls(pf *parsedFunc, names map[string]bool, fieldLeaf string) []boundaryCall {
	var out []boundaryCall
	ast.Inspect(pf.decl.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !names[strings.ToLower(sel.Sel.Name)] {
			return true
		}
		bc := boundaryCall{line: pf.fset.Position(call.Lparen).Line, api: sel.Sel.Name}
		for _, arg := range call.Args {
			switch a := arg.(type) {
			case *ast.BasicLit:
				if a.Kind == token.STRING {
					v := strings.Trim(a.Value, "`\"")
					if bc.topic == "" && !strings.Contains(strings.ToLower(fieldLeaf), strings.ToLower(v)) {
						bc.topic = v
					} else if bc.key == "" {
						bc.key = v
					}
				}
			case *ast.SelectorExpr:
				if a.Sel.Name == fieldLeaf {
					bc.keyExpr = exprTextShallow(a)
				}
			}
		}
		out = append(out, bc)
		return true
	})
	return out
}

// consumerFilterLiterals 读取侧的过滤字面量（== / != 比较 + 调用实参）。
func consumerFilterLiterals(pf *parsedFunc) []int2string {
	var out []int2string
	ast.Inspect(pf.decl.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.BinaryExpr:
			if x.Op != token.EQL && x.Op != token.NEQ {
				return true
			}
			for _, e := range []ast.Expr{x.X, x.Y} {
				if lit, ok := e.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					out = append(out, int2string{line: pf.fset.Position(x.Pos()).Line,
						value: strings.Trim(lit.Value, "`\"")})
				}
			}
		case *ast.CallExpr:
			for _, arg := range x.Args {
				if lit, ok := arg.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					out = append(out, int2string{line: pf.fset.Position(x.Lparen).Line,
						value: strings.Trim(lit.Value, "`\"")})
				}
			}
		}
		return true
	})
	return out
}

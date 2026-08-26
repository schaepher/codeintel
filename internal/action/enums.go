package action

// R89 迁移：枚举提取业务逻辑（原 cli/extractEnums）——渲染与输出留 cli。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)


// enumEntry 一个枚举常量。
type EnumEntry struct {
	Pkg	string	`json:"pkg"`		// 包路径（短名）/ proto package
	Type	string	`json:"type"`		// 枚举类型（空 = 无类型 const 组）
	Group	string	`json:"group"`		// 所在 const 块（首常量名）/ 枚举名
	Name	string	`json:"name"`		// 常量名/值名
	Value	string	`json:"value"`		// 字符串值/值号
	Comment	string	`json:"comment"`	// 行内注释
	File	string	`json:"file"`		// 定义文件
	Line	int	`json:"line"`		// 定义行
	Source	string	`json:"source"`		// 来源：go | proto（R29 grpc 枚举）
}
// extractEnums 提取仓库内字符串枚举常量（类型化或 const 块内字符串
// 字面量）——排除 _test.go 与外部目录（R29：全仓扫，不再限 internal/；
// 跳过 _pb.go 生成代码——枚举由 .proto 源提供）。onlyTyped=true 时
// 只返回有显式类型的枚举（无类型字符串常量多为展示标签——默认过滤；
// --include-untyped 放开）。R29：并入 .proto 源枚举（Source=proto）。
func Enums(repoAbs string, onlyTyped bool) []EnumEntry {
	var out []EnumEntry
	root := repoAbs
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || strings.HasSuffix(name, ".pb.go") {
			return nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return nil
		}
		pkg := f.Name.Name
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			var groupName string	// const 块首常量名（分组）
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok || len(vs.Names) == 0 || len(vs.Values) == 0 {
					continue
				}

				lit, ok := vs.Values[0].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				val, err := StrconvUnquote(lit.Value)
				if err != nil {
					continue
				}

				if len(val) > 64 {
					continue
				}
				typ := ""
				if vs.Type != nil {
					typ = ExprName(vs.Type)
				}

				if onlyTyped && typ == "" {
					continue
				}
				if groupName == "" {
					groupName = vs.Names[0].Name
				}
				comment := ""
				if vs.Comment != nil {
					comment = strings.TrimSpace(strings.TrimPrefix(vs.Comment.Text(), "//"))
				}
				out = append(out, EnumEntry{
					Pkg:	pkg, Type: typ, Group: groupName,
					Name:	vs.Names[0].Name, Value: val, Comment: comment,
					File:	filepath.ToSlash(path), Line: fset.Position(vs.Pos()).Line,
					Source:	"go",
				})
			}
		}
		return nil
	})

	out = append(out, extractProtoEnums(repoAbs)...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Pkg != out[j].Pkg {
			return out[i].Pkg < out[j].Pkg
		}
		if out[i].Group != out[j].Group {
			return out[i].Group < out[j].Group
		}
		return out[i].Name < out[j].Name
	})
	return out
}
// exprName AST 表达式短名（*ast.Ident / SelectorExpr 末段）。
// ExprName AST 表达式短名（*ast.Ident / SelectorExpr 末段）。
func ExprName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return t.Sel.Name
	}
	return ""
}
// strconvUnquote 去掉字符串字面量引号（含反引号）。
// StrconvUnquote 去掉字符串字面量引号（含反引号）。
func StrconvUnquote(s string) (string, error) {
	if len(s) >= 2 && s[0] == '`' && s[len(s)-1] == '`' {
		return s[1 : len(s)-1], nil
	}
	return strconv.Unquote(s)
}

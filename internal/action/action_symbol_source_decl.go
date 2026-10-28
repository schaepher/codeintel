package action

// todo23 §6：SymbolSource 的声明匹配扩展——struct/interface/type alias/
// 包级 const/var（函数/方法仍走既有精确匹配）。
//
// 文本规则：
//   - 函数/方法：声明全文；
//   - const/var：整个 GenDecl（保留 iota/多值语境），selection 指所选项；
//   - type：单 spec 取整个 GenDecl（含 `type` 关键字），分组取 TypeSpec。

import (
	"fmt"
	"go/ast"
	"go/token"
	"sort"
	"strconv"
	"strings"
)

// symbolSourceImports 按源文件顺序提取 imports（显式别名 "."/"_" 原样保留；
// 无显式别名时不填 alias——不按目录末段猜包名）。
func symbolSourceImports(f *ast.File) []SymbolSourceImport {
	if f == nil || len(f.Imports) == 0 {
		return nil
	}
	out := make([]SymbolSourceImport, 0, len(f.Imports))
	for _, im := range f.Imports {
		if im == nil || im.Path == nil {
			continue
		}
		path, err := strconv.Unquote(im.Path.Value)
		if err != nil {
			path = strings.Trim(im.Path.Value, "`\"")
		}
		item := SymbolSourceImport{Path: path}
		if im.Name != nil {
			item.Alias = im.Name.Name
		}
		out = append(out, item)
	}
	return out
}

// symbolSourceDecl 匹配到的声明（含展示范围与选中范围）。
type symbolSourceDecl struct {
	kind      string   // function|method|struct|interface|type|const|var
	textNode  ast.Node // 输出全文的节点（GenDecl/TypeSpec/FuncDecl）
	selNode   ast.Node // 选中项（函数声明/TypeSpec/ValueSpec）
	fset      *token.FileSet
	fileLabel string
	// todo24 §11.4：声明所在文件的 imports（源文件顺序，显式别名原样保留）。
	imports []SymbolSourceImport
}

// matchAnySymbolSourceDecl 在一个文件里按声明名匹配（含函数/类型/值）。
func matchAnySymbolSourceDecl(fset *token.FileSet, f *ast.File, name, relFile string) (*symbolSourceDecl, error) {
	imports := symbolSourceImports(f)
	if strings.HasPrefix(name, "(") || isPlainIdent(name) {
		// 函数/方法优先（名称形态与既有契约一致）
		if fd, err := matchSymbolSourceDecl(fset, f, name, relFile); err == nil {
			kind := "function"
			if fd.Recv != nil {
				kind = "method"
			}
			return &symbolSourceDecl{kind: kind, textNode: fd, selNode: fd, fset: fset,
				fileLabel: relFile, imports: imports}, nil
		} else if strings.Contains(err.Error(), "多重声明") {
			// 函数名多重声明不能继续按类型/常量匹配（会让错误契约失真）
			return nil, err
		}
	}
	var typeHits []*symbolSourceDecl
	var valueHits []*symbolSourceDecl
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, sp := range gd.Specs {
			switch spec := sp.(type) {
			case *ast.TypeSpec:
				if spec.Name == nil || spec.Name.Name != name {
					continue
				}
				kind := "type"
				switch spec.Type.(type) {
				case *ast.StructType:
					kind = "struct"
				case *ast.InterfaceType:
					kind = "interface"
				}
				text := ast.Node(spec)
				if len(gd.Specs) == 1 {
					text = gd // 单 spec：带 `type` 关键字
				}
				typeHits = append(typeHits, &symbolSourceDecl{kind: kind, textNode: text,
					selNode: spec, fset: fset, fileLabel: relFile, imports: imports})
			case *ast.ValueSpec:
				matched := false
				for _, nm := range spec.Names {
					if nm.Name == name {
						matched = true
						break
					}
				}
				if !matched {
					continue
				}
				kind := "var"
				if gd.Tok == token.CONST {
					kind = "const"
				}
				valueHits = append(valueHits, &symbolSourceDecl{kind: kind, textNode: gd,
					selNode: spec, fset: fset, fileLabel: relFile, imports: imports})
			}
		}
	}
	hits := append(typeHits, valueHits...)
	switch len(hits) {
	case 0:
		return nil, fmt.Errorf("文件内找不到匹配的声明（函数/类型/常量/变量名）: %s in %s", name, relFile)
	case 1:
		return hits[0], nil
	default:
		lines := make([]string, 0, len(hits))
		for _, h := range hits {
			lines = append(lines, fmt.Sprintf("%s@第 %d 行", h.kind, h.fset.Position(h.selNode.Pos()).Line))
		}
		sort.Strings(lines)
		return nil, fmt.Errorf("文件内有多重声明（%d 处同名）: %s in %s（%s）",
			len(hits), name, relFile, strings.Join(lines, ", "))
	}
}

func isPlainIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || (i > 0 && r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

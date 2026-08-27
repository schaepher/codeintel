package cli

// R20 表关联结构体：源码扫描 TableName() 方法反查结构体（ORM 结构体
// ↔ 表），提取结构体定义源码片段——表定义上方展示，可折叠展开供
// 用户核对字段映射。纯工具（源码事实），零 AI。

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// ormStruct 一个与表关联的结构体。
type ormStruct struct {
	Name   string     `json:"name"`
	File   string     `json:"file"`
	Line   int        `json:"line"`
	Code   string     `json:"code"` // 结构体定义源码片段
	Fields []ormField `json:"fields,omitempty"`
}

// ormField 结构体字段（R21：Go 类型 → 表列类型 fallback；R22：字段
// 顺序 + 自增识别）。
type ormField struct {
	Name      string // 字段名
	GoType    string // Go 类型（int64/string/time.Time…）
	Column    string // 列名（gorm column tag 优先，无 tag snake_case）
	IsAutoInc bool   // gorm tag 含 autoIncrement
}

// scanORMStructs 扫描仓库源码：func (T) TableName() string { return
// "tbl" } → 表名 tbl ↔ 结构体 T（含定义源码片段）。跳过后端目录。
func scanORMStructs(repoAbs string) map[string][]ormStruct {
	out := map[string][]ormStruct{}
	_ = filepath.Walk(repoAbs, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.Contains(path, "/vendor/") ||
			strings.Contains(path, "/.codeintel/") {
			return nil
		}
		scanORMFile(repoAbs, path, out)
		return nil
	})
	// 确定性排序
	for tbl := range out {
		sort.Slice(out[tbl], func(i, j int) bool { return out[tbl][i].Name < out[tbl][j].Name })
	}
	return out
}

// scanORMFile 解析单个文件：收集 TableName 方法（表名 → 类型名）与
// 结构体定义位置，然后配对提取源码。
func scanORMFile(repoAbs, path string, out map[string][]ormStruct) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return
	}
	type structDef struct {
		name   string
		pos    token.Pos
		end    token.Pos
		fields []ormField
	}
	var defs []structDef
	tableOf := map[string]string{} // 类型名 → 表名
	ast.Inspect(f, func(n ast.Node) bool {
		switch t := n.(type) {
		case *ast.FuncDecl:
			if t.Name.Name != "TableName" || t.Recv == nil || len(t.Recv.List) != 1 {
				return true
			}
			recv := t.Recv.List[0].Type
			if star, ok := recv.(*ast.StarExpr); ok {
				recv = star.X
			}
			id, ok := recv.(*ast.Ident)
			if !ok {
				return true
			}
			// 返回值字符串字面量
			if t.Type != nil && t.Type.Results != nil && len(t.Type.Results.List) == 1 {
				if lit, ok := t.Type.Results.List[0].Type.(*ast.Ident); ok && lit.Name == "string" {
					for _, st := range t.Body.List {
						if ret, ok := st.(*ast.ReturnStmt); ok && len(ret.Results) == 1 {
							if s, ok := ret.Results[0].(*ast.BasicLit); ok && s.Kind == token.STRING {
								tableOf[id.Name] = strings.Trim(s.Value, "`\"")
							}
						}
					}
				}
			}
		case *ast.TypeSpec:
			if st, ok := t.Type.(*ast.StructType); ok {
				d := structDef{name: t.Name.Name, pos: st.Pos(), end: st.End()}
				for _, f := range st.Fields.List {
					if len(f.Names) == 0 {
						continue // 嵌入字段（匿名字段）跳过
					}
					for _, fn := range f.Names {
						d.fields = append(d.fields, ormField{
							Name:      fn.Name,
							GoType:    goTypeString(f.Type),
							Column:    columnOf(fn.Name, f.Tag),
							IsAutoInc: f.Tag != nil && strings.Contains(f.Tag.Value, "autoIncrement"),
						})
					}
				}
				defs = append(defs, d)
			}
		}
		return true
	})
	if len(tableOf) == 0 || len(defs) == 0 {
		return
	}
	// 读源文件（源码片段）
	src, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for typeName, tbl := range tableOf {
		for _, d := range defs {
			if d.name != typeName {
				continue
			}
			start := fset.Position(d.pos).Offset
			end := fset.Position(d.end).Offset
			if start < 0 || end > len(src) || start >= end {
				continue
			}
			code := string(src[start:end])
			out[tbl] = append(out[tbl], ormStruct{
				Name:   typeName,
				File:   relPath(repoAbs, path),
				Line:   fset.Position(d.pos).Line,
				Code:   code,
				Fields: d.fields,
			})
		}
	}
}

// relPath 仓库相对路径。
func relPath(repoAbs, path string) string {
	if r, err := filepath.Rel(repoAbs, path); err == nil {
		return r
	}
	return path
}

// renderORMStructSectionMD 表上方关联结构体区块（md：<details> 折叠）。
func renderORMStructSectionMD(tbl string, structs []ormStruct) string {
	if len(structs) == 0 {
		return ""
	}
	var b strings.Builder
	for _, s := range structs {
		b.WriteString(fmt.Sprintf("<details><summary>关联结构体 <code>%s</code>（%s:%d）——展开核对字段映射</summary>\n\n",
			s.Name, s.File, s.Line))
		b.WriteString("```go\n" + s.Code + "\n```\n")
		b.WriteString("</details>\n\n")
	}
	return b.String()
}

// renderORMStructSectionHTML 表上方关联结构体区块（html：fold-btn
// 折叠，与模块区块同机制）。
func renderORMStructSectionHTML(tbl string, structs []ormStruct) string {
	if len(structs) == 0 {
		return ""
	}
	var b strings.Builder
	for _, s := range structs {
		b.WriteString(fmt.Sprintf(`<h4 class="fold-btn" data-target="orm-%s-%s" data-label="1">▸ 关联结构体 %s（%s:%d）——展开核对字段映射</h4><div class="sec-body" id="orm-%s-%s" style="display:none"><pre class="code">%s</pre></div>`,
			tbl, s.Name, htmlEsc(s.Name), htmlEsc(s.File), s.Line, tbl, s.Name, htmlEsc(s.Code)))
	}
	return b.String()
}

var _ = bytes.MinRead
var _ = domain.KindStruct

// goTypeString Go 类型表达式 → 字符串（含包限定 SelectorExpr）。

// columnOf 字段 → 列名：gorm tag `column:xxx` 优先，否则 snake_case。

// gormColumnRe gorm tag 的 column 名（`gorm:"column:order_id;..."`）。

// snakeCase 大驼峰 → snake_case（GORM 默认列名：OrderNo → order_no、
// ID → id——连续大写不拆，遇到小写后再遇大写才拆）。

// ormColTypes 表列 → Go 类型 fallback（R21）：结构体字段 Go 类型
// 映射表列（gorm column tag 优先、无 tag snake_case）——yaml/schema
// 都无类型时的兜底。

// ormColOrder 表 → 列 → 结构体字段位置（R22：字段顺序还原结构体序）。

// ormAutoIncCols 表 → 列 → 是否自增（R22：gorm autoIncrement tag）。

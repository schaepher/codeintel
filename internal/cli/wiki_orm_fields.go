package cli

import (
	"go/ast"
	"regexp"
	"strings"
)

// goTypeString Go 类型表达式 → 字符串（含包限定 SelectorExpr）。
func goTypeString(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + goTypeString(t.X)
	case *ast.SelectorExpr:
		return goTypeString(t.X) + "." + t.Sel.Name
	case *ast.ArrayType:
		return "[]" + goTypeString(t.Elt)
	default:
		return ""
	}
}

// columnOf 字段 → 列名：gorm tag `column:xxx` 优先，否则 snake_case。
func columnOf(fieldName string, tag *ast.BasicLit) string {
	if tag != nil {
		if m := gormColumnRe.FindStringSubmatch(tag.Value); m != nil {
			return m[1]
		}
	}
	return snakeCase(fieldName)
}

// gormColumnRe gorm tag 的 column 名（`gorm:"column:order_id;..."`）。
var gormColumnRe = regexp.MustCompile(`column:([A-Za-z0-9_]+)`)

// snakeCase 大驼峰 → snake_case（GORM 默认列名：OrderNo → order_no、
// ID → id——连续大写不拆，遇到小写后再遇大写才拆）。
func snakeCase(name string) string {
	var b strings.Builder
	prevLower := false
	for i, r := range name {
		if r >= 'A' && r <= 'Z' {
			if i > 0 && prevLower {
				b.WriteByte('_')
			}
			b.WriteRune(r + 32)
			prevLower = false
		} else {
			b.WriteRune(r)
			prevLower = (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		}
	}
	return b.String()
}

// ormColTypes 表列 → Go 类型 fallback（R21）：结构体字段 Go 类型
// 映射表列（gorm column tag 优先、无 tag snake_case）——yaml/schema
// 都无类型时的兜底。
func ormColTypes(ormStructs map[string][]ormStruct) map[string]map[string]string {
	out := map[string]map[string]string{}
	for tbl, structs := range ormStructs {
		cols := map[string]string{}
		for _, st := range structs {
			for _, f := range st.Fields {
				if f.Column == "" || f.GoType == "" {
					continue
				}
				if _, ok := cols[f.Column]; !ok {
					cols[f.Column] = f.GoType
				}
			}
		}
		if len(cols) > 0 {
			out[tbl] = cols
		}
	}
	return out
}

// ormColOrder 表 → 列 → 结构体字段位置（R22：字段顺序还原结构体序）。
func ormColOrder(ormStructs map[string][]ormStruct) map[string]map[string]int {
	out := map[string]map[string]int{}
	for tbl, structs := range ormStructs {
		cols := map[string]int{}
		idx := 0
		for _, st := range structs {
			for _, f := range st.Fields {
				if f.Column == "" {
					continue
				}
				if _, ok := cols[f.Column]; !ok {
					cols[f.Column] = idx
					idx++
				}
			}
		}
		if len(cols) > 0 {
			out[tbl] = cols
		}
	}
	return out
}

// ormAutoIncCols 表 → 列 → 是否自增（R22：gorm autoIncrement tag）。
func ormAutoIncCols(ormStructs map[string][]ormStruct) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for tbl, structs := range ormStructs {
		cols := map[string]bool{}
		for _, st := range structs {
			for _, f := range st.Fields {
				if f.Column != "" && f.IsAutoInc {
					cols[f.Column] = true
				}
			}
		}
		if len(cols) > 0 {
			out[tbl] = cols
		}
	}
	return out
}

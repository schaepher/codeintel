package cli

// R100：ORM 结构体字段解析（goTypeString/columnOf/snakeCase）随扫描
// 逻辑迁 action（action_wiki_sources.go）——本文件只留渲染辅助。

import (

	"github.com/schaepher/codeintel/internal/domain"
)

// ormColTypes 表列 → Go 类型 fallback（R21）：结构体字段 Go 类型
// 映射表列（gorm column tag 优先、无 tag snake_case）——yaml/schema
// 都无类型时的兜底。
func ormColTypes(ormStructs map[string][]domain.ORMStruct) map[string]map[string]string {
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
func ormColOrder(ormStructs map[string][]domain.ORMStruct) map[string]map[string]int {
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
func ormAutoIncCols(ormStructs map[string][]domain.ORMStruct) map[string]map[string]bool {
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

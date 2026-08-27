package cli

import (
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// mergeTableColumnsWithSchema 列合并（R19）：类型/默认值填充优先级
// yaml > sqlite schema > gorm tag——schema 事实自动补全，yaml 人工可覆盖。
func mergeTableColumnsWithSchema(table string, cols []*domain.TableColumn, yamlCols []wikiTableColumn, schemas map[string]map[string]schemaCol, ormStructs map[string][]ormStruct) []tableColRow {
	rows := mergeTableColumns(table, cols, yamlCols)

	if len(rows) == 0 {
		for _, st := range ormStructs[table] {
			for _, f := range st.Fields {
				if f.Column == "" {
					continue
				}
				rows = append(rows, tableColRow{name: f.Column, typ: f.GoType})
			}
		}
	}

	colType := map[string]string{}
	prefix := table + "."
	for _, c := range cols {
		if strings.HasPrefix(c.Name, prefix) {
			colType[strings.TrimPrefix(c.Name, prefix)] = c.ColType
		}
	}
	goTypes := ormColTypes(ormStructs)
	sc := schemas[table]
	goCols := goTypes[table]
	for i := range rows {
		name := rows[i].name
		if rows[i].typ == "" {
			if sc != nil {
				if c, ok := sc[name]; ok {
					rows[i].typ = c.Typ
				} else if colType[name] != "" {
					rows[i].typ = colType[name]
				} else {
					rows[i].typ = goCols[name]
				}
			} else if colType[name] != "" {
				rows[i].typ = colType[name]
			} else {
				rows[i].typ = goCols[name]
			}
		}
		if rows[i].def == "" && sc != nil {
			if c, ok := sc[name]; ok {
				rows[i].def = c.Def
			}
		}
	}

	order := ormColOrder(ormStructs)[table]
	autoInc := map[string]bool{}
	if sc != nil {
		for name, c := range sc {
			if c.AutoInc {
				autoInc[name] = true
			}
		}
	}
	for name := range ormAutoIncCols(ormStructs)[table] {
		autoInc[name] = true
	}
	sort.SliceStable(rows, func(i, j int) bool {
		ai, aj := autoInc[rows[i].name], autoInc[rows[j].name]
		if ai != aj {
			return ai
		}
		oi, oki := order[rows[i].name]
		oj, okj := order[rows[j].name]
		if oki && okj {
			return oi < oj
		}
		if oki {
			return true
		}
		return false
	})
	return rows
}

// mergeTableColumns 表字段合并（#243 自动初稿 + yaml 覆盖）：
// 自动列（ER 表列虚拟节点：列名 + gorm tag 类型）为底，yaml columns
// 覆盖同名（type/default/comment 各自覆盖），自动列未列出的补全。
func mergeTableColumns(table string, cols []*domain.TableColumn, yamlCols []wikiTableColumn) []tableColRow {

	byName := map[string]tableColRow{}
	var order []string
	hidden := map[string]bool{}
	for _, c := range yamlCols {
		if c.Hidden {
			hidden[c.Name] = true
			continue
		}
		byName[c.Name] = tableColRow{name: c.Name, typ: c.Type, def: c.Default, comment: c.Comment}
		order = append(order, c.Name)
	}

	prefix := table + "."
	for _, c := range cols {
		if !strings.HasPrefix(c.Name, prefix) {
			continue
		}
		col := strings.TrimPrefix(c.Name, prefix)
		if hidden[col] {
			continue
		}

		if r, ok := byName[col]; ok {
			byName[col] = r
			continue
		}
		byName[col] = tableColRow{name: col}
		order = append(order, col)
	}
	out := make([]tableColRow, 0, len(order))
	for _, n := range order {
		out = append(out, byName[n])
	}
	return out
}

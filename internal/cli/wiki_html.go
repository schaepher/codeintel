package cli

// #241 wiki HTML 输出：单文件自包含 index.html——左侧目录导航（模块
// 可折叠展开，锚点定位）+ 内容区块可折叠 + 内嵌 CSS/JS。双击即用，
// 零部署零依赖；与 md 输出共用 wiki.yaml 契约与六区块数据。

import (
	"sort"

	"github.com/schaepher/codeintel/internal/domain"
)





// tableRow 表清单行。
type tableRow struct {
	name  string
	alias string
	mods  []string
}

// collectTables 表清单（去重 + 涉及模块）。
func collectTables(data []*domain.WikiModule, tableAlias map[string]string, tableCfgs map[string]wikiTableConfig) []tableRow {
	byName := map[string]*tableRow{}
	var order []string
	add := func(t string, short string) {
		r, ok := byName[t]
		if !ok {
			r = &tableRow{name: t, alias: tableAlias[t]}
			byName[t] = r
			order = append(order, t)
		}
		if short != "" && !containsStr(r.mods, short) {
			r.mods = append(r.mods, short)
		}
	}
	for _, wm := range data {
		for _, t := range wm.Tables {
			add(t, wm.ShortName)
		}
	}
	// #249：yaml 手写定义的表也渲染
	for name := range tableCfgs {
		add(name, "")
	}
	sort.Strings(order)
	out := make([]tableRow, 0, len(order))
	for _, t := range order {
		out = append(out, *byName[t])
	}
	return out
}




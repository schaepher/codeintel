package cli

// R33 ER 图按业务领域分组（用户要求）：表名前缀（_ 前首段）→ 领域
// （go2o 实测 item_/mch_/member_/order_/trade_ 等前缀与领域目录对应；
// 无前缀归 other）。领域间关系一张图 + 各领域内部图分开展示（同 F2
// 实体图模式）——同时解决 1283 条边大 ER 图渲染失败/超 mermaid 限制。

import (
	"fmt"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// mermaidEdgeLimit mermaid 渲染边数上限（浏览器端渲染超限挂——
// 用户实测 500 条；方案 A 超限自动降级）。
const mermaidEdgeLimit = 500

// splitTableDomain 表名 → 领域（_ 前首段；无 _ → other）。
func splitTableDomain(table string) string {
	if i := strings.Index(table, "_"); i > 0 {
		return table[:i]
	}
	return "other"
}

// erDomainGroup 一个 ER 领域（领域内表 + 领域内直接关系）。
type erDomainGroup struct {
	name   string
	tables map[string]bool
	rels   []*domain.TableRelation
}

// splitERDomains 按领域归属分组直接关系（fk/query——同 renderERMermaid
// 口径）：**R34 统一消费 wiki.yaml domains**（表归属优先 domains.tables，
// 未覆盖的表走表名前缀降级）；领域内边进组、跨领域边返回（领域间图
// 数据源）。
func splitERDomains(rels []*domain.TableRelation, hideTable map[string]bool, doms []wikiDomainCfg) ([]*erDomainGroup, []*domain.TableRelation) {
	groups := map[string]*erDomainGroup{}
	var order []string
	var cross []*domain.TableRelation
	// R34：domains 表归属索引（表名 → 域名）
	tableDomain := map[string]string{}
	for _, d := range doms {
		for _, t := range d.Tables {
			tableDomain[t] = d.Name
		}
	}
	domainOf := func(table string) string {
		if n := tableDomain[table]; n != "" {
			return n
		}
		return splitTableDomain(table)
	}
	for _, r := range rels {
		if r.Type != domain.RelationFK && r.Type != domain.RelationQuery {
			continue
		}
		if hideTable[r.FromTable] || hideTable[r.ToTable] {
			continue
		}
		df, dt := domainOf(r.FromTable), domainOf(r.ToTable)
		if df == dt {
			g := groups[df]
			if g == nil {
				g = &erDomainGroup{name: df, tables: map[string]bool{}}
				groups[df] = g
				order = append(order, df)
			}
			g.rels = append(g.rels, r)
			g.tables[r.FromTable] = true
			g.tables[r.ToTable] = true
		} else {
			cross = append(cross, r)
		}
	}
	sort.Strings(order)
	doms2 := make([]*erDomainGroup, 0, len(order))
	for _, n := range order {
		doms2 = append(doms2, groups[n])
	}
	return doms2, cross
}

// erCrossMermaid 领域间关系图（R50 用户要求：只标领域 + 领域间直接
// 关系——表级细节聚合到领域级；此前表级标注（表/列/fk 类型）内容
// 太多）。领域名：domains.tables 归属优先，未覆盖走表前缀降级。
func erCrossMermaid(cross []*domain.TableRelation, doms []wikiDomainCfg) string {
	type key struct{ from, to string }
	// 表名 → 领域（domains.tables 优先 + 前缀降级——同 splitERDomains）
	tableDomain := map[string]string{}
	for _, d := range doms {
		for _, t := range d.Tables {
			tableDomain[t] = d.Name
		}
	}
	domainOf := func(table string) string {
		if n := tableDomain[table]; n != "" {
			return n
		}
		return splitTableDomain(table)
	}
	counts := map[key]int{}
	seen := map[string]bool{}
	for _, r := range cross {
		df, dt := domainOf(r.FromTable), domainOf(r.ToTable)
		if df == dt {
			continue // 已聚合到领域内
		}
		counts[key{df, dt}]++
		seen[df] = true
		seen[dt] = true
	}
	if len(counts) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("graph LR\n")
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		b.WriteString(fmt.Sprintf("  %s[\"%s\"]\n", archDomainID(n), n))
	}
	keys := make([]key, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].from != keys[j].from {
			return keys[i].from < keys[j].from
		}
		return keys[i].to < keys[j].to
	})
	for _, k := range keys {
		b.WriteString(fmt.Sprintf("  %s -->|%d| %s\n", archDomainID(k.from), counts[k], archDomainID(k.to)))
	}
	return b.String()
}

// renderERDomainsMD ER 领域分组区块（md）：领域间图 + 每领域内部图
// （折叠 details）。无分组价值（≤1 领域且无跨域边）返回空。
func renderERDomainsMD(rels []*domain.TableRelation, hideTable map[string]bool, rc *wikiRenderCtx) string {
	doms, cross := splitERDomains(rels, hideTable, rc.cfg.Domains)
	if len(doms) <= 1 && len(cross) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n## ER 图（按业务领域分组）\n\n")
	b.WriteString("> 表名前缀（_ 前首段）分领域；领域间图 + 每领域内部图分开。\n\n")
	if len(cross) > 0 {
		b.WriteString("### 领域间关系\n\n")
		b.WriteString(rc.diagramMD(erCrossMermaid(cross, rc.cfg.Domains)))
	}
	for _, d := range doms {
		b.WriteString(fmt.Sprintf("### 领域 <code>%s</code>（%d 张表，%d 条关系）\n\n",
			d.name, len(d.tables), len(d.rels)))
		if len(d.rels) == 0 {
			b.WriteString("（领域内无直接键关联）\n\n")
			continue
		}
		b.WriteString(rc.diagramMD(renderERMermaid(d.rels, nil)))
	}
	return b.String()
}

// renderERDomainsHTML ER 领域分组区块（html）：领域间图 + 每领域内部图
// （折叠——同 F2 实体图模式）。
func renderERDomainsHTML(rels []*domain.TableRelation, hideTable map[string]bool, rc *wikiRenderCtx) string {
	doms, cross := splitERDomains(rels, hideTable, rc.cfg.Domains)
	if len(doms) <= 1 && len(cross) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<section id="er-domains"><h2>ER 图（按业务领域分组）</h2><p class="muted">业务域划分（AI 分析 → wiki.yaml domains）；领域间图 + 每领域内部图分开。</p>`)
	if len(cross) > 0 {
		b.WriteString("<h3>领域间关系</h3>" + rc.diagramHTML(erCrossMermaid(cross, rc.cfg.Domains)))
	}
	for i, d := range doms {
		id := fmt.Sprintf("er-dom-%d", i)
		b.WriteString(fmt.Sprintf(`<h4 class="fold-btn" data-target="%s" data-label="1">▸ 领域 %s（%d 张表，%d 条关系）——展开查看</h4><div class="sec-body" id="%s" style="display:none">`,
			id, htmlEsc(d.name), len(d.tables), len(d.rels), id))
		if len(d.rels) == 0 {
			b.WriteString(`<p class="muted">（领域内无直接键关联）</p>`)
		} else {
			b.WriteString(rc.diagramHTML(renderERMermaid(d.rels, nil)))
		}
		b.WriteString("</div>")
	}
	b.WriteString("</section>")
	return b.String()
}

// diagramEdgeCount mermaid 文本边数（--> 与 ||-- 计数——方案 A 阈值检测）。
func diagramEdgeCount(m string) int {
	return strings.Count(m, "-->") + strings.Count(m, "||--")
}

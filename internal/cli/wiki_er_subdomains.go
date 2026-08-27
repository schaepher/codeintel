package cli

// R78 ER 域内图超限细分（待办 14——复用 R63 实体子域思路）：ER 领域
// 分组后某域内关系仍超 mermaid 上限（500）时，按表二级前缀再细分
// （item_order_x → item_order）——子域间图 + 每子域内部图（折叠）。

import (
	"fmt"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// erSubName 表子域归属：≥3 段取二级前缀（item_order_detail →
// item_order）；2 段/无 _ 回退一级前缀（member_a → member——与
// splitTableDomain 同口径，两段表名的二级前缀就是一级）。
func erSubName(table string) string {
	parts := strings.SplitN(table, "_", 3)
	if len(parts) == 3 {
		return parts[0] + "_" + parts[1]
	}
	return splitTableDomain(table)
}

// erSubOf 表 → 子域归属：yaml subdomains[].tables 精确匹配优先
// （R80——AI 语义子域），未覆盖走二级前缀降级（erSubName）。
func erSubOf(table string, subTables map[string]string) string {
	if v, ok := subTables[table]; ok {
		return v
	}
	return erSubName(table)
}

// splitERSubDomains 领域内关系按表子域分组（R78 二级前缀自动 + R80
// yaml subdomains 优先）：两端子域相同 → 组内边；不同 → 跨子域边
// （子域间图数据源）。组按子域名排序（确定性）。subTables 为表 →
// 子域名索引（yaml 配置；nil = 纯自动降级）。
func splitERSubDomains(rels []*domain.TableRelation, subTables map[string]string) ([]*erDomainGroup, []*domain.TableRelation) {
	groups := map[string]*erDomainGroup{}
	var order []string
	var cross []*domain.TableRelation
	for _, r := range rels {
		sf, st := erSubOf(r.FromTable, subTables), erSubOf(r.ToTable, subTables)
		if sf == st {
			g := groups[sf]
			if g == nil {
				g = &erDomainGroup{name: sf, tables: map[string]bool{}}
				groups[sf] = g
				order = append(order, sf)
			}
			g.rels = append(g.rels, r)
			g.tables[r.FromTable] = true
			g.tables[r.ToTable] = true
		} else {
			cross = append(cross, r)
		}
	}
	sort.Strings(order)
	subs := make([]*erDomainGroup, 0, len(order))
	for _, n := range order {
		subs = append(subs, groups[n])
	}
	return subs, cross
}

// erSubCrossMermaid 子域间关系图（领域级聚合——节点 = 子域，边 =
// 子域间关系计数；erCrossMermaid 同款形态，domainOf 换成 erSubOf——
// yaml subdomains 优先，未覆盖二级前缀）。
func erSubCrossMermaid(cross []*domain.TableRelation, subTables map[string]string) string {
	type key struct{ from, to string }
	counts := map[key]int{}
	seen := map[string]bool{}
	for _, r := range cross {
		df, dt := erSubOf(r.FromTable, subTables), erSubOf(r.ToTable, subTables)
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

// erSubTablesFor yaml subdomains 表归属索引（表名 → 子域名）——
// 域名匹配 rc.cfg.Domains[].Name；无配置返回空（纯自动降级）。
func erSubTablesFor(rc *wikiRenderCtx, domainName string) map[string]string {
	out := map[string]string{}
	for _, d := range rc.cfg.Domains {
		if d.Name != domainName {
			continue
		}
		for _, sd := range d.Subdomains {
			for _, t := range sd.Tables {
				out[t] = sd.Name
			}
		}
	}
	return out
}

// renderERSubDomainsMD 领域内图超限 → 子域细分（md；yaml subdomains
// 优先，未覆盖走表二级前缀降级）。
func renderERSubDomainsMD(d *erDomainGroup, rc *wikiRenderCtx) string {
	subs, cross := splitERSubDomains(d.rels, erSubTablesFor(rc, d.name))
	var b strings.Builder
	b.WriteString(fmt.Sprintf("**子域分组**（领域 %s 内图 %d 条边超限（上限 %d）——域过大，按表二级前缀细分）：\n\n",
		d.name, len(d.rels), mermaidEdgeLimit))
	if m := erSubCrossMermaid(cross, erSubTablesFor(rc, d.name)); m != "" {
		b.WriteString("子域间关系：\n\n")
		b.WriteString(rc.diagramMD(m))
	}
	for _, sd := range subs {
		b.WriteString(fmt.Sprintf("<details><summary>子域 <code>%s</code>（%d 张表，%d 条关系）</summary>\n\n",
			sd.name, len(sd.tables), len(sd.rels)))
		if len(sd.rels) == 0 {
			b.WriteString("（子域内无直接键关联）\n\n")
		} else if len(sd.rels) > mermaidEdgeLimit {
			// R100 待办14-④：子域内仍超限——不再细分（表级粒度末端），
			// 提示 + 表级统计（不渲染超限图）
			b.WriteString(fmt.Sprintf("（子域 %s 关系 %d 条仍超上限——最热表对：%s）\n\n",
				sd.name, len(sd.rels), topTablePairs(sd.rels, 10)))
		} else {
			b.WriteString(rc.diagramMD(renderERMermaid(sd.rels, nil)))
		}
		b.WriteString("</details>\n\n")
	}
	return b.String()
}

// topTablePairs 子域内最热表对（按条目数聚合降序——超限子域的表级
// 统计，防超限图渲染）。
func topTablePairs(rels []*domain.TableRelation, n int) string {
	type key struct{ from, to string }
	counts := map[key]int{}
	for _, r := range rels {
		k := key{r.FromTable, r.ToTable}
		if k.from > k.to {
			k.from, k.to = k.to, k.from
		}
		counts[k]++
	}
	pairs := make([]string, 0, len(counts))
	for k, c := range counts {
		pairs = append(pairs, fmt.Sprintf("%s↔%s×%d", k.from, k.to, c))
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i] > pairs[j] })
	if len(pairs) > n {
		pairs = pairs[:n]
	}
	return strings.Join(pairs, "、")
}

// renderERSubDomainsHTML 领域内图超限 → 子域细分（html 折叠版）。
func renderERSubDomainsHTML(d *erDomainGroup, rc *wikiRenderCtx, baseID string) string {
	subs, cross := splitERSubDomains(d.rels, erSubTablesFor(rc, d.name))
	var b strings.Builder
	b.WriteString(fmt.Sprintf(`<p class="muted">子域分组（领域 %s 内图 %d 条边超限——域过大，按表二级前缀细分）：</p>`,
		htmlEsc(d.name), len(d.rels)))
	if m := erSubCrossMermaid(cross, erSubTablesFor(rc, d.name)); m != "" {
		b.WriteString("<p class=\"muted\">子域间关系：</p>" + rc.diagramHTML(m))
	}
	for i, sd := range subs {
		sid := fmt.Sprintf("%s-sub-%d", baseID, i)
		inner := "（子域内无直接键关联）"
		if len(sd.rels) > mermaidEdgeLimit {
			// R100 待办14-④：子域内仍超限——提示 + 表级统计（不渲染超限图）
			inner = fmt.Sprintf(`<p class="muted">子域 %s 关系 %d 条仍超上限——最热表对：%s</p>`,
				htmlEsc(sd.name), len(sd.rels), htmlEsc(topTablePairs(sd.rels, 10)))
		} else if len(sd.rels) > 0 {
			inner = rc.diagramHTML(renderERMermaid(sd.rels, nil))
		}
		b.WriteString(fmt.Sprintf(`<h5 class="fold-btn" data-target="%s" data-label="1">▸ 子域 %s（%d 张表，%d 条关系）</h5><div class="sec-body" id="%s" style="display:none">%s</div>`,
			sid, htmlEsc(sd.name), len(sd.tables), len(sd.rels), sid, inner))
	}
	return b.String()
}

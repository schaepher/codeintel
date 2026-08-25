package cli

// F2：实体协作图按 DDD 领域分组——go2o 验收发现单图过大渲染失败。
// 策略：先找名为 domain/service 的目录（其后一段为业务子域）；程序
// 验证拆分有效性（组数 ≥2 且最大组占比 ≤80%）；目录结构不规范时
// 降级（module 相对第 2 段 / 第 1 段）。渲染：领域间图 + 每领域
// 内部图分开画分开展示。

import (
	"fmt"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// entityDomain 一个领域（子域）。
type entityDomain struct {
	Name   string               // 领域名（order/wallet/infra…）
	Nodes  []*domain.EntityNode // 该领域实体
	ByID   map[string]bool      // 实体 ID 集合（跨领域边判定用）
	Edges  []*domain.EntityEdge // 领域内部边（两端都在领域内）
}

// splitEntityDomains 实体按领域分组：**R34 统一消费 wiki.yaml domains**
// （包归属优先——domains.packages 短名匹配包路径；未覆盖走 DDD 子域
// 目录降级），程序验证有效性，逐级降级直到可用（单组=不分组）。
// R43（用户要求分领域间/领域内）：有显式 domains 配置时强制分组——
// 实体分布偏斜（go2o 实测实体集中在系统与平台域 → 80% 检查失败 → 整
// 图 753 行渲染崩溃）也要分；领域内大图由 mermaid 500 边降级兜底。
func splitEntityDomains(g *domain.EntityGraph, doms []wikiDomainCfg) []*entityDomain {
	// R34：包 → 域名索引（domains.packages 短名/完整路径——包路径包含匹配）
	domainOfPkg := func(pkg string) string {
		for _, d := range doms {
			for _, p := range d.Packages {
				short := p
				if i := strings.LastIndex(p, "/"); i >= 0 {
					short = p[i+1:]
				}
				if short != "" && (strings.HasSuffix(pkg, "/"+short) || pkg == short) {
					return d.Name
				}
			}
		}
		return ""
	}
	hasConfig := len(doms) > 0
	modRoot := pkgCommonPrefix(g)
	// lvl=2 默认（跳过 pkg/internal 等容器目录——go2o: pkg/infra → infra）；
	// 无效时降级 lvl=1
	for _, lvl := range []int{2, 1} {
		groups := map[string][]*domain.EntityNode{}
		for _, n := range g.Nodes {
			d := domainOfPkg(n.Pkg)
			if d == "" {
				d = domainOf(n.Pkg, modRoot, lvl)
			}
			groups[d] = append(groups[d], n)
		}
		if doms2 := buildDomains(g, groups); validSplit(doms2, hasConfig) {
			return doms2
		}
	}
	// 仍无效：整图一组
	groups := map[string][]*domain.EntityNode{"": g.Nodes}
	return buildDomains(g, groups)
}

// pkgCommonPrefix 实体包路径最长公共前缀，截断到最后一段前（module
// 根——go2o: github.com/ixre/go2o/pkg → github.com/ixre/go2o）。
func pkgCommonPrefix(g *domain.EntityGraph) string {
	if len(g.Nodes) == 0 {
		return ""
	}
	prefix := g.Nodes[0].Pkg
	for _, n := range g.Nodes[1:] {
		for !strings.HasPrefix(n.Pkg, prefix) {
			if i := strings.LastIndex(prefix, "/"); i >= 0 {
				prefix = prefix[:i]
			} else {
				return ""
			}
		}
	}
	if i := strings.LastIndex(prefix, "/"); i >= 0 {
		prefix = prefix[:i]
	}
	return prefix
}

// domainOf 包路径 → 领域名（第 lvl 级降级策略）：相对 module 的路径
// 段里找 domain/service（其后一段为子域）；无则取第 lvl 段。
func domainOf(pkg, modRoot string, lvl int) string {
	rel := strings.TrimPrefix(strings.TrimPrefix(pkg, modRoot), "/")
	seg := strings.Split(rel, "/")
	for i, s := range seg {
		if s == "domain" || s == "service" {
			if i+1 < len(seg) {
				return seg[i+1]
			}
			return seg[i]
		}
	}
	if lvl <= len(seg) {
		return seg[lvl-1]
	}
	return rel
}

// buildDomains 从分组构造领域（含领域内边）。
func buildDomains(g *domain.EntityGraph, groups map[string][]*domain.EntityNode) []*entityDomain {
	byID := map[string]*entityDomain{}
	var doms []*entityDomain
	for name, nodes := range groups {
		if len(nodes) == 0 {
			continue
		}
		d := &entityDomain{Name: name, Nodes: nodes, ByID: map[string]bool{}}
		for _, n := range nodes {
			d.ByID[n.ID] = true
		}
		byID[name] = d
		doms = append(doms, d)
	}
	// 领域内边：两端都在同一领域
	for _, e := range g.Edges {
		for _, d := range doms {
			if d.ByID[e.From] && d.ByID[e.To] {
				d.Edges = append(d.Edges, e)
				break
			}
		}
	}
	sort.Slice(doms, func(i, j int) bool { return doms[i].Name < doms[j].Name })
	return doms
}

// validSplit 拆分有效性：组数 ≥2；有显式 domains 配置时直接通过
// （R43：用户要求分领域间/领域内——配置即意图，实体偏斜也分，领域内
// 大图由 500 边降级兜底）；无配置（DDD 目录降级）保留最大组占比
// ≤80% 检查（防无效拆分）。
func validSplit(doms []*entityDomain, hasConfig bool) bool {
	if len(doms) < 2 {
		return false
	}
	if hasConfig {
		return true
	}
	total := 0
	max := 0
	for _, d := range doms {
		total += len(d.Nodes)
		if len(d.Nodes) > max {
			max = len(d.Nodes)
		}
	}
	return max*5 <= total*4 // max/total <= 0.8
}

// domainMermaid 领域间关系 mermaid：领域为节点（实体数），跨领域
// 实体边聚合为领域间边。
func domainMermaid(doms []*entityDomain, edges []*domain.EntityEdge) string {
	var b strings.Builder
	b.WriteString("graph LR\n")
	// 领域节点 + ID 映射（实体 ID → 领域名）
	domainOfID := map[string]string{}
	for i, d := range doms {
		id := fmt.Sprintf("D%d", i)
		b.WriteString(fmt.Sprintf("  %s[\"%s（%d 实体）\"]\n", id, d.Name, len(d.Nodes)))
		for _, n := range d.Nodes {
			domainOfID[n.ID] = id
		}
	}
	// 跨领域边聚合
	agg := map[string]int{}
	for _, e := range edges {
		from, ok1 := domainOfID[e.From]
		to, ok2 := domainOfID[e.To]
		if !ok1 || !ok2 || from == to {
			continue
		}
		agg[from+"|"+to] += e.Count
	}
	for pair, count := range agg {
		parts := strings.SplitN(pair, "|", 2)
		b.WriteString(fmt.Sprintf("  %s -->|%d| %s\n", parts[0], count, parts[1]))
	}
	return b.String()
}

// domainCount 各领域实体数摘要（渲染标题用）。
func domainCount(d *entityDomain) string {
	return itoa(len(d.Nodes)) + " 实体"
}

// itoa 简易整数转字符串。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

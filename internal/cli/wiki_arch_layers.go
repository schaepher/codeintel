package cli

// R44 三层架构图（用户要求：第 1 张架构图反映整个系统架构——从上到
// 下：接入层入口 → 领域 → 存储层；每层一个大 subgraph 框，三层框宽
// 对齐）。分层识别按包短名模式（零配置通用）；领域层用 domains 聚合
// 节点（有配置时）；跨层调用边聚合；每层加占位节点撑等宽（mermaid
// subgraph 宽度由内容决定——占位 label 固定长度对齐）。

import (
	"fmt"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// archAccessPkgs 接入层包短名（入口：命令层/HTTP 服务/app）。
var archAccessPkgs = map[string]bool{
	"cmd": true, "app": true, "cli": true, "server": true,
}

// archStoragePkgs 存储层包短名（存储/数据源/DAO）。
var archStoragePkgs = map[string]bool{
	"sqlite": true, "git": true, "db": true, "dao": true, "storage": true,
	"redis": true, "kafka": true, "cache": true, "clickhouse": true,
}

// archLayerName 包短名 → 层名（接入/存储；其余领域层）。
func archLayerName(short string) string {
	if archAccessPkgs[short] {
		return "接入层"
	}
	if archStoragePkgs[short] {
		return "存储层"
	}
	return "领域层"
}

// archPadLabel 占位节点 label（固定宽度撑 subgraph 等宽——R44 用户
// 要求三层框宽对齐）。
const archPadLabel = "　　　　　　　　　　　　　　"

// archLayeredMermaid 三层架构图 mermaid（graph TB 从上到下）：
// subgraph 接入层/领域层/存储层，各层节点 + 跨层/层内调用边。
// domains 非空时领域层 = 领域聚合节点（含包数）；否则领域层 = 包节点。
func archLayeredMermaid(data []*domain.WikiModule, doms []wikiDomainCfg) string {
	type key struct{ from, to string }
	counts := map[key]int{}
	// 包短名 → 层名 / 领域名（短名匹配——PkgCalls 的 From/To 是短名）
	pkgDomain := map[string]string{}
	for _, d := range doms {
		for _, p := range d.Packages {
			short := p
			if i := strings.LastIndex(p, "/"); i >= 0 {
				short = p[i+1:]
			}
			pkgDomain[short] = d.Name
		}
	}
	// 节点归类：短名 → 展示节点 id（接入/存储包原样；领域包 → 领域节点）
	nodeID := map[string]string{}
	var accessNodes, storageNodes []string
	accessSeen, storageSeen := map[string]bool{}, map[string]bool{}
	domainSeen := map[string]bool{}
	var domainOrder []string
	for _, wm := range data {
		for _, c := range wm.PkgCalls {
			for _, p := range []string{c.From, c.To} {
				if nodeID[p] != "" {
					continue
				}
				switch archLayerName(p) {
				case "接入层":
					if !accessSeen[p] {
						accessSeen[p] = true
						accessNodes = append(accessNodes, p)
					}
					nodeID[p] = p
				case "存储层":
					if !storageSeen[p] {
						storageSeen[p] = true
						storageNodes = append(storageNodes, p)
					}
					nodeID[p] = p
				default:
					if len(doms) > 0 {
						if d := pkgDomain[p]; d != "" && !domainSeen[d] {
							domainSeen[d] = true
							domainOrder = append(domainOrder, d)
							nodeID[p] = archDomainID(d)
						}
					} else {
						nodeID[p] = p
					}
				}
			}
		}
	}
	// 无 domains 时领域层 = 其余包节点（未归接入/存储的）
	var domainNodes []string
	if len(doms) == 0 {
		seen := map[string]bool{}
		for _, wm := range data {
			for _, c := range wm.PkgCalls {
				for _, p := range []string{c.From, c.To} {
					if seen[p] || nodeID[p] == "" {
						continue
					}
					if archLayerName(p) == "领域层" {
						seen[p] = true
						domainNodes = append(domainNodes, p)
					}
				}
			}
		}
	}
	// 边聚合（跨层与领域间）——from/to 的展示节点不同则计数
	for _, wm := range data {
		for _, c := range wm.PkgCalls {
			f, t := nodeID[c.From], nodeID[c.To]
			if f == "" || t == "" || f == t {
				continue
			}
			counts[key{f, t}] += c.Count
		}
	}
	if len(counts) == 0 {
		return ""
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
	var b strings.Builder
	b.WriteString("graph TB\n")
	// 接入层
	b.WriteString("  subgraph 接入层[接入层：入口]\n")
	for _, p := range accessNodes {
		b.WriteString("    " + archNode(p) + "\n")
	}
	b.WriteString("    padA[\"" + archPadLabel + "\"]\n")
	b.WriteString("  end\n")
	// 领域层
	b.WriteString("  subgraph 领域层[领域层]\n")
	if len(doms) > 0 {
		pkgCount := map[string]int{}
		for _, d := range doms {
			pkgCount[d.Name] = len(d.Packages)
		}
		for _, dn := range domainOrder {
			b.WriteString(fmt.Sprintf("    %s[\"%s（%d 包）\"]\n", archDomainID(dn), dn, pkgCount[dn]))
		}
	} else {
		for _, p := range domainNodes {
			b.WriteString("    " + archNode(p) + "\n")
		}
	}
	b.WriteString("    padB[\"" + archPadLabel + "\"]\n")
	b.WriteString("  end\n")
	// 存储层
	b.WriteString("  subgraph 存储层[存储层]\n")
	for _, p := range storageNodes {
		b.WriteString("    " + archNode(p) + "\n")
	}
	b.WriteString("    padC[\"" + archPadLabel + "\"]\n")
	b.WriteString("  end\n")
	for _, k := range keys {
		b.WriteString(fmt.Sprintf("  %s -->|%d| %s\n", k.from, counts[k], k.to))
	}
	return b.String()
}

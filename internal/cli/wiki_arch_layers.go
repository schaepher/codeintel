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

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// archAccessPkgs 接入层包短名（入口：命令层/HTTP 服务/app）。
var archAccessPkgs = map[string]bool{
	"cmd": true, "app": true, "cli": true, "server": true,
}

// archStoragePkgs 存储层包短名（存储/数据源/DAO）。
// R82：加 repo/mss/orm/store——repository 模式/消息存储/ORM 访问层
// 是常见存储形态（go2o 实测 internal/impl/repo 是数据访问层）。
var archStoragePkgs = map[string]bool{
	"sqlite": true, "git": true, "db": true, "dao": true, "storage": true,
	"redis": true, "kafka": true, "cache": true, "clickhouse": true,
	"repo": true, "mss": true, "orm": true, "store": true,
}

// archForceTB 架构图方向强制从上到下（R82：第一张架构图严格 graph TB
// ——yaml architecture 手写可能是 LR/TD，统一替换首行）。
func archForceTB(mermaid string) string {
	if i := strings.Index(mermaid, "graph LR"); i >= 0 {
		return strings.Replace(mermaid, "graph LR", "graph TB", 1)
	}
	if i := strings.Index(mermaid, "graph TD"); i >= 0 {
		return strings.Replace(mermaid, "graph TD", "graph TB", 1)
	}
	return mermaid
}

// archPadLabel 占位节点 label（固定宽度撑 subgraph 等宽——R44 用户
// 要求三层框宽对齐）。
const archPadLabel = "　　　　　　　　　　　　　　"

// archLayeredMermaid 三层架构图 mermaid（graph TB 从上到下）：
// subgraph 接入层/领域层/存储层，各层节点 + 跨层/层内调用边。
// domains 非空时领域层 = 领域聚合节点（含包数）；否则领域层 = 包节点。
// R47：外部接口调用按服务聚合（grpc 服务名 / http host）为领域层右侧
// 节点（外部系统集成点），边 = 调用方领域 → 外部服务。acts nil（纯
// 函数测试）跳过外部节点；repo 仍用于接入层服务包识别（archSvcPkgs）。
func archLayeredMermaid(data []*domain.WikiModule, doms []wikiDomainCfg, repo *sqlite.Repo, acts *action.Actions) string {
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
	// R82：grpc/http 服务所在包 → 接入层（服务入口——不依赖短名约定）
	svcPkgs := archSvcPkgs(repo)
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
				switch archLayerOf(p, svcPkgs) {
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
					if archLayerOf(p, svcPkgs) == "领域层" {
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
		// 单域/无跨域边：领域包全折叠为同一领域节点（f==t 全被跳过）
		// → 降级包级模式（等同无 domains——域内包间调用可见；go2o
		// 多域不受影响）
		if len(doms) > 0 {
			return archLayeredMermaid(data, nil, repo, acts)
		}
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
	// R47：外部接口按服务聚合（grpc 服务名 / http host）→ 领域层右侧
	// 节点；边 = 调用方领域 → 外部服务（调用方包 → 领域映射）
	extNodeID := map[string]string{} // 服务名 → 外部节点 id
	var extNodes []string
	extEdges := map[string]int{} // 领域|外部节点 → 计数
	if acts != nil && len(doms) > 0 {
		if ext, err := acts.ExternalInterfaces(); err == nil {
			for _, ei := range ext.Interfaces {
				id := "EXT_" + mermaidID(ei.Service)
				if _, ok := extNodeID[ei.Service]; !ok {
					extNodeID[ei.Service] = id
					extNodes = append(extNodes, ei.Service)
				}
				for _, c := range ei.Callers {
					// 调用方包（完整路径）→ 领域（末段查 pkgDomain）
					short := c.Pkg
					if i := strings.LastIndex(short, "/"); i >= 0 {
						short = short[i+1:]
					}
					d := pkgDomain[short]
					if d == "" {
						continue
					}
					extEdges[d+"|"+ei.Service]++
				}
			}
		}
	}
	var b strings.Builder
	b.WriteString("graph TB\n")
	// 接入层
	b.WriteString("  subgraph 接入层[接入层：入口]\n")
	for _, p := range accessNodes {
		b.WriteString("    " + archNode(p) + "\n")
	}
	b.WriteString("    padA[\"" + archPadLabel + "\"]\n")
	b.WriteString("  end\n")
	// 领域层（领域节点 + 右侧外部接口节点）
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
	// 外部接口节点（领域层右侧——外部系统集成点）
	for _, svc := range extNodes {
		label := svc
		if label == "" {
			label = "外部系统"
		}
		b.WriteString(fmt.Sprintf("    %s[\"%s\"]\n", extNodeID[svc], label))
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
	// R47：调用方领域 → 外部接口节点边（领域层右侧）
	extKeys := make([]string, 0, len(extEdges))
	for k := range extEdges {
		extKeys = append(extKeys, k)
	}
	sort.Strings(extKeys)
	for _, k := range extKeys {
		parts := strings.SplitN(k, "|", 2)
		svc := parts[1]
		b.WriteString(fmt.Sprintf("  %s -->|%d| %s\n", archDomainID(parts[0]), extEdges[k], extNodeID[svc]))
	}
	return b.String()
}

// mermaidID 任意文本 → mermaid 安全节点 id（字母数字下划线——外部
// 服务名/域名含点横线）。
func mermaidID(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// ---- R7 AI 整理架构图 ----
// 自动包间调用聚合保留（archMermaidFallback）；此处为 AI 二次整理版：
// 过滤基础工具包（logging 无业务信息）与临时包（seed），按分层
// subgraph 分组（入口/核心/支撑）——结构更分明。规则固化（确定性，
// 不依赖运行时 LLM）。

// curatedDropPkgs 过滤的包（基础工具/临时——无业务信息）。
var curatedDropPkgs = map[string]bool{
	"logging": true, // 日志基础库：所有包都依赖，无业务信息
	"seed":    true, // 测试种子包（fixture 工具）
}

// curatedGroups 分层分组（顺序即 subgraph 展示顺序）。
var curatedGroups = []struct {
	Name string
	Pkgs []string
}{
	{"入口层", []string{"codeintel", "cli", "server"}},
	{"核心层", []string{"action", "ssa", "ast", "scip", "git", "orchestrator", "sqlite"}},
	{"支撑层", []string{"domain", "canonicalizer"}},
}

// archMermaidCurated AI 整理架构图：过滤 + 分层 subgraph + 调用边。
func archMermaidCurated(data []*domain.WikiModule) string {
	type key struct{ from, to string }
	counts := map[key]int{}
	groupOf := map[string]string{}
	for _, g := range curatedGroups {
		for _, p := range g.Pkgs {
			groupOf[p] = g.Name
		}
	}
	inGroup := func(p string) bool { return groupOf[p] != "" }
	for _, wm := range data {
		for _, c := range wm.PkgCalls {
			if curatedDropPkgs[c.From] || curatedDropPkgs[c.To] {
				continue
			}
			// 只保留已分组包的边（未识别包丢弃——AI 整理聚焦分层）
			if !inGroup(c.From) || !inGroup(c.To) {
				continue
			}
			counts[key{c.From, c.To}] += c.Count
		}
	}
	if len(counts) == 0 {
		return ""
	}
	// R42：分组规则硬编码 ana 自身包名（R7 定制）——不识别该项目时
	// 有效节点过少（go2o 实测只剩 domain 1 包 1 自环边）→ 降级返回空
	// （不显示贫瘠的 AI 整理版，保留自动聚合版 archMermaidFallback）
	usedNodes := map[string]bool{}
	for k := range counts {
		usedNodes[k.from] = true
		usedNodes[k.to] = true
	}
	if len(usedNodes) < 3 {
		return ""
	}
	// 确定性排序
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
	b.WriteString("graph LR\n")
	for _, g := range curatedGroups {
		// 组内节点（有边的）——subgraph 内列出
		var nodes []string
		for _, p := range g.Pkgs {
			used := false
			for k := range counts {
				if k.from == p || k.to == p {
					used = true
					break
				}
			}
			if used {
				nodes = append(nodes, p)
			}
		}
		if len(nodes) == 0 {
			continue
		}
		b.WriteString("  subgraph " + g.Name + "[" + g.Name + "]\n")
		for _, p := range nodes {
			b.WriteString("    " + archNode(p) + "\n")
		}
		b.WriteString("  end\n")
	}
	for _, k := range keys {
		b.WriteString(fmt.Sprintf("  %s -->|%d| %s\n", archNode(k.from), counts[k], archNode(k.to)))
	}
	return b.String()
}

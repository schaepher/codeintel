package cli

// R32 mermaid → plantuml 转换器（待办 2：wiki 图双引擎）。实测语法：
// - ER：plantuml 纯关系行（A ||--o{ B : "label"）自动建实体——删
//   erDiagram 头与实体名行，关系行两引擎语法相同
// - sequence：participant/消息行两引擎相同——删 sequenceDiagram 头
// - graph：节点行 X["label"] → node "label" as X；边行 -->|N| 保留
//   （entityMermaid/domainMermaid/moduleArchMermaid/archCurated/
//   entitySubgraphMermaid 全为 graph LR 格式——一次转换全覆盖）

import (
	"fmt"
	"regexp"
	"strings"
)

// mermaidToPlantuml mermaid 图文本 → plantuml 文本（按图类型分发）。
func mermaidToPlantuml(m string) string {
	switch {
	case strings.HasPrefix(m, "erDiagram"):
		return mermaidERToPlantuml(m)
	case strings.HasPrefix(m, "sequenceDiagram"):
		return mermaidSequenceToPlantuml(m)
	case strings.HasPrefix(m, "graph "):
		return mermaidGraphToPlantuml(m)
	}
	return ""
}

// mermaidERToPlantuml erDiagram → plantuml：删 erDiagram 头与实体名行
// （无关系符号的行——plantuml 关系行自动建实体）。
func mermaidERToPlantuml(m string) string {
	var b strings.Builder
	b.WriteString("@startuml\n")
	for _, l := range strings.Split(m, "\n") {
		t := strings.TrimSpace(l)
		if t == "" || t == "erDiagram" {
			continue
		}
		if !hasRelationSymbol(t) {
			continue // 实体名行——关系行自动建实体
		}
		b.WriteString(t + "\n")
	}
	b.WriteString("@enduml\n")
	return b.String()
}

// isSelfLoop 边行自环（A -->|N| A——plantuml 不支持）。
func isSelfLoop(line string) bool {
	i := strings.Index(line, "-->")
	if i < 0 {
		return false
	}
	from := strings.TrimSpace(line[:i])
	to := line[i+3:]
	if j := strings.Index(to, "|"); j >= 0 {
		to = to[j+1:]
	}
	if k := strings.Index(to, "|"); k >= 0 {
		to = to[k+1:]
	}
	return strings.TrimSpace(to) == from
}

// hasRelationSymbol 行含关系符号（|| -- |o o| ——ER 关系线）。
func hasRelationSymbol(s string) bool {
	return strings.Contains(s, "||") || strings.Contains(s, "--") ||
		strings.Contains(s, "|o") || strings.Contains(s, "o|")
}

// mermaidSequenceToPlantuml sequenceDiagram → plantuml（删头；participant
// 与消息行语法两引擎相同）。
func mermaidSequenceToPlantuml(m string) string {
	body := strings.TrimPrefix(m, "sequenceDiagram\n")
	return "@startuml\n" + body + "@enduml\n"
}

// mermaidGraphToPlantuml graph LR → plantuml：节点两种格式统一转换
// ——`X["label"]`（实体图）与 `cli[cli]`（模块架构图，无引号）→
// `node "label" as X` 定义 + 行内引用替换为纯 id；边行 -->|N| 保留。
func mermaidGraphToPlantuml(m string) string {
	var b strings.Builder
	b.WriteString("@startuml\n")
	defined := map[string]bool{}
	addNode := func(id, label string) {
		if defined[id] {
			return
		}
		defined[id] = true
		fmt.Fprintf(&b, "node \"%s\" as %s\n", label, id)
	}
	// id[label] / id["label"]——label 去引号；id 支持 Unicode（领域节点
	// D商品域——R34 架构图领域聚合实测中文 id 匹配不上致 label 丢失）
	nodeRe := regexp.MustCompile(`([\p{L}\p{N}_]+)\[(?:"([^"]*)"|([^\[\]]*))\]`)
	// -->|N| → --> N : （plantuml 冒号 label——实测 --check-syntax）
	arrowLabelRe := regexp.MustCompile(`-->\|([^|]+)\|`)
	// subgraph 名[名]（mermaid 分组标题）→ subgraph "名"
	subgraphRe := regexp.MustCompile(`subgraph ([^\[<]+)\[([^\]]*)\]`)
	for _, l := range strings.Split(m, "\n") {
		t := strings.TrimSpace(l)
		if t == "" || t == "graph LR" || t == "graph TD" {
			continue
		}
		// subgraph 行优先（`subgraph 名[名]` 的 [名] 与节点同格式——
		// nodeRe 先处理会吃掉 label 致 subgraphRe 匹配不到）
		if sg := subgraphRe.FindStringSubmatch(t); sg != nil {
			b.WriteString(`subgraph "` + sg[1] + `" {` + "\n")
			continue
		}
		// 先收集行内节点定义（边行端点），再输出替换后的行
		out := nodeRe.ReplaceAllStringFunc(t, func(match string) string {
			parts := nodeRe.FindStringSubmatch(match)
			id, label := parts[1], parts[2]
			if label == "" {
				label = parts[3]
			}
			addNode(id, label)
			return id
		})
		// 纯节点行（`X["label"]` 单独行——无边）→ node 已定义，跳过
		// 裸 id 行（plantuml 组件图不认裸标识符行）；end（subgraph 闭合）
		// 保留
		if !strings.Contains(out, "-->") && !strings.Contains(out, "subgraph") && strings.TrimSpace(out) != "end" {
			continue
		}
		// plantuml 不支持自环边（`ad -->|24| ad` 语法错误——go2o 同包
		// 调用聚合大量自环，实测报错）——过滤
		if isSelfLoop(out) {
			continue
		}
		// plantuml 边 label 语法修正（R38）：`A -->|6| B` 原转成
		// `A --> 6 : B`——plantuml 把 6 当目标节点名（数字节点！），
		// 长符号 ID 成了线标签（"线连接的是数字"实测）。正确：
		// `A --> B : 6`（label 移到行尾冒号后）
		labels := []string{}
		out = arrowLabelRe.ReplaceAllStringFunc(out, func(m string) string {
			mm := arrowLabelRe.FindStringSubmatch(m)
			if len(mm) > 1 {
				labels = append(labels, mm[1])
			}
			return "-->"
		})
		if len(labels) > 0 {
			out += " : " + strings.Join(labels, ", ")
		}
		b.WriteString(out + "\n")
	}
	b.WriteString("@enduml\n")
	return b.String()
}

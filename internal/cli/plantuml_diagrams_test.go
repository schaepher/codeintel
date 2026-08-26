package cli

// R32 mermaid → plantuml 转换器测试：三类图（ER 关系行/sequence/
// graph）转换 + 节点两种格式（引号/无引号方括号）。测试先行。

import (
	"strings"
	"testing"
)

// TestMermaidGraphToPlantuml：graph LR 转换——节点引号/无引号格式
// → node "label" as id；边 label 移到行尾冒号（R38：`A -->|6| B` →
// `A --> B : 6`——旧写法 `--> 6 : B` 把 6 当目标节点名，线连数字）。
func TestMermaidGraphToPlantuml(t *testing.T) {
	m := `graph LR
  cli[cli] -->|42| action[action]
  symbol_x["订单聚合"] -->|6| symbol_y["账户门面"]
  ad[ad] -->|24| ad[ad]
`
	out := mermaidGraphToPlantuml(m)
	for _, want := range []string{
		"@startuml",
		`node "cli" as cli`,
		`node "action" as action`,
		`node "订单聚合" as symbol_x`,
		`node "账户门面" as symbol_y`,
		"cli --> action : 42",
		"symbol_x --> symbol_y : 6",
		"@enduml",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("graph 转换缺 %q:\n%s", want, out)
		}
	}
	// 旧错误形态（数字当目标节点）不应出现
	if strings.Contains(out, "--> 6 : symbol_y") || strings.Contains(out, "--> 42 : action") {
		t.Errorf("边 label 应在行尾（旧写法数字节点）:\n%s", out)
	}
	if strings.Contains(out, "[cli]") || strings.Contains(out, `["`) {
		t.Errorf("转换后不应残留方括号节点:\n%s", out)
	}
	// 自环边（plantuml 不支持——语法错误）应过滤
	if strings.Contains(out, "ad -->|24| ad") {
		t.Errorf("自环边应过滤:\n%s", out)
	}
}

// TestMermaidERToPlantuml：ER 转换——删 erDiagram 头与实体名行，关系行保留。
func TestMermaidERToPlantuml(t *testing.T) {
	m := `erDiagram
    orders
    orders ||--o{ items : "order_id → id [fk]"
    items
`
	out := mermaidERToPlantuml(m)
	if strings.Contains(out, "erDiagram") || strings.Contains(out, "\n    orders\n") {
		t.Errorf("ER 转换应删 erDiagram 头与实体名行:\n%s", out)
	}
	for _, want := range []string{`orders ||--o{ items : "order_id → id [fk]"`} {
		if !strings.Contains(out, want) {
			t.Errorf("ER 转换缺关系行 %q:\n%s", want, out)
		}
	}
}

// TestMermaidGraphSubgraph：subgraph 分组行转换（mermaid 无 { → plantuml
// 需 {）；纯节点行不输出裸 id；end 保留。
func TestMermaidGraphSubgraph(t *testing.T) {
	m := `graph LR
  subgraph 支撑层[支撑层]
  node domain[domain]
  end
  node api[api]
`
	out := mermaidGraphToPlantuml(m)
	for _, want := range []string{`subgraph "支撑层" {`, `node "domain" as domain`, "\nend\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("subgraph 转换缺 %q:\n%s", want, out)
		}
	}
	// 纯节点行（node api[api] 无边）→ 不输出裸 id
	if strings.Contains(out, "\napi\n") {
		t.Errorf("纯节点行不应输出裸 id:\n%s", out)
	}
	if strings.Contains(out, "subgraph 支撑层[") {
		t.Errorf("subgraph 应转换（含 {）:\n%s", out)
	}
}

// TestMermaidSequenceToPlantuml：sequence 转换——删头；participant 转
// plantuml 形态（`participant "名字" as 别名`——R81 实测 plantuml 不认
// mermaid 的 `别名 as 名字` 顺序，只显示别名）；消息行保留。
func TestMermaidSequenceToPlantuml(t *testing.T) {
	m := `sequenceDiagram
  participant P0 as "cmdBatch"
  participant P1 as orderManagerImpl
  P0->>P1: call
`
	out := mermaidSequenceToPlantuml(m)
	if strings.Contains(out, "sequenceDiagram") {
		t.Errorf("sequence 转换应删头:\n%s", out)
	}
	for _, want := range []string{`participant "cmdBatch" as P0`, `participant "orderManagerImpl" as P1`, "P0->>P1: call", "@startuml", "@enduml"} {
		if !strings.Contains(out, want) {
			t.Errorf("sequence 转换缺 %q:\n%s", want, out)
		}
	}
}

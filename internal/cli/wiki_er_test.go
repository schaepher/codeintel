package cli

import (
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestWikiERMermaid：Q251 ER 图页面——erDiagram 实体/关系行渲染，
// fk/query 画线、write/read 剔除、隐藏表过滤、列级 label、确定性排序。
func TestWikiERMermaid(t *testing.T) {
	rels := []*domain.TableRelation{
		{FromTable: "orders", FromCol: "user_id", ToTable: "users", ToCol: "id", Type: domain.RelationFK, Hops: 1},
		{FromTable: "users", FromCol: "id", ToTable: "orders", ToCol: "user_id", Type: domain.RelationQuery, Hops: 2},
		{FromTable: "orders", FromCol: "total", ToTable: "audit", ToCol: "amount", Type: domain.RelationWrite, Hops: 1},
		{FromTable: "secret", FromCol: "x", ToTable: "users", ToCol: "id", Type: domain.RelationQuery, Hops: 3},
	}
	hide := map[string]bool{"secret": true}
	m := renderERMermaid(rels, hide)
	if !strings.HasPrefix(m, "erDiagram\n") {
		t.Errorf("应以 erDiagram 开头:\n%s", m)
	}
	for _, want := range []string{"    orders\n", "    users\n", "user_id → id [fk]", "id → user_id [query]"} {
		if !strings.Contains(m, want) {
			t.Errorf("ER 图应含 %q:\n%s", want, m)
		}
	}
	for _, bad := range []string{"secret", "write", "audit"} {
		if strings.Contains(m, bad) {
			t.Errorf("ER 图不应含 %q（write/隐藏表剔除）:\n%s", bad, m)
		}
	}

	if m2 := renderERMermaid(rels, hide); m2 != m {
		t.Errorf("ER 图输出不确定")
	}

	if e := renderERMermaid(nil, nil); e != "erDiagram\n" {
		t.Errorf("空关系应返回裸 erDiagram，got %q", e)
	}
}

// TestWikiERMermaidSpecialName：动态表名（go2o 实测 pt_%s——fmt.Sprintf
// 拼接）含 % 破坏 mermaid 语法 → 实体名清洗为下划线。
func TestWikiERMermaidSpecialName(t *testing.T) {
	rels := []*domain.TableRelation{
		{FromTable: "pt_%s", FromCol: "merchant_id", ToTable: "mch_merchant", ToCol: "id", Type: domain.RelationFK, Hops: 1},
	}
	m := renderERMermaid(rels, nil)
	if strings.Contains(m, "pt_%s") {
		t.Errorf("ER 图不得出现原始特殊表名 pt_%%s（%% 是 mermaid 语法错误）:\n%s", m)
	}
	for _, want := range []string{"    pt__s\n", "pt__s ||--o{ mch_merchant"} {
		if !strings.Contains(m, want) {
			t.Errorf("ER 图应含清洗后表名 %q:\n%s", want, m)
		}
	}
}

// TestWikiERPage：Q251 er.md 页面——erDiagram 代码块 + 关系明细表。
func TestWikiERPage(t *testing.T) {
	rels := []*domain.TableRelation{
		{FromTable: "orders", FromCol: "user_id", ToTable: "users", ToCol: "id", Type: domain.RelationFK, Hops: 1},
	}
	page := renderERPage(rels, nil, &wikiRenderCtx{Diagram: "mermaid"})
	for _, want := range []string{"# ER 图", "```mermaid", "erDiagram", "| orders | user_id | users | id | fk |", "tables.md"} {
		if !strings.Contains(page, want) {
			t.Errorf("er.md 应含 %q:\n%s", want, page)
		}
	}

	if p2 := renderERPage(nil, nil, &wikiRenderCtx{Diagram: "mermaid"}); !strings.Contains(p2, "无表间直接关联") {
		t.Errorf("空关系应提示无关联:\n%s", p2)
	}
}

// TestWikiPkgArch：Q251-A 模块页架构图改包级调用——线上标次数，
// 空调用返回空（区块显示提示）。
func TestWikiPkgArch(t *testing.T) {
	wm := &domain.WikiModule{PkgCalls: []*domain.WikiPkgCall{
		{From: "cli", To: "action", Count: 42},
		{From: "action", To: "sqlite", Count: 7},
	}}
	m := moduleArchMermaid(wm)
	for _, want := range []string{"graph LR", "cli[cli] -->|42| action[action]", "action[action] -->|7| sqlite[sqlite]"} {
		if !strings.Contains(m, want) {
			t.Errorf("包级架构图应含 %q:\n%s", want, m)
		}
	}
	if e := moduleArchMermaid(&domain.WikiModule{}); e != "" {
		t.Errorf("空调用应返回空串，got %q", e)
	}
}

// TestWikiSeqMermaidAlias：Q251 补——sequenceDiagram 参与者含括号
// 符号名（(Actions).BatchSymbols）→ 别名化（participant P0 as "..."），
// 消息行用别名（括号名直接渲染是语法错误）。
func TestWikiSeqMermaidAlias(t *testing.T) {
	steps := []domain.WikiSeqStep{
		{Caller: "cmdBatch", Callee: "(Actions).BatchSymbols"},
		{Caller: "(Actions).BatchSymbols", Callee: "(Repo).Save"},
	}
	m := sequenceMermaid(steps)
	for _, want := range []string{"sequenceDiagram", `participant P0 as "cmdBatch"`, `participant P1 as "(Actions).BatchSymbols"`,
		"P0->>P1: call", "P1->>P2: call"} {
		if !strings.Contains(m, want) {
			t.Errorf("sequence 应含 %q:\n%s", want, m)
		}
	}
	if strings.Contains(m, "(Actions).BatchSymbols->>") {
		t.Errorf("参与者不得直接出现括号名于消息行:\n%s", m)
	}
}

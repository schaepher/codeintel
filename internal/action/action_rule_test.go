package action

import (
	"errors"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// TestAddRuleParsing 规则表达式解析（Q220d 语法 + 旧箭头兼容）——
// P1-03：解析属业务规则，随 rule 命令逻辑迁 action。
func TestAddRuleParsing(t *testing.T) {
	cases := []struct {
		name                                   string
		from, to                               string
		wantFromT, wantFromC, wantToT, wantToC string
	}{
		{"模式规则（来源无表前缀）", "member_id", "mm_member.id", "", "member_id", "mm_member", "id"},
		{"显式列对", "mm_relation.member_id", "mm_member.id", "mm_relation", "member_id", "mm_member", "id"},
		{"目标列省略默认 id", "member_id", "mm_member", "", "member_id", "mm_member", "id"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rule, err := ParseRule(c.from, c.to)
			if err != nil {
				t.Fatalf("ParseRule: %v", err)
			}
			if rule.FromTable != c.wantFromT || rule.FromCol != c.wantFromC ||
				rule.ToTable != c.wantToT || rule.ToCol != c.wantToC {
				t.Errorf("rule = %+v, want %s.%s → %s.%s", rule,
					c.wantFromT, c.wantFromC, c.wantToT, c.wantToC)
			}
		})
	}

	// 非法形态：空串 / 多级点号 / 缺少目标
	for _, bad := range [][2]string{
		{"", "mm_member.id"},
		{"a.b.c", "mm_member.id"},
		{"member_id", ""},
		{"member_id", "a.b.c"},
	} {
		if _, err := ParseRule(bad[0], bad[1]); !errors.Is(err, ErrInvalidRule) {
			t.Errorf("ParseRule(%q, %q) err = %v, want ErrInvalidRule", bad[0], bad[1], err)
		}
	}
}

// TestAddRuleLegacyExpression 单参旧形态（"from x to y" / 箭头）——
// 解析入口集中在 action（cli 不再自己拆字符串）。
func TestAddRuleLegacyExpression(t *testing.T) {
	rule, err := ParseRuleExpr("from mm_relation.member_id to mm_member.id")
	if err != nil || rule.FromTable != "mm_relation" || rule.ToTable != "mm_member" {
		t.Errorf("from/to 形态 = %+v, %v", rule, err)
	}
	rule, err = ParseRuleExpr("member_id → mm_member.id")
	if err != nil || rule.FromCol != "member_id" || rule.ToTable != "mm_member" {
		t.Errorf("箭头形态 = %+v, %v", rule, err)
	}
	if _, err := ParseRuleExpr("garbage"); !errors.Is(err, ErrInvalidRule) {
		t.Errorf("garbage err = %v, want ErrInvalidRule", err)
	}
}

// TestRuleAddListRemoveRoundTrip 规则增删查全流程（仓储经 Reader 端口）。
func TestRuleAddListRemoveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	a := New(sqlite.NewRepo(db))

	rule, err := ParseRule("member_id", "mm_member.id")
	if err != nil {
		t.Fatal(err)
	}
	id, err := a.AddRelationRule(rule)
	if err != nil {
		t.Fatalf("AddRule: %v", err)
	}
	rules, err := a.ListRelationRules()
	if err != nil || len(rules) != 1 {
		t.Fatalf("ListRules = %+v, %v", rules, err)
	}
	if rules[0].FromCol != "member_id" || rules[0].ToTable != "mm_member" {
		t.Errorf("rule = %+v", rules[0])
	}
	if err := a.RemoveRelationRule(id); err != nil {
		t.Fatalf("RemoveRule: %v", err)
	}
	if rules, err = a.ListRelationRules(); err != nil || len(rules) != 0 {
		t.Errorf("after remove = %+v, %v", rules, err)
	}
}

// TestRuleKindLabel 规则分类文案（模式/显式）——展示口径同源，避免
// cli 与未来消费者各写一份。
func TestRuleKindLabel(t *testing.T) {
	mode := domain.RelationRule{FromCol: "member_id", ToTable: "mm_member", ToCol: "id"}
	if got := RuleKindLabel(mode); got != "模式" {
		t.Errorf("mode label = %q", got)
	}
	explicit := domain.RelationRule{FromTable: "t", FromCol: "member_id", ToTable: "mm_member", ToCol: "id"}
	if got := RuleKindLabel(explicit); got != "显式" {
		t.Errorf("explicit label = %q", got)
	}
}

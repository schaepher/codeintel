package action

// P1-03 迁移：表间关联人工规则（codeintel rule add|list|remove）的命令
// 逻辑——规则表达式解析 + 增删查编排。原实现散在 cli/rule.go（解析）
// 与 sqlite.Repo（存储）；组合口径属业务规则，收口到 action，cli 只留
// 参数解析与结果渲染。
//
// 规则存 relation_rules 表（clean/reindex 保留），生效时校验目标表/列
// 真实存在（不存在静默跳过），生成关系 type=fk（ER 默认显示）。

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// ErrInvalidRule 规则表达式不合法（cli 据此返回参数错误退出码 2）。
var ErrInvalidRule = errors.New("invalid relation rule expression")

// ParseRule 由两个位置参数构造规则：
//
//	member_id mm_member.id       模式规则：所有含 member_id 列的表 → mm_member.id
//	mm_relation.member_id mm_member.id  显式列对（单对）
//	member_id mm_member          目标列省略时默认 id
func ParseRule(from, to string) (domain.RelationRule, error) {
	from, to = strings.TrimSpace(from), strings.TrimSpace(to)
	if from == "" || to == "" || strings.Count(from, ".") > 1 || strings.Count(to, ".") > 1 {
		return domain.RelationRule{}, fmt.Errorf("%w: %q → %q", ErrInvalidRule, from, to)
	}
	var rule domain.RelationRule
	if i := strings.Index(from, "."); i >= 0 {
		rule.FromTable, rule.FromCol = from[:i], from[i+1:]
	} else {
		rule.FromCol = from
	}
	if i := strings.Index(to, "."); i >= 0 {
		rule.ToTable, rule.ToCol = to[:i], to[i+1:]
	} else {
		rule.ToTable, rule.ToCol = to, "id"
	}
	if rule.FromCol == "" || rule.ToTable == "" || rule.ToCol == "" {
		return domain.RelationRule{}, fmt.Errorf("%w: %q → %q", ErrInvalidRule, from, to)
	}
	return rule, nil
}

// ParseRuleExpr 解析单参旧形态（Q220d 兼容）：
//
//	"from A.x to B.y"   显式列对
//	"from x to B.y"     模式规则
//	"from x to B"       目标列省略默认 id
//
// 大小写不敏感（FROM/TO 亦可）；兼容旧箭头形态 "A.x → B.y"（-> 亦可）。
func ParseRuleExpr(expr string) (domain.RelationRule, error) {
	up := strings.ToUpper(strings.TrimSpace(expr))
	var from, to string
	if i := strings.Index(up, " TO "); i >= 0 && strings.HasPrefix(up, "FROM ") {
		from = strings.TrimSpace(expr[len("FROM "):i])
		to = strings.TrimSpace(expr[i+len(" TO "):])
	} else {
		arrow := "->"
		if j := strings.Index(expr, "→"); j >= 0 {
			arrow = "→"
		}
		if j := strings.Index(expr, arrow); j >= 0 {
			from = strings.TrimSpace(expr[:j])
			to = strings.TrimSpace(expr[j+len(arrow):])
		}
	}
	if from == "" || to == "" {
		return domain.RelationRule{}, fmt.Errorf("%w: %q", ErrInvalidRule, expr)
	}
	return ParseRule(from, to)
}

// ParseRuleID 解析规则 id（rule remove <id>）。
func ParseRuleID(s string) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: 规则 id %q 不是整数", ErrInvalidRule, s)
	}
	return id, nil
}

// RuleKindLabel 规则分类文案：来源列带表前缀为"显式"，否则"模式"
// （模式规则对所有含该列的表生效）。
func RuleKindLabel(rule domain.RelationRule) string {
	if rule.FromTable != "" {
		return "显式"
	}
	return "模式"
}

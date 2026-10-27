package cli

// Q220c/Q220d `codeintel rule add|list|remove`——表间关联人工规则。
// P1-03：规则表达式解析与增删查编排在 action（action_rule.go）；
// cli 只做参数解析 + 结果渲染。
//
//	codeintel rule add <from> <to> --repo <path>  添加规则（两个位置参数）：
//	      member_id mm_member.id        模式规则：所有含 member_id 列的表 → mm_member.id
//	      mm_relation.member_id mm_member.id  显式列对（单对）
//	      目标列省略时默认 id；输出用箭头（→）；单参形态兼容旧解析
//	codeintel rule list [--json] --repo <path>  列出规则
//	codeintel rule remove <id> --repo <path>    删除规则

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
	"go.uber.org/zap"
)

// cmdRule 用户连线规则管理（Q220c/Q220d）。
func cmdRule(args []string) int {
	logger := zap.L()
	logger.Debug("enter cmdRule")
	defer logger.Debug("exit cmdRule")
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "用法: codeintel rule add|list|remove …")
		return 2
	}
	// Q237：--repo 缺省当前目录（parseRepoFlag 默认 "."）
	repoDir, rest, err := parseRepoFlag(args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	db, err := sqlite.Open(repoDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer db.Close()
	acts := action.New(sqlite.NewRepo(db))

	switch args[0] {
	case "add":
		return ruleAdd(acts, rest)
	case "list":
		return ruleList(acts, rest)
	case "remove":
		return ruleRemove(acts, rest)
	default:
		fmt.Fprintf(os.Stderr, "unknown rule subcommand: %s\n", args[0])
		return 2
	}
}

// ruleAdd 添加规则（Q220d：from/to 分为两个位置参数，输出用箭头）：
//
//	codeintel rule add member_id mm_member.id           模式规则（所有含 member_id 列的表 → mm_member.id）
//	codeintel rule add mm_relation.member_id mm_member.id   显式列对（单对）
//
// 目标列省略时默认 id（to 传 mm_member 亦可）。单参数形态兼容旧解析
// （"from x to y" / "x → y"）。输出用箭头（→）。
func ruleAdd(acts *action.Actions, args []string) int {
	logger := zap.L()
	logger.Debug("enter ruleAdd")
	defer logger.Debug("exit ruleAdd")
	var positional []string
	for _, a := range args {
		if a == "--json" {
			continue
		}
		positional = append(positional, a)
	}
	var rule domain.RelationRule
	var err error
	switch {
	case len(positional) >= 2:
		rule, err = action.ParseRule(positional[0], positional[1])
	case len(positional) == 1:
		// 兼容旧单参形态（"from x to y" / "x → y"）
		rule, err = action.ParseRuleExpr(positional[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "规则表达式无效: %q（用法：rule add <from> <to>，如 rule add member_id mm_member.id）\n", positional[0])
			return 2
		}
	default:
		fmt.Fprintln(os.Stderr, "用法: codeintel rule add <from> <to> --repo <path>（如 rule add member_id mm_member.id）")
		return 2
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "规则表达式无效: %v\n", err)
		return ruleErrCode(err)
	}
	id, aerr := acts.AddRelationRule(rule)
	if aerr != nil {
		fmt.Fprintln(os.Stderr, aerr)
		return 1
	}
	fmt.Printf("已添加%s规则 #%d: %s.%s → %s.%s（生效时校验表/列存在）\n",
		action.RuleKindLabel(rule), id, rule.FromTable, rule.FromCol, rule.ToTable, rule.ToCol)
	return 0
}

// ruleList 列出规则（--json 输出 RelationRule 数组）。
func ruleList(acts *action.Actions, args []string) int {
	logger := zap.L()
	logger.Debug("enter ruleList")
	defer logger.Debug("exit ruleList")
	asJSON := false
	for _, a := range args {
		if a == "--json" {
			asJSON = true
		}
	}
	rules, err := acts.ListRelationRules()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if asJSON {
		b, err := json.Marshal(rules)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println(string(b))
		return 0
	}
	if len(rules) == 0 {
		fmt.Println("（无规则）")
		return 0
	}
	for _, ru := range rules {
		scope := ru.FromTable
		if scope == "" {
			scope = "*"
		}
		fmt.Printf("#%d  %s.%s → %s.%s\n", ru.ID, scope, ru.FromCol, ru.ToTable, ru.ToCol)
	}
	return 0
}

// ruleRemove 删除规则。
func ruleRemove(acts *action.Actions, args []string) int {
	logger := zap.L()
	logger.Debug("enter ruleRemove")
	defer logger.Debug("exit ruleRemove")
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "用法: codeintel rule remove <id> --repo <path>")
		return 2
	}
	id, err := action.ParseRuleID(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "规则 id 无效: %q\n", args[0])
		return ruleErrCode(err)
	}
	if err := acts.RemoveRelationRule(id); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("已删除规则 #%d\n", id)
	return 0
}

// ruleErrCode 规则解析错误 → 退出码（表达式不合法属参数错误=2）。
func ruleErrCode(err error) int {
	if errors.Is(err, action.ErrInvalidRule) {
		return 2
	}
	return 1
}

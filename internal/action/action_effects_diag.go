package action

import "fmt"

// effectPartialCodes 判为 partial 的诊断代码。
var effectPartialCodes = map[string]bool{
	EffectDiagPathBudget:       true,
	EffectDiagPathNesting:      true,
	EffectDiagLoopUnrefined:    true,
	EffectDiagSwitchUnrefine:   true,
	EffectDiagTargetUnknown:    true,
	EffectDiagSlotUnresolved:   true,
	EffectDiagCalleeUnresolved: true,
}

// effectAnalysis 聚合完整性诊断（未知/截断 → partial）。
func effectAnalysis(depth int, st *pathWalkStats, tgt *effectsTarget, writes []DBEffect, unresolvedCalls int) *EffectsAnalysis {
	a := &EffectsAnalysis{Status: "complete", Depth: depth, Diagnostics: []SeqDiagCount{}}
	add := func(code, detail string, count int) {
		if count <= 0 {
			return
		}
		a.Diagnostics = append(a.Diagnostics, SeqDiagCount{Code: code, Count: count, Detail: detail})
		if effectPartialCodes[code] {
			a.Status = "partial"
		}
	}
	if st != nil {
		if st.budgetLimit > 0 && st.frontier > 0 {
			extra := "unknown"
			if !st.extraUnknown {
				extra = itoaSeq(st.dropped)
			}
			add(EffectDiagPathBudget,
				fmt.Sprintf("路径预算已达上限：已保留/上限=%d/%d；停止位置=%s:%d；未展开范围=%s；未展开前沿=%d；由该前沿可产生的额外终态总数=%s（低预算不可证明具体数量）；用 --path-budget 提高预算",
					st.budgetUsed, st.budgetLimit, st.stopFile, st.stopLine, st.stopScope, st.frontier, extra),
				st.frontier)
			a.PathBudget = &EffectsPathBudget{Used: st.budgetUsed, Limit: st.budgetLimit,
				StopFile: st.stopFile, StopLine: st.stopLine, Scope: st.stopScope,
				Frontier: st.frontier, ExtraTerminals: extra}
		} else {
			add(EffectDiagPathBudget,
				fmt.Sprintf("路径预算已达上限（丢弃 %d 个未展开终态；用 --path-budget 提高预算）", st.dropped), st.dropped)
		}
		add(EffectDiagPathNesting, "分支嵌套超过 12 层，未继续展开", st.nestTrunc)
		add(EffectDiagLoopUnrefined, "循环体按 0..N 次计（未逐次展开）", st.loops)
		add(EffectDiagSwitchUnrefine, "switch/select 分支未细化为独立路径", st.switches)
	}
	add("path_callee_unresolved", "路径上存在未映射索引的调用点（被调写入可能遗漏）", unresolvedCalls)
	if tgt != nil {
		add(EffectDiagTargetUnknown,
			"目标依赖里有类型无法判定的写入（已保守保留，未证明与目标同类型）", tgt.unknown)
		add(EffectDiagSlotUnresolved,
			"部分路径的 return 未提供目标槽（多值调用/表达式个数不足）", tgt.slotMiss)
		add(EffectDiagTargetNoWrite, "目标合法但未观察到写入", boolCount(len(writes) == 0))
	}
	return a
}

// setEffectCommitScope 标注每个写点是否落在观察到的 begin/commit 范围内
// （只陈述事实：不在范围内/无事务 → outside_or_unknown / none）。
func setEffectCommitScope(writes []DBEffect, tx *TxScope) {
	for i := range writes {
		switch {
		case tx == nil || tx.Kind == "none":
			writes[i].CommitScope = "none"
		case tx.Kind == "begin_commit" && writes[i].Line >= tx.BeginLine &&
			(tx.CommitLine <= 0 || writes[i].Line < tx.CommitLine):
			writes[i].CommitScope = "begin_commit"
		default:
			writes[i].CommitScope = "outside_or_unknown"
		}
	}
}

// boolCount 条件计数（true = 1）。
func boolCount(b bool) int {
	if b {
		return 1
	}
	return 0
}

// txCovers 写点是否落在 [begin, commit) 之间（事务范围判定的事实陈述）。
func txCovers(tx *TxScope, writes []DBEffect) bool {
	if tx.BeginLine <= 0 {
		return false
	}
	end := tx.CommitLine
	if end <= 0 {
		end = int(^uint(0) >> 1)
	}
	covers := 0
	for _, w := range writes {
		if w.Line >= tx.BeginLine && w.Line < end {
			covers++
		}
	}
	return covers > 0
}

package cli

// todo10-7：`query effects <函数>`——按路径输出副作用与返回结果。
// 业务逻辑与结构化结果在 action（Actions.Effects），本层只渲染。

import (
	"fmt"
	"os"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
)

// queryEffectsOpts `query effects` 参数（cli 只做转发）。
type queryEffectsOpts struct {
	abs          string
	target       string
	depth        int
	scene        []string
	jsonOut      bool
	targetField  string // todo12-1：类型限定字段路径（互斥）
	targetReturn string // todo12-1：返回槽名字或 #序号
	pathBudget   int    // todo12-7：路径预算（0=默认）
	targetSet    bool   // 显式指定了目标（区分未指定/空值）
}

// queryEffects 实现 `query effects <函数> [--depth N] [--target-field ...]
// [--target-return ...] [--path-budget N] [--json]`。
func queryEffects(acts *action.Actions, o queryEffectsOpts) int {
	res, err := acts.Effects(action.EffectsRequest{Target: o.target, RepoAbs: o.abs, Depth: o.depth,
		Scene: action.NewScene(o.scene), TargetField: o.targetField,
		TargetReturn: o.targetReturn, TargetSet: o.targetSet, PathBudget: o.pathBudget})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if o.jsonOut {
		encodeJSON(res)
		return 0
	}
	fmt.Printf("== %s 副作用与返回 ==\n", shortSymbolNameID(res.Function))
	if res.File != "" {
		fmt.Printf("源码：%s\n", res.File)
	}
	// todo12-1/7：目标与分析完整性
	if res.TargetInfo != nil {
		switch res.TargetInfo.Kind {
		case "field":
			fmt.Printf("目标：字段 %s\n", res.TargetInfo.Field)
		case "return":
			name := res.TargetInfo.Name
			if name == "" {
				name = "（匿名）"
			}
			fmt.Printf("目标：返回值 %s（%s）\n", res.TargetInfo.Slot, name)
		}
	}
	if res.Analysis != nil {
		fmt.Printf("分析：%s（深度 %d）\n", res.Analysis.Status, res.Analysis.Depth)
		for _, d := range res.Analysis.Diagnostics {
			fmt.Printf("  · 诊断 %s ×%d", d.Code, d.Count)
			if d.Detail != "" {
				fmt.Printf("：%s", d.Detail)
			}
			fmt.Println()
		}
	}
	fmt.Println("\n数据库写入：")
	if len(res.DBWrites) == 0 {
		fmt.Println("  （未观察到字段写入）")
	}
	for _, w := range res.DBWrites {
		loc := w.Table + "." + w.Column
		if w.Table == "" {
			loc = "（无表列映射）"
		}
		file := w.File
		if file == "" || file == res.File {
			file = shortSymbolNameID(w.Function)
		}
		fmt.Printf("  · %s ← %s（%s:%d）%s%s\n", loc, fieldLeafText(w.Field), file, w.Line,
			effectEvidenceNote(w.Evidence), effectWriteKindNote(w))
	}
	fmt.Println("\n远程写（出站）：")
	if len(res.RemoteWrites) == 0 {
		fmt.Println("  （无 grpc/http 出站）")
	}
	for _, r := range res.RemoteWrites {
		target := r.Service
		if r.Method != "" {
			target += "." + r.Method
		}
		if r.Path != "" {
			target += " " + r.Path
		}
		fmt.Printf("  · %-4s %s（%s:%d）%s\n", r.Kind, target, r.Function, r.Line,
			effectEvidenceNote(r.Evidence))
	}
	fmt.Printf("\n事务范围：%s%s\n", res.Tx.Kind, txScopeNote(&res.Tx))
	fmt.Printf("\n返回结果：%s\n", strings.Join(res.Result.Returns, ", "))
	if len(res.Result.Slots) > 0 {
		fmt.Printf("  结果槽：%s\n", strings.Join(res.Result.Slots, ", "))
	}
	if len(res.Result.ErrorVars) > 0 {
		fmt.Printf("  错误变量：%s\n", strings.Join(res.Result.ErrorVars, ", "))
	}
	if len(res.Result.Suspicious) > 0 {
		fmt.Printf("  ⚠ 可疑（错误被覆盖/吞掉）：%s\n", strings.Join(res.Result.Suspicious, ", "))
	}
	if res.Result.Detail != "" {
		fmt.Printf("  依据：%s\n", res.Result.Detail)
	}
	// todo11-4：按执行路径（互斥分支各自一条；不可达路径标注依据）
	if len(res.Paths) > 0 {
		fmt.Println("\n执行路径：")
		for _, p := range res.Paths {
			mark := ""
			if !p.Reachable {
				mark = " ✗不可达"
			}
			fmt.Printf("  · %s%s", p.ID, mark)
			if len(p.Conditions) > 0 {
				fmt.Printf(" [%s]", strings.Join(p.Conditions, " && "))
			}
			fmt.Println()
			if len(p.Writes) > 0 {
				var ws []string
				for _, w := range p.Writes {
					loc := fieldLeafText(w.Field)
					if w.Table != "" {
						loc = w.Table + "." + w.Column
					}
					pos := fmt.Sprintf("(L%d)", w.Line)
					if w.File != "" && w.File != res.File {
						pos = fmt.Sprintf("(%s:%d)", w.File, w.Line)
					}
					ws = append(ws, loc+pos)
				}
				fmt.Printf("      写入：%s\n", strings.Join(ws, ", "))
				if len(p.Writes) > 0 {
					var kinds []string
					for _, w := range p.Writes {
						if w.Kind != "" {
							k := w.Kind
							if w.CommitScope == "begin_commit" {
								k += "（事务内）"
							} else if w.CommitScope == "outside_or_unknown" {
								k += "（事务外/未知）"
							}
							kinds = append(kinds, k)
						}
					}
					if len(kinds) > 0 {
						fmt.Printf("      写点类型：%s\n", strings.Join(kinds, ", "))
					}
				}
			}
			if len(p.Calls) > 0 {
				var cs []string
				for _, c := range p.Calls {
					mark := ""
					if c.Target {
						mark = " ⭑目标"
					}
					cs = append(cs, shortSymbolNameID(c.Function)+fmt.Sprintf("(L%d)%s", c.Line, mark))
				}
				fmt.Printf("      调用：%s\n", strings.Join(cs, ", "))
			}
			if len(p.Remote) > 0 {
				var rs []string
				for _, r := range p.Remote {
					rs = append(rs, r.Service+optionalMethod(r.Method))
				}
				fmt.Printf("      远程：%s\n", strings.Join(rs, ", "))
			}
			if p.Result != nil {
				slots := ""
				if len(p.Result.Slots) > 0 {
					slots = " [" + strings.Join(p.Result.Slots, ", ") + "]"
				}
				fmt.Printf("      返回：%s%s（L%d）\n", strings.Join(p.Result.Returns, ", "), slots, p.Result.Line)
				if p.Result.Detail != "" {
					fmt.Printf("      依据：%s\n", p.Result.Detail)
				}
			}
			for _, n := range p.Notes {
				fmt.Printf("      注：%s\n", n)
			}
		}
	}
	for _, d := range res.Diagnostics {
		fmt.Printf("\n诊断：%s ×%d\n", d.Code, d.Count)
	}
	return 0
}

// fieldLeafText 字段路径末段（展示用）。
func fieldLeafText(p string) string {
	if i := strings.LastIndex(p, "."); i >= 0 {
		return p[i+1:]
	}
	return p
}

// effectWriteKindNote 写点类型/提交范围（todo12-8 R2：字段赋值不冒充
// 数据库写入；事务范围只陈述观察到的事实）。
func effectWriteKindNote(w action.DBEffect) string {
	var parts []string
	if w.Kind != "" {
		parts = append(parts, w.Kind)
	}
	switch w.CommitScope {
	case "begin_commit":
		parts = append(parts, "事务内")
	case "outside_or_unknown":
		parts = append(parts, "事务外/未知")
	}
	if len(parts) == 0 {
		return ""
	}
	return " [" + strings.Join(parts, " · ") + "]"
}

// effectEvidenceNote 证据支持程度 + 原因（unverified 必须可见）。
func effectEvidenceNote(ev *action.SeqEvidence) string {
	if ev == nil {
		return ""
	}
	note := " [" + ev.Verdict
	if ev.Reason != "" {
		note += " · " + ev.Reason
	}
	return note + "]"
}

// txScopeNote 事务范围的说明（只陈述观察到的事实）。
func txScopeNote(tx *action.TxScope) string {
	var parts []string
	if tx.BeginLine > 0 {
		parts = append(parts, fmt.Sprintf("Begin@%d", tx.BeginLine))
	}
	if tx.CommitLine > 0 {
		parts = append(parts, fmt.Sprintf("Commit@%d", tx.CommitLine))
	}
	if tx.RollbackLine > 0 {
		parts = append(parts, fmt.Sprintf("Rollback@%d", tx.RollbackLine))
	}
	if tx.CoversWrites {
		parts = append(parts, "写点落在范围内（不代表失败必回滚）")
	}
	if tx.Detail != "" {
		parts = append(parts, tx.Detail)
	}
	if len(parts) == 0 {
		return ""
	}
	return "（" + strings.Join(parts, "；") + "）"
}

// optionalMethod 方法名片段（空则空串）。
func optionalMethod(m string) string {
	if m == "" {
		return ""
	}
	return "." + m
}

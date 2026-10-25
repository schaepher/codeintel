package cli

// todo10-7：`query effects <函数>`——按路径输出副作用与返回结果。
// 业务逻辑与结构化结果在 action（Actions.Effects），本层只渲染。

import (
	"fmt"
	"os"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
)

// queryEffects 实现 `query effects <函数> [--depth N] [--json]`。
func queryEffects(acts *action.Actions, abs, target string, depth int, scene []string, jsonOut bool) int {
	res, err := acts.Effects(action.EffectsRequest{Target: target, RepoAbs: abs, Depth: depth,
		Scene: action.NewScene(scene)})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if jsonOut {
		encodeJSON(res)
		return 0
	}
	fmt.Printf("== %s 副作用与返回 ==\n", shortSymbolNameID(res.Function))
	if res.File != "" {
		fmt.Printf("源码：%s\n", res.File)
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
		fmt.Printf("  · %s ← %s（%s:%d）%s\n", loc, fieldLeafText(w.Field), w.Function, w.Line,
			effectEvidenceNote(w.Evidence))
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
	if len(res.Result.ErrorVars) > 0 {
		fmt.Printf("  错误变量：%s\n", strings.Join(res.Result.ErrorVars, ", "))
	}
	if len(res.Result.Suspicious) > 0 {
		fmt.Printf("  ⚠ 可疑（错误被覆盖/吞掉）：%s\n", strings.Join(res.Result.Suspicious, ", "))
	}
	if res.Result.Detail != "" {
		fmt.Printf("  依据：%s\n", res.Result.Detail)
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

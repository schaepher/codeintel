package cli

// todo10-6：`query async <入口符号|表>`——异步生命周期关联（人工动作 /
// 内部审核 / 定时任务 / 消息消费者）与因果证据。业务逻辑与结构化结果在
// action（Actions.AsyncLifecycle），本层只做参数转发与渲染。

import (
	"fmt"
	"os"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
)

// queryAsync 实现 `query async <入口|表> [--json]`。
func queryAsync(acts *action.Actions, abs, seed string, jsonOut bool) int {
	res, err := acts.AsyncLifecycle(action.AsyncLifecycleRequest{Seed: seed, RepoAbs: abs})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if jsonOut {
		encodeJSON(res)
		return 0
	}
	fmt.Printf("== %s 异步生命周期 ==\n", seed)
	if res.Scope != "" {
		fmt.Printf("作用域：%s\n", prettyScope(res.Scope))
	}
	if len(res.Nodes) == 0 {
		fmt.Println("（未找到生命周期入口——该符号可能无字段读写摘要；试试表名或换入口）")
	}
	fmt.Println("\n入口：")
	for _, n := range res.Nodes {
		loc := n.File
		if n.Line > 0 {
			loc = fmt.Sprintf("%s:%d", n.File, n.Line)
		}
		fmt.Printf("  · %-10s %s（%s）%s\n", n.Role, n.Name, loc, asyncNodeVerdict(n))
	}
	if len(res.Links) > 0 {
		fmt.Println("\n关联（证据）：")
		for _, l := range res.Links {
			// todo11-3：把"启发式候选"与"经调用路径/值来源验证的因果"分开显示
			fmt.Printf("  · %s ↔ %s → %s [%s · %s]%s\n", shortSymbolNameID(l.From), shortSymbolNameID(l.To),
				l.Reason, l.Verdict, l.EvidenceKind, asyncLinkNote(l))
		}
	}
	if len(res.Diagnostics) > 0 {
		fmt.Println("\n诊断：")
		for _, d := range res.Diagnostics {
			fmt.Printf("  · %s ×%d\n", d.Code, d.Count)
		}
	}
	return 0
}

// asyncNodeVerdict 节点角色判定的支持程度标注（unverified 明示为启发式）。
func asyncNodeVerdict(n action.AsyncNode) string {
	if n.Verdict == action.SeqVerdictUnverified {
		return "（角色未核实：名字无特征词）"
	}
	if n.RoleRule != "" {
		return "（" + n.RoleRule + "）"
	}
	return ""
}

// asyncLinkNote 关联依据的补充说明（unverified 必须明说"不构成因果"）。
func asyncLinkNote(l action.AsyncLink) string {
	if l.Detail == "" {
		return ""
	}
	if strings.Contains(l.Detail, "不构成因果") {
		return " ⚠ " + l.Detail
	}
	return "（" + l.Detail + "）"
}

// prettyScope 作用域展示（去掉 canonical ID 前缀，包/类型名更好读）。
func prettyScope(scope string) string {
	scope = strings.TrimPrefix(scope, "symbol:go:")
	return strings.TrimSuffix(scope, ":")
}

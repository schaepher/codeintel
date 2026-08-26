package cli

// R76 `codeintel query sequence <symbol>`——任意函数/方法时序图：
// 调用链接口具体化（CalleesConcrete——wiki 流程页同款数据源）→ 步骤
// 列表（含调用行号）+ mermaid sequenceDiagram（--mermaid）。
// 独立于 wiki——任何符号可查。

import (
	"fmt"
	"os"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
)

// cmdQuerySequence 实现 `query sequence <symbol> [--depth N] [--mermaid]`。
func cmdQuerySequence(acts *action.Actions, target string, depth int, mermaid bool, jsonOut bool) int {
	n, err := acts.ResolveSymbol(target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if depth <= 0 {
		depth = 2
	}
	facts, err := acts.CalleesConcrete(n.ID, depth)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	steps := action.SortChainByCallLine(string(n.ID), facts)
	if jsonOut {
		encodeJSON(map[string]any{
			"symbol": shortSymbolName(n),
			"id":     string(n.ID),
			"depth":  depth,
			"steps":  steps,
		})
		return 0
	}
	if mermaid {
		fmt.Print(sequenceStepsMermaid(n, steps))
		return 0
	}
	fmt.Printf("== %s 时序（depth %d，接口已具体化） ==\n", shortSymbolName(n), depth)
	for i, st := range steps {
		fmt.Printf("%d. %s → %s\n", i+1, st.Caller, st.Callee)
	}
	if len(steps) == 0 {
		fmt.Println("（无项目内调用——可能仅调用外部库）")
	}
	return 0
}

// sequenceStepsMermaid 步骤 → mermaid sequenceDiagram（参与者 = 符号
// 短名；连续同向调用合并计数）。
func sequenceStepsMermaid(entry *domain.CodeEntity, steps []domain.WikiSeqStep) string {
	var b strings.Builder
	b.WriteString("sequenceDiagram\n")
	seen := map[string]bool{}
	for _, st := range steps {
		for _, p := range []string{st.Caller, st.Callee} {
			if !seen[p] {
				seen[p] = true
				b.WriteString(fmt.Sprintf("  participant %s as %s\n", mermaidID(p), p))
			}
		}
	}
	type pair struct{ from, to string }
	counts := map[pair]int{}
	var order []pair
	for _, st := range steps {
		p := pair{st.Caller, st.Callee}
		if counts[p] == 0 {
			order = append(order, p)
		}
		counts[p]++
	}
	for _, p := range order {
		label := "调用"
		if counts[p] > 1 {
			label = fmt.Sprintf("调用 ×%d", counts[p])
		}
		b.WriteString(fmt.Sprintf("  %s->>%s: %s\n", mermaidID(p.from), mermaidID(p.to), label))
	}
	return b.String()
}

package cli

// Q244 意图命令：`codeintel before <目标>`（改动影响预判）与
// `codeintel trace <目标>`（数据来龙去脉）——顶层命令，--repo/--json
// 与 query 同解析；目标形态分派在 action.ResolveBeforeTarget。

import (
	"fmt"
	"os"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
	"github.com/schaepher/codeintel/internal/logging"
	"go.uber.org/zap"
)

// intentFlags 意图命令的 flag 解析结果。
type intentFlags struct {
	repoPath string
	json     bool
	maxDepth int
}

// parseIntentFlags 解析 before/trace 参数（--repo/--json/--max-depth）。
func parseIntentFlags(args []string) (target string, f intentFlags, rest []string) {
	f.repoPath = "."
	f.maxDepth = 8
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--repo" && i+1 < len(args):
			f.repoPath = ResolveRepoRef(args[i+1])
			i++
		case strings.HasPrefix(a, "--repo="):
			f.repoPath = ResolveRepoRef(strings.TrimPrefix(a, "--repo="))
		case a == "--json":
			f.json = true
		case a == "--max-depth" && i+1 < len(args):
			var n int
			if _, err := fmt.Sscanf(args[i+1], "%d", &n); err == nil {
				f.maxDepth = n
			}
			i++
		case strings.HasPrefix(a, "-"):
			rest = append(rest, a)
		default:
			if target == "" {
				target = a
			} else {
				rest = append(rest, a)
			}
		}
	}
	return target, f, rest
}

// intentActs 解析 --repo 并打开 Actions（意图命令共用）。
func intentActs(repoPath string) (*action.Actions, string, int) {
	abs, _, err := resolveRepo(repoPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		if repoPath == "." {
			printRepoHint()
		}
		return nil, "", 1
	}
	if err := logging.ToFile(abs); err != nil {
		fmt.Fprintf(os.Stderr, "warning: 日志切换失败: %v\n", err)
	}
	db, err := sqlite.Open(abs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return nil, "", 1
	}
	acts := action.New(sqlite.NewRepo(db))
	return acts, abs, 0
}

// cmdBefore 实现 `codeintel before <目标>`。
func cmdBefore(args []string) int {
	logger := zap.L()
	logger.Debug("enter cmdBefore")
	defer logger.Debug("exit cmdBefore")
	target, f, rest := parseIntentFlags(args)
	if target == "" {
		fmt.Fprintln(os.Stderr, "用法: codeintel before <符号|字段|表> [--json] [--repo <path>]")
		return 2
	}
	if len(rest) > 0 {
		fmt.Fprintf(os.Stderr, "error: 多余参数 %v\n", rest)
		return 2
	}
	acts, _, code := intentActs(f.repoPath)
	if acts == nil {
		return code
	}
	sum, err := acts.Before(target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if f.json {
		encodeJSON(sum)
		return 0
	}
	printBefore(sum)
	return 0
}

// printBefore 文本输出（三段式：目标/影响面/建议）。
func printBefore(sum *action.BeforeSummary) {
	t := sum.Target
	fmt.Printf("目标: %s（%s）\n", t.Name, t.Kind)
	switch t.Kind {
	case "symbol":
		if len(sum.Callers) > 0 {
			fmt.Printf("调用者（深度 2，%d 个）:\n", len(sum.Callers))
			for _, f := range sum.Callers {
				fmt.Printf("  %s  (conf=%.2f)\n", shortID(f.SourceID), f.Confidence)
			}
		} else {
			fmt.Println("调用者: 无")
		}
		if len(sum.Impact) > 0 {
			fmt.Printf("影响节点（深度 3，%d 个）:\n", len(sum.Impact))
			for _, n := range sum.Impact {
				loc := ""
				if n.FilePath != "" {
					loc = fmt.Sprintf(" %s:%d", n.FilePath, n.LineStart)
				}
				fmt.Printf("  %s %s%s\n", n.Kind, n.Name, loc)
			}
		}
	case "field":
		if len(sum.Writers) > 0 {
			fmt.Printf("写入方（%d 处）:\n", len(sum.Writers))
			for _, s := range sum.Writers {
				fmt.Printf("  %s :%d %s\n", shortFuncName(string(s.FunctionID)), s.LineStart, s.CodeSnippet)
			}
		}
		if len(sum.Reads) > 0 {
			fmt.Printf("读取方（%d 处）:\n", len(sum.Reads))
			for _, s := range sum.Reads {
				fmt.Printf("  %s :%d %s\n", shortFuncName(string(s.FunctionID)), s.LineStart, s.CodeSnippet)
			}
		}
	case "table":
		if len(sum.Relations) > 0 {
			fmt.Printf("表关联（%d 条）:\n", len(sum.Relations))
			for _, r := range sum.Relations {
				fmt.Printf("  %s.%s → [%s] → %s.%s\n", r.FromTable, r.FromCol, r.Type, r.ToTable, r.ToCol)
			}
		}
		if len(sum.Columns) > 0 {
			fmt.Printf("列（%d 列）:\n", len(sum.Columns))
			for _, c := range sum.Columns {
				fmt.Printf("  %s (%s)\n", c.Name, c.Access)
			}
		}
	}
}

// cmdTrace 实现 `codeintel trace <目标>`。
func cmdTrace(args []string) int {
	logger := zap.L()
	logger.Debug("enter cmdTrace")
	defer logger.Debug("exit cmdTrace")
	target, f, rest := parseIntentFlags(args)
	if target == "" {
		fmt.Fprintln(os.Stderr, "用法: codeintel trace <字段|符号|表> [--max-depth N] [--json] [--repo <path>]")
		return 2
	}
	if len(rest) > 0 {
		fmt.Fprintf(os.Stderr, "error: 多余参数 %v\n", rest)
		return 2
	}
	acts, _, code := intentActs(f.repoPath)
	if acts == nil {
		return code
	}
	flow, err := acts.TraceFlow(target, f.maxDepth)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if f.json {
		encodeJSON(flow)
		return 0
	}
	fmt.Printf("目标: %s（%s）\n", flow.Target.Name, flow.Target.Kind)
	if len(flow.Flows) > 0 {
		fmt.Printf("值流全链（%d 节点）:\n", len(flow.Flows))
		for _, r := range flow.Flows {
			fmt.Printf("  %s %s :%d\n", r.Kind, r.Name, r.Line)
		}
	}
	if len(flow.Chain) > 0 {
		fmt.Printf("生命周期主链（%d 步）:\n", len(flow.Chain))
		for _, s := range flow.Chain {
			fmt.Printf("  [%s] %s\n", s.Kind, s.Name)
		}
	}
	return 0
}

// cmdBatch 实现 `codeintel batch <符号1> <符号2> ...`（Q244：批量符号
// 概览——多输入一次返回，Agent 减少往返）。
func cmdBatch(args []string) int {
	logger := zap.L()
	logger.Debug("enter cmdBatch")
	defer logger.Debug("exit cmdBatch")
	targets := []string{}
	f := intentFlags{repoPath: ".", json: false}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--repo" && i+1 < len(args):
			f.repoPath = ResolveRepoRef(args[i+1])
			i++
		case strings.HasPrefix(a, "--repo="):
			f.repoPath = ResolveRepoRef(strings.TrimPrefix(a, "--repo="))
		case a == "--json":
			f.json = true
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(os.Stderr, "error: 未知参数 %q\n", a)
			return 2
		default:
			targets = append(targets, a)
		}
	}
	if len(targets) == 0 {
		fmt.Fprintln(os.Stderr, "用法: codeintel batch <符号1> <符号2> ... [--json] [--repo <path>]")
		return 2
	}
	acts, _, code := intentActs(f.repoPath)
	if acts == nil {
		return code
	}
	res, err := acts.BatchSymbols(targets)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if f.json {
		encodeJSON(map[string]any{"results": res})
		return 0
	}
	fmt.Printf("批量符号（%d/%d 命中）:\n", len(res), len(targets))
	for _, r := range res {
		fmt.Printf("  %s %s  调用者 %d / 被调用 %d\n", r.Kind, r.Name, r.Callers, r.Callees)
	}
	return 0
}

package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
)

// Q241 query table-path <表A> <表B> [--max-hops N] [--json] [--full]：
// 表 A → 表 B 数据通路（跨 mapping 表/关联表）。文本输出类型优先级
// 最优一条；--json 全列候选（Q244：默认截断 maxTablePathCandidates
// 条 + truncated 标记，--full 全列）。

// maxTablePathCandidates 同跳数候选截断上限（Q244，防真实仓库爆炸）。
const maxTablePathCandidates = 50

// capTablePathCandidates 截断 candidates（CLI --json 与 MCP 默认；
// --full 跳过）。
func capTablePathCandidates(res *action.TablePathResult) {
	if res == nil || len(res.Candidates) <= maxTablePathCandidates {
		return
	}
	res.Candidates = res.Candidates[:maxTablePathCandidates]
	res.CandidatesTruncated = true
}

func queryTablePath(acts *action.Actions, args []string, jsonFlag, fullFlag bool) int {
	from, to, maxHops, asJSON, full, rest, err := parseTablePathArgs(args)
	if jsonFlag {
		asJSON = true
	}
	if fullFlag {
		full = true
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}
	if len(rest) > 0 {
		fmt.Fprintf(os.Stderr, "error: 多余参数 %v\n", rest)
		return 2
	}
	// 表名解析（大小写不敏感；多匹配报候选）
	resolved := make([]string, 2)
	for i, name := range []string{from, to} {
		hit, candidates, err := acts.ResolveTableName(name)
		if err != nil {
			if len(candidates) > 0 {
				fmt.Fprintf(os.Stderr, "error: %v:\n", err)
				for _, c := range candidates {
					fmt.Fprintf(os.Stderr, "  %s\n", c)
				}
			} else {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
			}
			return 2
		}
		resolved[i] = hit
	}
	res, err := acts.TablePath(resolved[0], resolved[1], maxHops)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if !res.Reachable {
		fmt.Printf("表 %s → %s 不可达（%d 跳内无通路）\n", resolved[0], resolved[1], maxHops)
		return 1
	}
	if asJSON {
		if !full {
			capTablePathCandidates(res)
		}
		b, _ := json.Marshal(res)
		fmt.Println(string(b))
		return 0
	}
	fmt.Printf("表 %s → %s 通路（%d 跳）:\n", resolved[0], resolved[1], res.Hops)
	for _, s := range res.Path {
		fmt.Printf("  %s.%s → [%s] → %s.%s\n", s.FromTable, s.FromCol, s.Type, s.ToTable, s.ToCol)
	}
	if len(res.Candidates) > 1 {
		fmt.Printf("（另有 %d 条同跳数候选路径，--json 查看）\n", len(res.Candidates)-1)
	}
	return 0
}

// parseTablePathArgs 解析 table-path 参数（位置参数 + --max-hops/--json/--full）。
func parseTablePathArgs(args []string) (from, to string, maxHops int, asJSON, full bool, rest []string, err error) {
	maxHops = 6 // Q241：默认 6（mapping 链常见 2-3 跳，显式查询放宽）
	var positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--max-hops" && i+1 < len(args):
			if _, e := fmt.Sscanf(args[i+1], "%d", &maxHops); e != nil {
				return "", "", 0, false, false, nil, fmt.Errorf("--max-hops 无效: %q", args[i+1])
			}
			i++
		case strings.HasPrefix(a, "--max-hops="):
			if _, e := fmt.Sscanf(strings.TrimPrefix(a, "--max-hops="), "%d", &maxHops); e != nil {
				return "", "", 0, false, false, nil, fmt.Errorf("--max-hops 无效: %q", a)
			}
		case a == "--json":
			asJSON = true
		case a == "--full":
			full = true
		case strings.HasPrefix(a, "-"):
			return "", "", 0, false, false, nil, fmt.Errorf("未知参数 %q", a)
		default:
			positional = append(positional, a)
		}
	}
	if len(positional) < 2 {
		return "", "", 0, false, false, nil, fmt.Errorf("用法: query table-path <表A> <表B> [--max-hops N] [--json] [--full]")
	}
	return positional[0], positional[1], maxHops, asJSON, full, positional[2:], nil
}

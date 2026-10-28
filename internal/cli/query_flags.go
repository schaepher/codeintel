package cli

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// parseContinueSegments 解析 --continue-segments 的段数预算（todo24 §2）：
// 合法范围 1–128（0/负数/非数字/超过 128 都是用法错误，不能静默忽略）。
func parseContinueSegments(raw string) (int, string) {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Sprintf("--continue-segments 需要整数，got %q", raw)
	}
	if n < 1 || n > 128 {
		return 0, fmt.Sprintf("--continue-segments 超出范围（1–128），got %d", n)
	}
	return n, ""
}

// parseQueryFlags 手动解析 query 参数（flags 与位置参数任意顺序）。
func parseQueryFlags(args []string) queryFlags {
	f := queryFlags{repoPath: "."}
	f.queryMaxHops, f.writeMaxHops, f.readMaxHops = -1, -1, -1
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--repo" && i+1 < len(args):
			f.repoPath = ResolveRepoRef(args[i+1])
			i++
		case strings.HasPrefix(a, "--repo="):
			f.repoPath = ResolveRepoRef(strings.TrimPrefix(a, "--repo="))
		case a == "--include-untyped":
			f.includeUntyped = true
		case a == "--include-source":
			f.includeSource = true
		case a == "--depth" && i+1 < len(args):
			f.depth, _ = strconv.Atoi(args[i+1])
			i++
		case strings.HasPrefix(a, "--depth="):
			f.depth, _ = strconv.Atoi(strings.TrimPrefix(a, "--depth="))
		case a == "--max-depth" && i+1 < len(args):
			f.maxDepth, _ = strconv.Atoi(args[i+1])
			i++
		case strings.HasPrefix(a, "--max-depth="):
			f.maxDepth, _ = strconv.Atoi(strings.TrimPrefix(a, "--max-depth="))
		case a == "--func" && i+1 < len(args):
			f.funcPath = args[i+1]
			i++
		case strings.HasPrefix(a, "--func="):
			f.funcPath = strings.TrimPrefix(a, "--func=")
		case a == "--since" && i+1 < len(args):
			f.since = args[i+1]
			i++
		case strings.HasPrefix(a, "--since="):
			f.since = strings.TrimPrefix(a, "--since=")
		case a == "--fail-on" && i+1 < len(args):
			f.failOn = args[i+1]
			i++
		case strings.HasPrefix(a, "--fail-on="):
			f.failOn = strings.TrimPrefix(a, "--fail-on=")
		case a == "--min-conf" && i+1 < len(args):
			f.minConf, _ = strconv.ParseFloat(args[i+1], 64)
			f.minConfSet = true
			i++
		case strings.HasPrefix(a, "--min-conf="):
			f.minConf, _ = strconv.ParseFloat(strings.TrimPrefix(a, "--min-conf="), 64)
			f.minConfSet = true
		case a == "--min-packages" && i+1 < len(args):
			f.minPkgs, _ = strconv.Atoi(args[i+1])
			i++
		case strings.HasPrefix(a, "--min-packages="):
			f.minPkgs, _ = strconv.Atoi(strings.TrimPrefix(a, "--min-packages="))
		case a == "--include-container":
			f.includeContainer = true
		case a == "--direction" && i+1 < len(args):
			f.direction = args[i+1]
			i++
		case strings.HasPrefix(a, "--direction="):
			f.direction = strings.TrimPrefix(a, "--direction=")
		case a == "--follow-indirect":
			f.followIndirect = true
		case a == "--all":
			f.all = true
		case a == "--all-repos":
			f.allRepos = true
		case a == "--verify-evidence" && i+1 < len(args):
			f.verifyEvidence = args[i+1]
			i++
		case strings.HasPrefix(a, "--verify-evidence="):
			f.verifyEvidence = strings.TrimPrefix(a, "--verify-evidence=")
		case a == "--type" && i+1 < len(args):
			f.relTypes = append(f.relTypes, strings.Split(args[i+1], ",")...)
			i++
		case strings.HasPrefix(a, "--type="):
			f.relTypes = append(f.relTypes, strings.Split(strings.TrimPrefix(a, "--type="), ",")...)
		case a == "--max-hops" && i+1 < len(args):
			f.maxHops, _ = strconv.Atoi(args[i+1])
			i++
		case strings.HasPrefix(a, "--max-hops="):
			f.maxHops, _ = strconv.Atoi(strings.TrimPrefix(a, "--max-hops="))
		case a == "--max-results" && i+1 < len(args):
			f.maxResults, _ = strconv.Atoi(args[i+1])
			i++
		case strings.HasPrefix(a, "--max-results="):
			f.maxResults, _ = strconv.Atoi(strings.TrimPrefix(a, "--max-results="))
		case a == "--include-long-query":
			f.includeLongQuery = true
		case a == "--query-max-hops" && i+1 < len(args):
			f.queryMaxHops, _ = strconv.Atoi(args[i+1])
			i++
		case strings.HasPrefix(a, "--query-max-hops="):
			f.queryMaxHops, _ = strconv.Atoi(strings.TrimPrefix(a, "--query-max-hops="))
		case a == "--write-max-hops" && i+1 < len(args):
			f.writeMaxHops, _ = strconv.Atoi(args[i+1])
			i++
		case strings.HasPrefix(a, "--write-max-hops="):
			f.writeMaxHops, _ = strconv.Atoi(strings.TrimPrefix(a, "--write-max-hops="))
		case a == "--read-max-hops" && i+1 < len(args):
			f.readMaxHops, _ = strconv.Atoi(args[i+1])
			i++
		case strings.HasPrefix(a, "--read-max-hops="):
			f.readMaxHops, _ = strconv.Atoi(strings.TrimPrefix(a, "--read-max-hops="))
		case a == "--memory" && i+1 < len(args):
			f.memory = args[i+1]
			i++
		case strings.HasPrefix(a, "--memory="):
			f.memory = strings.TrimPrefix(a, "--memory=")
		case a == "--yaml" && i+1 < len(args):
			f.yamlPath = args[i+1]
			i++
		case strings.HasPrefix(a, "--yaml="):
			f.yamlPath = strings.TrimPrefix(a, "--yaml=")
		case a == "--max-entries" && i+1 < len(args):
			f.maxEntries, _ = strconv.Atoi(args[i+1])
			i++
		case strings.HasPrefix(a, "--max-entries="):
			f.maxEntries, _ = strconv.Atoi(strings.TrimPrefix(a, "--max-entries="))
		case a == "--code":
			f.code = true
		case a == "--scene" && i+1 < len(args):
			f.scene = append(f.scene, args[i+1])
			i++
		case strings.HasPrefix(a, "--scene="):
			f.scene = append(f.scene, strings.TrimPrefix(a, "--scene="))
		case a == "--target-field" && i+1 < len(args):
			f.targetField, f.targetFieldSet = args[i+1], true
			i++
		case strings.HasPrefix(a, "--target-field="):
			f.targetField, f.targetFieldSet = strings.TrimPrefix(a, "--target-field="), true
		case a == "--target-return" && i+1 < len(args):
			f.targetReturn, f.targetRetSet = args[i+1], true
			i++
		case strings.HasPrefix(a, "--target-return="):
			f.targetReturn, f.targetRetSet = strings.TrimPrefix(a, "--target-return="), true
		case a == "--timeout" && i+1 < len(args):
			f.timeout = parseTimeout(args[i+1])
			i++
		case strings.HasPrefix(a, "--timeout="):
			f.timeout = parseTimeout(strings.TrimPrefix(a, "--timeout="))
		case a == "--path-budget" && i+1 < len(args):
			f.pathBudget, _ = strconv.Atoi(args[i+1])
			i++
		case strings.HasPrefix(a, "--path-budget="):
			f.pathBudget, _ = strconv.Atoi(strings.TrimPrefix(a, "--path-budget="))
		case a == "--evidence":
			f.evidence = true
		case a == "--continue-segments":
			f.contSegSet = true
			if i+1 >= len(args) {
				f.contSegErr = "--continue-segments 缺少数值（合法范围 1–128）"
				break
			}
			f.contSegments, f.contSegErr = parseContinueSegments(args[i+1])
			i++
		case strings.HasPrefix(a, "--continue-segments="):
			f.contSegSet = true
			f.contSegments, f.contSegErr = parseContinueSegments(strings.TrimPrefix(a, "--continue-segments="))
		case a == "--focus" && i+1 < len(args):
			f.focus = append(f.focus, splitComma(args[i+1])...)
			i++
		case strings.HasPrefix(a, "--focus="):
			f.focus = append(f.focus, splitComma(strings.TrimPrefix(a, "--focus="))...)
		case a == "--out" && i+1 < len(args):
			f.out = args[i+1]
			i++
		case strings.HasPrefix(a, "--out="):
			f.out = strings.TrimPrefix(a, "--out=")
		case a == "--json":
			f.json = true
		case a == "--full":
			f.full = true
		case a == "--compact":
			f.compact = true
		case a == "--format" && i+1 < len(args):
			f.format = args[i+1]
			i++
		case strings.HasPrefix(a, "--format="):
			f.format = strings.TrimPrefix(a, "--format=")
		case a == "--mermaid":
			// todo21-14.6：`--mermaid` 是 `--format mermaid` 的等价别名。
			// 此前该写法被未知选项分支静默忽略 → 输出退化成缩进文本
			// （文档/脚本用 --mermaid 时拿不到图）。
			f.format = "mermaid"
		case strings.HasPrefix(a, "-"):

		default:
			f.positional = append(f.positional, a)
		}
	}
	return f
}

// splitComma 逗号分隔列表拆分（去空白；空串丢弃）。
func splitComma(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// parseTimeout --timeout 解析：纯数字按秒；否则 time.ParseDuration。
func parseTimeout(s string) time.Duration {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	if n, err := strconv.Atoi(s); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return d
	}
	return 0
}

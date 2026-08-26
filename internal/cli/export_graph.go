package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
	"github.com/schaepher/codeintel/internal/logging"
	"go.uber.org/zap"
)

// cmdExportGraph 实现 `codeintel export graph`（Q89）：
//
//	--type value-trace|callees [|lifecycle|modules] --target <节点> [--format mermaid|dot] [--out file]
//
// value-trace 默认 mermaid（flowchart 子图表达函数分组）；callees 默认 dot。
// R9x：图数据获取/编排迁 action（Actions.ExportGraph）；本文件只做
// 参数解析 + 渲染（mermaid/dot 文本拼装）+ 输出。
func cmdExportGraph(args []string) int {
	logger := zap.L()
	logger.Debug("enter cmdExportGraph")
	defer logger.Debug("exit cmdExportGraph")
	graphType, target, format, outPath, repoPath := parseExportGraphFlags(args)
	if graphType != "value-trace" && graphType != "callees" && graphType != "lifecycle" && graphType != "modules" {
		fmt.Fprintln(os.Stderr, "error: --type 须为 value-trace / callees / lifecycle / modules")
		return 2
	}
	if target == "" && graphType != "modules" {
		fmt.Fprintln(os.Stderr, "error: --target <节点> 是必需的")
		return 2
	}
	if format == "" {
		if graphType == "callees" {
			format = "dot"
		} else {
			format = "mermaid"
		}
	}
	if format != "mermaid" && format != "dot" {
		fmt.Fprintln(os.Stderr, "error: --format 须为 mermaid 或 dot")
		return 2
	}

	abs, _, err := resolveRepo(repoPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if err := logging.ToFile(abs); err != nil {
		fmt.Fprintf(os.Stderr, "warning: 日志切换失败: %v\n", err)
	}
	db, err := sqlite.Open(abs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer db.Close()
	acts := action.New(sqlite.NewRepo(db))

	res, err := acts.ExportGraph(action.ExportGraphRequest{Type: graphType, Target: target})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	var output string
	switch {
	case graphType == "callees":
		output, err = renderCalleesDot(res.Facts)
	case graphType == "modules":
		output = renderModulesMermaid(res.Calls)
	case graphType == "lifecycle":
		output = renderLifecycleMermaid(res.Rows)
	case format == "mermaid":
		output = renderValueTraceMermaid(res.Rows)
	default:
		output = renderValueTraceDot(res.Rows)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if outPath == "" {
		fmt.Println(output)
		return 0
	}
	if err := os.WriteFile(outPath, []byte(output), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Printf("已导出 %s 图到 %s\n", format, outPath)
	return 0
}

// parseExportGraphFlags 解析 export graph 参数（--type/--target/
// --format/--out/--repo，支持 --x=y 与 --x y 两种形态）。
func parseExportGraphFlags(args []string) (graphType, target, format, outPath, repoPath string) {
	repoPath = "."
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--type" && i+1 < len(args):
			graphType = args[i+1]
			i++
		case strings.HasPrefix(a, "--type="):
			graphType = strings.TrimPrefix(a, "--type=")
		case a == "--target" && i+1 < len(args):
			target = args[i+1]
			i++
		case strings.HasPrefix(a, "--target="):
			target = strings.TrimPrefix(a, "--target=")
		case a == "--format" && i+1 < len(args):
			format = args[i+1]
			i++
		case strings.HasPrefix(a, "--format="):
			format = strings.TrimPrefix(a, "--format=")
		case a == "--out" && i+1 < len(args):
			outPath = args[i+1]
			i++
		case strings.HasPrefix(a, "--out="):
			outPath = strings.TrimPrefix(a, "--out=")
		case a == "--repo" && i+1 < len(args):
			repoPath = args[i+1]
			i++
		case strings.HasPrefix(a, "--repo="):
			repoPath = strings.TrimPrefix(a, "--repo=")
		}
	}
	return graphType, target, format, outPath, repoPath
}

// renderCalleesDot 渲染 callees 为 DOT digraph（节点用短名，边带 kind）。
func renderCalleesDot(facts []*domain.Fact) (string, error) {
	var sb strings.Builder
	sb.WriteString("digraph callees {\n")
	sb.WriteString("  rankdir=LR;\n")
	for _, f := range facts {
		sb.WriteString(fmt.Sprintf("  %q -> %q [label=%q];\n",
			shortID(f.SourceID), shortID(f.TargetID), string(f.Kind)))
	}
	sb.WriteString("}\n")
	return sb.String(), nil
}

// renderValueTraceMermaid 渲染 value-trace 为 mermaid flowchart，
// 函数上下文用 subgraph 分组（Q89）。
func renderValueTraceMermaid(rows []*domain.TraceRow) string {
	var sb strings.Builder
	sb.WriteString("flowchart LR\n")
	group := ""
	for _, r := range rows {
		if r.FuncID != group {
			if group != "" {
				sb.WriteString("  end\n")
			}
			group = r.FuncID
			gname := shortFuncName(group)
			if gname == "" {
				gname = "unknown"
			}
			sb.WriteString(fmt.Sprintf("  subgraph %q\n", gname))
		}
		arrow := "-->"
		if r.Dir == 0 {
			arrow = "<--"
		}
		sb.WriteString(fmt.Sprintf("    %q %s %q\n", shortID(r.ID), arrow, shortID(r.ID)+"|"+r.Name))
	}
	if group != "" {
		sb.WriteString("  end\n")
	}
	return sb.String()
}

// renderValueTraceDot 渲染 value-trace 为 DOT（同数据，dot 形态）。
func renderValueTraceDot(rows []*domain.TraceRow) string {
	var sb strings.Builder
	sb.WriteString("digraph value_trace {\n  rankdir=LR;\n")
	seen := map[string]bool{}
	for _, r := range rows {
		nid := string(r.ID)
		if !seen[nid] {
			seen[nid] = true
			sb.WriteString(fmt.Sprintf("  %q [label=%q];\n", shortID(r.ID), r.Name))
		}
	}
	for _, r := range rows {
		if r.Depth == 0 {
			continue
		}
		sb.WriteString(fmt.Sprintf("  %q -> %q [label=%q];\n",
			shortID(r.ID), shortID(r.ID)+"|"+r.Name, lastEdgeKind(r.EdgeKinds)))
	}
	sb.WriteString("}\n")
	return sb.String()
}

// renderLifecycleMermaid 端到端生命周期图（Q99）：value-trace 全链
// （含写锚点的下游跳板，⑤）+ 路径条件（Q92），mermaid flowchart 输出。
// R9x：数据获取与条件标注迁 action（Actions.ExportGraph——lifecycle
// 型）；本函数只做渲染。
func renderLifecycleMermaid(rows []*domain.TraceRow) string {
	var sb strings.Builder
	sb.WriteString("flowchart LR\n")
	group := ""
	for _, r := range rows {
		if r.FuncID != group {
			if group != "" {
				sb.WriteString("  end\n")
			}
			group = r.FuncID
			gname := shortFuncName(group)
			if gname == "" {
				gname = "unknown"
			}
			sb.WriteString(fmt.Sprintf("  subgraph %q\n", gname))
		}

		label := r.Name
		switch {
		case strings.HasPrefix(r.Name, "sql."):
			label += " [存储]"
		case strings.HasPrefix(r.Name, "metric"):
			label += " [观测]"
		case r.Kind == domain.KindFieldAccess:
			acc := "写"
			if r.Access == "read" {
				acc = "读"
			}
			label += " [" + acc + "]"
		}
		if len(r.Conditions) > 0 {
			label += " 条件:" + strings.Join(r.Conditions, ";")
		}
		arrow := "-->"
		if r.Dir == 0 {
			arrow = "<--"
		}
		sb.WriteString(fmt.Sprintf("    %q %s %q\n", shortID(r.ID), arrow, shortID(r.ID)+"|"+label))
	}
	if group != "" {
		sb.WriteString("  end\n")
	}
	return sb.String()
}

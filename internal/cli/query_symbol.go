package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// lastEdgeKind 取路径上最后一段边类型（进入当前节点的边）。
func lastEdgeKind(kinds string) string {
	if i := strings.LastIndex(kinds, ","); i >= 0 {
		return kinds[i+1:]
	}
	return kinds
}

// querySymbol 输出符号摘要（对齐 TD.md 7.1 explore_symbol 摘要层）。
func querySymbol(acts *action.Actions, input string, opts outputOpts, since *domain.SinceInfo) int {
	logger := zap.L()
	logger.Debug("enter querySymbol")
	defer logger.Debug("exit querySymbol")
	d, err := acts.SymbolDetail(input)
	if err != nil {
		// todo25 §6：索引缺节点但 AST 声明存在——--include-source 仍输出
		// 声明源码（source.index_status=not_indexed），不写伪节点。
		if opts.includeSource && strings.HasPrefix(input, "symbol:") {
			if src, ok := fetchSymbolSource(acts, opts, input); ok {
				if opts.json {
					// T13：未索引声明仍返回精确身份（顶层 file/id/kind/line/name/
					// index_status）+ source 详情（含 package/imports/selection）。
					encodeJSON(symbolSourceUnindexedJSON{
						File:        src.File,
						ID:          src.SymbolID,
						IndexStatus: src.IndexStatus,
						Kind:        src.Kind,
						Line:        symbolSourceLine(src),
						Name:        symbolSourceName(src.SymbolID),
						Source:      src,
					})
				} else {
					fmt.Printf("ID:         %s\n", src.SymbolID)
					printSymbolSource(src)
				}
				return 0
			}
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	n := d.Node
	// todo22 §6：--include-source 先取源码再输出（失败即返回 1，无部分 JSON）。
	var src *action.SymbolSource
	if opts.includeSource {
		var ok bool
		if src, ok = fetchSymbolSource(acts, opts, string(n.ID)); !ok {
			return 1
		}
	}
	// 动态派发候选（Q95）：接口类型 → 候选实现（置信度/注册点）。
	// P1-03：组合已下沉 action.SymbolDetail，CLI/MCP 同源。
	dispatch := d.Dispatch
	if opts.json {
		out := map[string]any{
			"id":        string(n.ID),
			"name":      n.Name,
			"kind":      string(n.Kind),
			"file":      n.FilePath,
			"line":      n.LineStart,
			"signature": n.Signature(),
			"doc":       n.DocComment(),
			"callers":   factIDs(acts, d.Callers, "source"),
			"callees":   factIDs(acts, d.Callees, "target"),
		}
		if src != nil {
			// T13：source 明细（含 package/imports/selection）；未索引标记
			// 只在顶层输出，不重复写进明细。
			out["source"] = src
			if src.IndexStatus != "" {
				out["index_status"] = src.IndexStatus
			}
		}
		if len(dispatch) > 0 {
			cands := make([]map[string]any, 0, len(dispatch))
			for _, c := range dispatch {
				cands = append(cands, map[string]any{
					"id":            string(c.Impl),
					"method":        c.Method,
					"origin":        c.Origin,
					"confidence":    c.Confidence,
					"register_site": c.RegisterSite,
				})
			}
			out["candidates"] = cands
		}
		encodeJSON(out)
		return 0
	}
	fmt.Printf("ID:         %s\n", n.ID)
	fmt.Printf("名称:       %s%s\n", n.Name, sinceFlag(n, since))
	fmt.Printf("种类:       %s\n", n.Kind)
	if n.FilePath != "" {
		fmt.Printf("文件:       %s", n.FilePath)
		if n.LineStart > 0 {
			fmt.Printf(":%d", n.LineStart)
		}
		fmt.Println()
	}
	if sig := n.Signature(); sig != "" {
		fmt.Printf("签名:       %s\n", sig)
	}
	if doc := n.DocComment(); doc != "" {
		fmt.Printf("文档:       %s\n", strings.Split(doc, "\n")[0])
	}
	fmt.Printf("调用者数:   %d\n", len(d.Callers))
	fmt.Printf("被调用数:   %d\n", len(d.Callees))

	callerIDs := make([]domain.CanonicalID, 0, len(d.Callers))
	for _, f := range d.Callers {
		callerIDs = append(callerIDs, f.SourceID)
	}
	calleeIDs := make([]domain.CanonicalID, 0, len(d.Callees))
	for _, f := range d.Callees {
		calleeIDs = append(calleeIDs, f.TargetID)
	}
	callerMarks := sinceMarks(acts, callerIDs, since)
	calleeMarks := sinceMarks(acts, calleeIDs, since)
	if len(d.Callers) > 0 {
		fmt.Println("调用者:")
		printFacts(d.Callers, "source", 50, callerMarks)
	}
	if len(d.Callees) > 0 {
		fmt.Println("被调用者:")
		printFacts(d.Callees, "target", 50, calleeMarks)
	}

	if src != nil {
		printSymbolSource(src)
	}
	if len(dispatch) > 0 {
		fmt.Printf("候选实现:   %d 个\n", len(dispatch))
		for _, c := range dispatch {
			line := ""
			if c.RegisterSite > 0 {
				line = fmt.Sprintf(" 注册点:%d", c.RegisterSite)
			}
			fmt.Printf("    %-40s %s.%s [%s %.1f]%s\n",
				shortID(c.Impl), shortFuncName(string(n.ID)), c.Method, c.Origin, c.Confidence, line)
		}
	}
	return 0
}

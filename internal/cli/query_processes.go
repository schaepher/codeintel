package cli

// R77 `query processes`——系统流程（wiki processes.md 转命令）：目标
// 仓库全部入口聚合展开——main 入口 + HTTP 路由入口（handler 调用链）
// + gRPC 服务方法入口（(Impl).Method 调用链）。--json 结构化；
// --max-entries 每节展开上限（0 = 默认 15）；--yaml 读 wiki.yaml
// （gRPC 服务按领域分组）。

import (
	"fmt"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// procEntryOut 一个入口（main/http/grpc 通用展示）。
type procEntryOut struct {
	Kind   string         `json:"kind"`   // main | http | grpc
	Title  string         `json:"title"`  // 入口名（main / METHOD path / 服务名）
	Detail string         `json:"detail"` // 补充（位置/实现/注册点）
	Chain  *procChainOut  `json:"chain,omitempty"`
}

// procChainOut 调用链输出（复用 procChain 数据）。
type procChainOut struct {
	Entry string              `json:"entry"`           // 入口符号
	Steps []domain.WikiSeqStep `json:"steps,omitempty"` // 调用步骤（caller → callee）
	Pkgs  []string            `json:"pkgs,omitempty"`  // 涉及包
	Miss  string              `json:"miss,omitempty"`  // 无链原因
}

// processesOut 系统流程输出契约（cmd --json / MCP 共用）。
type processesOut struct {
	Entries []procEntryOut `json:"entries"`
}

// procChainToOut procChain → 输出结构。
func procChainToOut(c *procChain) *procChainOut {
	if c == nil {
		return nil
	}
	return &procChainOut{Entry: c.Entry, Steps: c.Steps, Pkgs: c.Pkgs, Miss: c.Miss}
}

// processesData 收集全部入口（wiki processes.md 同款数据源：main +
// HTTP 路由 + gRPC 服务方法）。
func processesData(acts *action.Actions, repo *sqlite.Repo, data []*domain.WikiModule, cfg wikiConfig, abs string, maxEntries int) processesOut {
	out := processesOut{Entries: []procEntryOut{}}
	rc := wikiCtx(acts, repo, data, cfg, abs, maxEntries)
	// 1. main 入口
	for _, e := range entrySymbols(acts, abs) {
		detail := e.File
		if e.Line > 0 {
			detail = fmt.Sprintf("%s:%d", e.File, e.Line)
		}
		p := procEntryOut{Kind: "main", Title: e.Name, Detail: detail}
		if len(e.CalleeIDs) > 0 {
			p.Chain = procChainToOut(queryChain(acts, e.CalleeIDs[0]))
		}
		out.Entries = append(out.Entries, p)
	}
	// 2. HTTP 路由入口（同 handler 去重）
	for _, h := range httpProcEntries(acts, repo) {
		detail := "[" + h.Resolver + "] " + h.Register
		p := procEntryOut{Kind: "http", Title: joinAnd(h.Paths), Detail: detail, Chain: procChainToOut(h.Chain)}
		out.Entries = append(out.Entries, p)
	}
	// 3. gRPC 服务方法入口（每服务展开上限与 wiki 一致：--max-entries）
	max := procMaxOf(maxEntries)
	for _, s := range grpcServiceList(rc) {
		detail := s.Name
		if s.Impl != "" {
			detail += "（实现 " + s.Impl + "）"
		}
		methods := grpcProcMethods(acts, s)
		if max > 0 && len(methods) > max {
			methods = methods[:max]
		}
		for _, m := range methods {
			out.Entries = append(out.Entries, procEntryOut{
				Kind: "grpc", Title: m.Name, Detail: detail, Chain: procChainToOut(m.Chain),
			})
		}
	}
	return out
}

// cmdQueryProcesses 实现 `query processes [--json] [--max-entries N] [--yaml <file>]`。
func cmdQueryProcesses(acts *action.Actions, repo *sqlite.Repo, abs string, data []*domain.WikiModule, cfg wikiConfig, opts outputOpts, f queryFlags) int {
	out := processesData(acts, repo, data, cfg, abs, f.maxEntries)
	if opts.json {
		encodeJSON(out)
		return 0
	}
	if len(out.Entries) == 0 {
		fmt.Println("（未找到入口——库项目或索引为空）")
		return 0
	}
	for _, e := range out.Entries {
		fmt.Printf("## [%s] %s\n", e.Kind, e.Title)
		if e.Detail != "" {
			fmt.Println("  " + e.Detail)
		}
		if e.Chain == nil {
			fmt.Println("  （无调用链）")
			continue
		}
		if e.Chain.Entry != "" {
			fmt.Println("  入口：" + e.Chain.Entry)
		}
		if e.Chain.Miss != "" {
			fmt.Printf("  %s\n", e.Chain.Miss)
			continue
		}
		for i, st := range e.Chain.Steps {
			fmt.Printf("    %d. %s → %s\n", i+1, st.Caller, st.Callee)
		}
		if len(e.Chain.Pkgs) > 0 {
			fmt.Println("  涉及包：" + joinAnd(e.Chain.Pkgs))
		}
	}
	fmt.Printf("共 %d 个入口\n", len(out.Entries))
	return 0
}

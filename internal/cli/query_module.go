package cli

// R77 `query module <name>`——模块详情（wiki 模块页转命令）：职责/
// 入口/核心符号/关键数据流/模块间调用/相关表/包间调用（模块架构图）。
// --json 结构化；--format mermaid 输出模块架构图（包间调用）。

import (
	"fmt"
	"os"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
)

// moduleOut 模块详情输出契约（cmd --json / MCP 共用）。
type moduleOut struct {
	Name        string        `json:"name"`
	ShortName   string        `json:"short_name"`
	Desc        string        `json:"desc,omitempty"`
	Entries     []string      `json:"entries,omitempty"`
	CoreSymbols []string      `json:"core_symbols,omitempty"` // "Name (Kind, callers 调用者)" 展示
	KeyFlows    []wikiKeyFlow `json:"key_flows,omitempty"`    // 核心符号字段读写
	OutCalls    []string      `json:"out_calls,omitempty"`
	InCalls     []string      `json:"in_calls,omitempty"`
	Tables      []string      `json:"tables,omitempty"`
	PkgCalls    []*domain.WikiPkgCall `json:"pkg_calls,omitempty"` // 包间调用（模块架构图数据）
	ArchMermaid string        `json:"arch_mermaid,omitempty"`      // 模块架构图（--format mermaid）
}

// moduleData 模块详情（wiki 模块页同款：WikiData + wikiModuleKeyFlows）。
func moduleData(acts *action.Actions, data []*domain.WikiModule, name string) *moduleOut {
	var wm *domain.WikiModule
	for _, m := range data {
		if m.Name == name || m.ShortName == name {
			wm = m
			break
		}
	}
	if wm == nil {
		return nil
	}
	out := &moduleOut{
		Name: wm.Name, ShortName: wm.ShortName, Desc: wm.Desc,
		Entries: wm.Entries, OutCalls: wm.OutCalls, InCalls: wm.InCalls,
		Tables: wm.Tables, PkgCalls: wm.PkgCalls,
	}
	for _, s := range wm.CoreSymbols {
		out.CoreSymbols = append(out.CoreSymbols, fmt.Sprintf("%s（%s，%d 调用者）", s.Name, s.Kind, s.Callers))
	}
	out.KeyFlows = wikiModuleKeyFlows(acts, wm)
	out.ArchMermaid = moduleArchMermaid(wm)
	return out
}

// cmdQueryModule 实现 `query module <name> [--json] [--format mermaid]`。
func cmdQueryModule(acts *action.Actions, abs string, f queryFlags, opts outputOpts) int {
	repo, err := buildRepo(abs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	data, err := acts.WikiData(repo.Modules)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	target := ""
	if len(f.positional) > 0 {
		target = f.positional[0]
	}
	if target == "" {
		fmt.Fprintf(os.Stderr, "error: 缺少模块名（可用 module 短名或完整路径）\n")
		return 2
	}
	out := moduleData(acts, data, target)
	if out == nil {
		fmt.Fprintf(os.Stderr, "error: 模块 %q 不存在（可用 `query module-calls` 看模块列表）\n", target)
		return 1
	}
	if opts.json {
		encodeJSON(out)
		return 0
	}
	if f.format == "mermaid" {
		fmt.Println(out.ArchMermaid)
		return 0
	}
	fmt.Printf("## %s\n\n", out.Name)
	if out.Desc != "" {
		fmt.Println("> " + out.Desc + "\n")
	}
	if len(out.Entries) > 0 {
		fmt.Println("入口：" + joinAnd(out.Entries))
	}
	if len(out.CoreSymbols) > 0 {
		fmt.Println("\n核心符号（被调用最多）：")
		for _, s := range out.CoreSymbols {
			fmt.Println("  - " + s)
		}
	}
	if len(out.KeyFlows) > 0 {
		fmt.Println("\n关键数据流（字段读写——value-trace 入口）：")
		for _, fl := range out.KeyFlows {
			parts := []string{}
			if len(fl.Reads) > 0 {
				parts = append(parts, "读 "+joinAnd(fl.Reads))
			}
			if len(fl.Writes) > 0 {
				parts = append(parts, "写 "+joinAnd(fl.Writes))
			}
			fmt.Printf("  - %s：%s\n", fl.Symbol, strings.Join(parts, "；"))
		}
	}
	if len(out.OutCalls) > 0 {
		fmt.Println("\n调用的模块：" + joinAnd(out.OutCalls))
	}
	if len(out.InCalls) > 0 {
		fmt.Println("被哪些模块调用：" + joinAnd(out.InCalls))
	}
	if len(out.Tables) > 0 {
		fmt.Println("相关表：" + joinAnd(out.Tables))
	}
	if len(out.PkgCalls) > 0 {
		fmt.Println("\n包间调用（模块架构图）：")
		for _, c := range out.PkgCalls {
			fmt.Printf("  %s → %s ×%d\n", c.From, c.To, c.Count)
		}
		fmt.Println("（--format mermaid 输出图文本）")
	}
	return 0
}

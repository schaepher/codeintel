package cli

// R5 枚举与工具函数清单：项目关键信息（常量值/helper）进 wiki +
// query enums --json + MCP 工具——AI Agent 直接获取权威枚举值，
// 避免重复定义导致值不一致（如边类型 calls 被写成 call）。
// 数据源：源码 go/ast 提取类型化/字符串 const（代码事实）。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
	"sort"
)

// enumEntry 一个枚举常量。

// extractEnums 提取仓库内字符串枚举常量（类型化或 const 块内字符串
// 字面量）——排除 _test.go 与外部目录（R29：全仓扫，不再限 internal/；
// 跳过 _pb.go 生成代码——枚举由 .proto 源提供）。onlyTyped=true 时
// 只返回有显式类型的枚举（无类型字符串常量多为展示标签——默认过滤；
// --include-untyped 放开）。R29：并入 .proto 源枚举（Source=proto）。

// exprName AST 表达式短名（*ast.Ident / SelectorExpr 末段）。

// strconvUnquote 去掉字符串字面量引号（含反引号）。

// cmdEnums 实现 `codeintel query enums [--repo <path>] [--json]`——
// 源码枚举权威清单（不依赖索引；AI Agent 直接获取避免重定义）。
func cmdEnums(repoAbs string, f queryFlags) int {
	entries := action.Enums(repoAbs, !f.includeUntyped)
	if f.json {
		b, err := json.MarshalIndent(entries, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Println(string(b))
		return 0
	}
	// 文本：分组展示（类型 → 名称 = 值 注释）
	group := ""
	for _, e := range entries {
		key := e.Type
		if key == "" {
			key = e.Group
		}
		if key != group {
			group = key
			fmt.Printf("\n[%s]（%s 包）\n", key, e.Pkg)
		}
		fmt.Printf("  %-28s = %-24q %s\n", e.Name, e.Value, e.Comment)
	}
	fmt.Printf("\n共 %d 个枚举常量\n", len(entries))
	return 0
}

// renderEnumsMD 枚举页 Markdown（含索引实际值分布对照 + 工具函数）。
// R88：工具函数 = 游离函数且被 ≥3 个包调用（helpers.min_packages
// 可调）——与 `query helpers` 同源（queryHelpers）。
func renderEnumsMD(repoAbs string, repo *sqlite.Repo) string {
	entries := action.Enums(repoAbs, true)
	var b strings.Builder
	b.WriteString("# 枚举与工具函数\n\n> 数据源：源码 go/ast 提取（代码事实，默认只显示有类型枚举）\n")
	b.WriteString("> ——AI/Agent 直接引用这些权威值，勿重新定义（值不一致会导致\n")
	b.WriteString("> 转换成本）；`query enums --include-untyped` 可含无类型常量。\n\n")
	group := ""
	for _, e := range entries {
		key := e.Type
		if key == "" {
			key = e.Group
		}
		if key != group {
			group = key
			b.WriteString("## " + key + "\n\n")
			b.WriteString("| 名称 | 值 | 说明 | 位置 |\n|---|---|---|---|\n")
		}
		loc := fmt.Sprintf("%s:%d", e.File, e.Line)
		b.WriteString(fmt.Sprintf("| %s | `%s` | %s | %s |\n", e.Name, e.Value, e.Comment, loc))
	}
	if len(entries) == 0 {
		b.WriteString("（未提取到枚举常量）\n")
	}
	return b.String()
}

// renderEnumsHTML 枚举页 html 内容。
func renderEnumsHTML(repoAbs string, repo *sqlite.Repo) string {
	entries := action.Enums(repoAbs, true)
	var b strings.Builder
	b.WriteString(`<section id="enums"><h2>枚举与工具函数</h2><p class="muted">数据源：源码 go/ast 提取（有类型枚举）——权威值，勿重新定义。</p>`)
	if sec := renderHelpersHTML(repo); sec != "" {
		b.WriteString(sec)
	}
	// R87：每组枚举默认折叠（组名按钮——点击展开该组表格，减少长页
	// 面滚动；枚举区只显示组名列表）。R93：展开后表上方展示所在包
	groupPkgs := map[string]map[string]bool{}
	for _, e := range entries {
		key := e.Type
		if key == "" {
			key = e.Group
		}
		if groupPkgs[key] == nil {
			groupPkgs[key] = map[string]bool{}
		}
		groupPkgs[key][e.Pkg] = true
	}
	group := ""
	groupIdx := 0
	for _, e := range entries {
		key := e.Type
		if key == "" {
			key = e.Group
		}
		if key != group {
			if group != "" {
				b.WriteString("</table></div>")
			}
			group = key
			b.WriteString(fmt.Sprintf(`<div class="fold-btn" data-target="enum-%d" data-label="1">▸ %s</div>`,
				groupIdx, htmlEsc(key)))
			pkgs := make([]string, 0, len(groupPkgs[key]))
			for pkg := range groupPkgs[key] {
				pkgs = append(pkgs, pkg)
			}
			sort.Strings(pkgs)
			b.WriteString(fmt.Sprintf(`<div class="sec-body" id="enum-%d" style="display:none"><h3>%s</h3><p class="muted">所在包：<code>%s</code></p><table><tr><th>名称</th><th>值</th><th>说明</th><th>位置</th></tr>`,
				groupIdx, htmlEsc(key), htmlEsc(strings.Join(pkgs, ", "))))
			groupIdx++
		}
		b.WriteString(fmt.Sprintf("<tr><td>%s</td><td><code>%s</code></td><td>%s</td><td class=\"muted\">%s:%d</td></tr>",
			htmlEsc(e.Name), htmlEsc(e.Value), htmlEsc(e.Comment), htmlEsc(filepath.Base(e.File)), e.Line))
	}
	if group != "" {
		b.WriteString("</table></div>")
	}
	b.WriteString("</section>")
	return b.String()
}

// renderHelpersMD 工具函数 Markdown 小节（R88/R89：游离函数 + 跨包
// 使用数 ≥ minPkgs——action.Helpers 同源）。
func renderHelpersMD(repo *sqlite.Repo) string {
	if repo == nil {
		return ""
	}
	minPkgs := helperMinPackages()
	helpers, err := action.New(repo).Helpers(action.HelpersRequest{MinPackages: minPkgs})
	if err != nil || len(helpers) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## 工具函数\n\n> 游离函数且被 ≥")
	b.WriteString(fmt.Sprint(minPkgs))
	b.WriteString(" 个包调用（`query helpers` 同源；config.yaml helpers.min_packages 可调）\n\n")
	// R93：展示所在包（ID 提取——同函数名跨包可区分）
	b.WriteString("| 函数 | 所在包 | 包数 | 调用方 |\n|---|---|---|---|\n")
	for _, h := range helpers {
		fmt.Fprintf(&b, "| %s | `%s` | %d | %d |\n", h.Name, pkgPathOfHelper(h.ID), h.Pkgs, h.Callers)
	}
	return b.String()
}

// renderHelpersHTML 工具函数 html 小节（同源 action.Helpers）。
func renderHelpersHTML(repo *sqlite.Repo) string {
	if repo == nil {
		return ""
	}
	minPkgs := helperMinPackages()
	helpers, err := action.New(repo).Helpers(action.HelpersRequest{MinPackages: minPkgs})
	if err != nil || len(helpers) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf(`<h3 class="fold-btn" data-target="helpers" data-label="1">▸ 工具函数（游离函数，被 ≥%d 个包调用）</h3><div class="sec-body" id="helpers" style="display:none"><p class="muted">`+`query helpers`+` 同源；config.yaml helpers.min_packages 可调</p><table><tr><th>函数</th><th>包数</th><th>调用方</th></tr>`, minPkgs))
	for _, h := range helpers {
		fmt.Fprintf(&b, "<tr><td><code>%s</code></td><td><code>%s</code></td><td>%d</td><td>%d</td></tr>",
			htmlEsc(h.Name), htmlEsc(pkgPathOfHelper(h.ID)), h.Pkgs, h.Callers)
	}
	b.WriteString("</table></div>")
	return b.String()
}

// pkgPathOfHelper helperEntry ID → 包路径（symbol:go:<pkg>:<name>）。
func pkgPathOfHelper(id string) string {
	rest := strings.TrimPrefix(id, "symbol:go:")
	if i := strings.LastIndex(rest, ":"); i > 0 {
		return rest[:i]
	}
	return rest
}

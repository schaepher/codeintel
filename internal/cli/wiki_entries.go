package cli

// F1：命令与入口页——目标仓库 main 入口 + 一级调用链（不再硬编码
// codeintel 自身命令）。从 wiki_commands.go 拆出（行数治理）。

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

type entrySymbol struct {
	Name      string
	File      string
	Line      int
	Callees   []string // 压缩短名（展示用：pkg:name）
	CalleeIDs []string // 完整 canonical ID（processes 展开用——短名无法按名解析）
}


// entrySymbols 目标仓库 main 入口。R39：过滤两类噪音（自身 wiki 实测——
// 8 个 main 中 6 个无调用链）：
// - tmp/ 前缀：仓库内临时工具/探针（约定 tmp/ 存临时脚本——非业务入口）
// - 文件不存在：多模块仓库索引进的外部 fixture 残留（fixtureapp/bench/
//   probe 等幽灵 main——relPath 对仓库外文件算出裸文件名）
func entrySymbols(acts *action.Actions, repoAbs string) []entrySymbol {
	nodes, err := acts.Entries()
	if err != nil || len(nodes) == 0 {
		return nil
	}
	var out []entrySymbol
	for _, n := range nodes {
		if strings.HasPrefix(n.FilePath, "tmp/") {
			continue
		}
		if repoAbs != "" {
			if _, err := os.Stat(filepath.Join(repoAbs, n.FilePath)); err != nil {
				continue
			}
		}
		d, err := acts.SymbolDetail(string(n.ID))
		if err != nil {
			continue
		}
		e := entrySymbol{Name: n.Name, File: n.FilePath, Line: n.LineStart}
		for _, f := range d.Callees {
			e.Callees = append(e.Callees, shortID(f.TargetID))
			e.CalleeIDs = append(e.CalleeIDs, string(f.TargetID))
		}
		out = append(out, e)
	}
	return out
}

// renderCommandsMD 命令与入口页 Markdown（R35：有 urfave/cli 命令树
// 先展示命令清单；后接目标仓库 main 入口 + 一级调用链）。repoAbs：
// main 入口噪音过滤（R39——tmp/ 前缀 + 文件不存在）。
func renderCommandsMD(acts *action.Actions, repo *sqlite.Repo, repoAbs string) string {
	var b strings.Builder
	b.WriteString("# 命令与入口\n\n")
	b.WriteString("> 数据源：urfave/cli 命令树（代码事实）+ main 入口调用链（索引事实）。\n\n")
	if res, err := cliRoutes(repo); err == nil && len(res.Commands) > 0 {
		b.WriteString("## 命令清单\n\n")
		var walk func(cmds []cliCommandEntry, depth int)
		walk = func(cmds []cliCommandEntry, depth int) {
			for _, c := range cmds {
				line := "- `" + c.Name + "`"
				if c.Usage != "" {
					line += " — " + c.Usage
				}
				if c.Action != "" {
					line += "（" + c.Action + "）"
				}
				b.WriteString(strings.Repeat("  ", depth) + line + "\n")
				walk(c.Subcommands, depth+1)
			}
		}
		walk(res.Commands, 0)
		b.WriteString("\n")
	}
	entries := entrySymbols(acts, repoAbs)
	if len(entries) == 0 {
		b.WriteString("未找到 main 入口（库项目或入口不在索引中）。\n")
		return b.String()
	}
	for _, e := range entries {
		b.WriteString("## 入口 `" + e.Name + "`\n\n")
		if e.File != "" {
			b.WriteString("位置: " + e.File)
			if e.Line > 0 {
				b.WriteString(":" + strconv.Itoa(e.Line))
			}
			b.WriteString("\n\n")
		}
		if len(e.Callees) == 0 {
			b.WriteString("一级调用: （无）\n\n")
			continue
		}
		b.WriteString("一级调用:\n\n")
		for _, c := range e.Callees {
			b.WriteString("- `" + c + "`\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// renderCommandsHTML 命令与入口页 html 内容（R35：urfave/cli 命令树
// 前置 + main 入口调用链）。repoAbs：main 入口噪音过滤（R39）。
func renderCommandsHTML(acts *action.Actions, repo *sqlite.Repo, repoAbs string) string {
	var b strings.Builder
	b.WriteString(`<section id="commands"><h2>命令与入口</h2><p class="muted">数据源：urfave/cli 命令树（代码事实）+ main 入口调用链（索引事实）。</p>`)
	if res, err := cliRoutes(repo); err == nil && len(res.Commands) > 0 {
		b.WriteString("<h3>命令清单</h3><ul>")
		var walk func(cmds []cliCommandEntry)
		walk = func(cmds []cliCommandEntry) {
			for _, c := range cmds {
				line := "<code>" + htmlEsc(c.Name) + "</code>"
				if c.Usage != "" {
					line += " — " + htmlEsc(c.Usage)
				}
				if c.Action != "" {
					line += "（" + htmlEsc(c.Action) + "）"
				}
				b.WriteString("<li>" + line)
				if len(c.Subcommands) > 0 {
					b.WriteString("<ul>")
					walk(c.Subcommands)
					b.WriteString("</ul>")
				}
				b.WriteString("</li>")
			}
		}
		walk(res.Commands)
		b.WriteString("</ul>")
	}
	entries := entrySymbols(acts, repoAbs)
	if len(entries) == 0 {
		b.WriteString(`<p>未找到 main 入口（库项目或入口不在索引中）。</p></section>`)
		return b.String()
	}
	for _, e := range entries {
		b.WriteString(fmt.Sprintf(`<h3>入口 <code>%s</code></h3>`, htmlEsc(e.Name)))
		if e.File != "" {
			loc := htmlEsc(e.File)
			if e.Line > 0 {
				loc += ":" + strconv.Itoa(e.Line)
			}
			b.WriteString(`<p class="muted">` + loc + `</p>`)
		}
		b.WriteString("<ul>")
		for _, c := range e.Callees {
			b.WriteString("<li><code>" + htmlEsc(c) + "</code></li>")
		}
		b.WriteString("</ul>")
	}
	b.WriteString("</section>")
	return b.String()
}


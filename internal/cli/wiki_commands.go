package cli

// R1 自举分析扩展：命令页 + HTTP 接口页——数据源是代码事实
// （usageText 常量 / server 路由与 handler 注释），不依赖 AI。
// commands.md：全部顶层命令与 query 子命令（usageText 解析）；
// api.md：全部 HTTP 路由（server 源码解析）。

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// cmdEntry 一条命令（usageText 解析）。
type cmdEntry struct {
	Cmd  string // codeintel init --repo <path>
	Desc string // 说明（后续缩进行合并）
}

// reCmdLine usageText 中的命令行（`  codeintel ...`）。
var reCmdLine = regexp.MustCompile(`^  (codeintel .+)$`)

// parseCommands 解析 usageText → 命令条目（连续缩进行并入 Desc）。
func parseCommands(text string) []cmdEntry {
	var out []cmdEntry
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		m := reCmdLine.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		e := cmdEntry{Cmd: strings.TrimSpace(m[1])}
		for i+1 < len(lines) {
			next := lines[i+1]
			if next == "" || strings.HasPrefix(strings.TrimSpace(next), "codeintel") {
				break
			}
			e.Desc += strings.TrimSpace(next) + " "
			i++
		}
		e.Desc = strings.TrimSpace(e.Desc)
		out = append(out, e)
	}
	return out
}

// renderCommandsMD 命令页 Markdown。
func renderCommandsMD() string {
	var b strings.Builder
	b.WriteString("# 命令清单\n\n> 数据源：CLI 帮助文本（root.go usageText）——全部顶层命令与\n> query 子命令；参数细节以 `codeintel --help` 为准。\n\n")
	entries := parseCommands(usageText)
	group := ""
	for _, e := range entries {
		// 分组：query 子命令归入 "query"，其余取第一词
		first := e.Cmd
		if i := strings.Index(first, " "); i >= 0 {
			first = first[:i]
		}
		if strings.HasPrefix(e.Cmd, "codeintel query") {
			first = "query"
		}
		if first != group {
			group = first
			b.WriteString("## " + group + "\n\n")
		}
		b.WriteString("### `" + e.Cmd + "`\n\n")
		if e.Desc != "" {
			b.WriteString(e.Desc + "\n\n")
		}
	}
	return b.String()
}

// apiRoute 一条 HTTP 路由（server 源码解析）。
type apiRoute struct {
	Method  string // GET/POST/DELETE/任意（路由行注释补充）
	Path    string
	Handler string
	Desc    string
}

// reRouteLine server.go 路由注册行。
var reRouteLine = regexp.MustCompile(`mux\.HandleFunc\("([^"]+)", s\.(\w+)\)(?: // (.*))?`)

// reHandlerDef handler 函数定义（提取上方注释）。
var reHandlerDef = regexp.MustCompile(`(?m)^func \(s \*Server\) (\w+)\(w http\.ResponseWriter`)

// parseAPIRoutes 解析 server 包源码 → 路由清单（handler 上方注释作描述）。
func parseAPIRoutes(repoAbs string) []apiRoute {
	dir := filepath.Join(repoAbs, "internal", "server")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") && !strings.HasSuffix(e.Name(), "_test.go") {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	// handler 注释索引：handler 名 → 上方连续 // 注释
	handlerDoc := map[string]string{}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		text := string(src)
		for _, m := range reHandlerDef.FindAllStringSubmatchIndex(text, -1) {
			name := text[m[2]:m[3]]
			start := m[0]
			var doc []string
			lines := strings.Split(text[:start], "\n")
			for j := len(lines) - 1; j >= 0 && j > len(lines)-6; j-- {
				trim := strings.TrimSpace(lines[j])
				if strings.HasPrefix(trim, "//") {
					doc = append([]string{strings.TrimSpace(strings.TrimPrefix(trim, "//"))}, doc...)
				} else if trim != "" {
					break
				}
			}
			if len(doc) > 0 {
				handlerDoc[name] = strings.Join(doc, " ")
			}
		}
	}
	// 路由表（server.go）
	var routes []apiRoute
	src, err := os.ReadFile(filepath.Join(dir, "server.go"))
	if err == nil {
		for _, m := range reRouteLine.FindAllStringSubmatch(string(src), -1) {
			r := apiRoute{Path: m[1], Handler: m[2]}
			if m[3] != "" {
				r.Desc = strings.TrimSpace(m[3])
			}
			if d := handlerDoc[m[2]]; d != "" {
				if r.Desc != "" {
					r.Desc += "；" + d
				} else {
					r.Desc = d
				}
			}
			routes = append(routes, r)
		}
	}
	// wiki 路由（cli 包 wiki_serve.go）
	if wsrc, err := os.ReadFile(filepath.Join(repoAbs, "internal", "cli", "wiki_serve.go")); err == nil {
		ws := string(wsrc)
		for _, p := range []string{"/wiki/", "/wiki/overview", "/wiki/mod/<短名>", "/wiki/er", "/wiki/tables"} {
			routes = append(routes, apiRoute{Path: p, Desc: "wiki 网页版多页路由（serve 集成）"})
		}
		_ = ws
	}
	sort.Slice(routes, func(i, j int) bool { return routes[i].Path < routes[j].Path })
	return routes
}

// renderAPIMD HTTP 接口页 Markdown。
func renderAPIMD(repoAbs string) string {
	var b strings.Builder
	b.WriteString("# HTTP 接口\n\n> 数据源：server 包路由注册 + handler 注释（代码事实）。\n")
	b.WriteString("> 图探索前端（AntV G6）调用 /api/*；wiki 网页版在 /wiki/*。\n\n")
	group := ""
	for _, r := range parseAPIRoutes(repoAbs) {
		g := "图探索 API"
		switch {
		case r.Path == "/incremental":
			g = "增量构建"
		case strings.HasPrefix(r.Path, "/wiki"):
			g = "wiki 网页版"
		}
		if g != group {
			group = g
			b.WriteString("## " + g + "\n\n")
		}
		b.WriteString("### `" + r.Path + "`\n\n")
		if r.Desc != "" {
			b.WriteString(r.Desc + "\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// renderCommandsHTML 命令页 html 内容（usageText 原样 pre——格式已
// 精心排版，逐条转换反而丢信息）。
func renderCommandsHTML() string {
	return `<section id="commands"><h2>命令清单</h2><p class="muted">数据源：CLI 帮助文本（全部顶层命令与 query 子命令）。</p><pre style="font-size:12px;line-height:1.6;background:#f6f8fa;padding:12px;border-radius:6px;overflow-x:auto">` + htmlEsc(usageText) + `</pre></section>`
}

// renderAPIHTML HTTP 接口页 html 内容。
func renderAPIHTML(repoAbs string) string {
	var b strings.Builder
	b.WriteString(`<section id="api"><h2>HTTP 接口</h2><p class="muted">数据源：server 包路由注册 + handler 注释。</p>`)
	group := ""
	for _, r := range parseAPIRoutes(repoAbs) {
		g := "图探索 API"
		switch {
		case r.Path == "/incremental":
			g = "增量构建"
		case strings.HasPrefix(r.Path, "/wiki"):
			g = "wiki 网页版"
		}
		if g != group {
			if group != "" {
				b.WriteString("</ul>")
			}
			group = g
			b.WriteString(fmt.Sprintf("<h3>%s</h3><ul>", g))
		}
		b.WriteString("<li><code>" + htmlEsc(r.Path) + "</code>")
		if r.Desc != "" {
			b.WriteString(" — " + htmlEsc(r.Desc))
		}
		b.WriteString("</li>")
	}
	if group != "" {
		b.WriteString("</ul>")
	}
	b.WriteString("</section>")
	return b.String()
}

// renderPackagesHTML 包职责地图 html 内容（R1：KindPackage doc_comment）。
func renderPackagesHTML(pkgs []*domain.CodeEntity) string {
	if len(pkgs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<section id="packages"><h2>包结构</h2><p class="muted">数据源：包节点 doc_comment（代码事实）。</p>`)
	for _, p := range pkgs {
		name := p.Name
		if i := strings.LastIndex(name, "/"); i >= 0 {
			name = name[i+1:]
		}
		b.WriteString("<h3><code>" + htmlEsc(name) + "</code></h3>")
		if dc, ok := p.Properties["doc_comment"].(string); ok && dc != "" {
			b.WriteString("<p>" + htmlEsc(dc) + "</p>")
		}
	}
	b.WriteString("</section>")
	return b.String()
}

// renderPackagesMD 包职责地图（R1：KindPackage 节点 doc_comment）。
func renderPackagesMD(pkgs []*domain.CodeEntity) string {
	if len(pkgs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## 包结构\n\n> 数据源：包节点 doc_comment（代码事实）。\n\n")
	for _, p := range pkgs {
		name := p.Name
		if i := strings.LastIndex(name, "/"); i >= 0 {
			name = name[i+1:]
		}
		b.WriteString("### `" + name + "`\n\n")
		if dc, ok := p.Properties["doc_comment"].(string); ok && dc != "" {
			b.WriteString(dc + "\n\n")
		}
	}
	return b.String()
}

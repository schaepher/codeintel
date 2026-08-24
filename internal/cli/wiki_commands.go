package cli

// R1 自举分析扩展：HTTP 接口页——数据源是 server 路由与 handler
// 注释（代码事实），不依赖 AI。命令/入口页在 wiki_entries.go
// （F1：目标仓库 main 入口）。

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

)

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

// renderAPIMD HTTP 接口页 Markdown（目标仓库 internal/server 路由；
// 无 server 包时提示——F1：不再展示 codeintel 自身路由）。
func renderAPIMD(repoAbs string) string {
	var b strings.Builder
	b.WriteString("# HTTP 接口\n\n> 数据源：目标仓库 internal/server 包路由注册 + handler 注释。\n\n")
	routes := parseAPIRoutes(repoAbs)
	if len(routes) == 0 {
		b.WriteString("未发现 HTTP 路由（无 internal/server 包）。\n")
		return b.String()
	}
	group := ""
	for _, r := range routes {
		if r.Path == "/incremental" {
			group = "增量构建"
		} else if strings.HasPrefix(r.Path, "/wiki") {
			group = "wiki 网页版"
		} else {
			group = "API"
		}
		b.WriteString("## " + group + "\n\n")
		b.WriteString("### `" + r.Path + "`\n\n")
		if r.Desc != "" {
			b.WriteString(r.Desc + "\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// renderAPIHTML HTTP 接口页 html 内容（目标仓库 internal/server 路由；
// 无 server 包时提示）。
func renderAPIHTML(repoAbs string) string {
	var b strings.Builder
	b.WriteString(`<section id="api"><h2>HTTP 接口</h2><p class="muted">数据源：目标仓库 internal/server 包路由注册 + handler 注释。</p>`)
	routes := parseAPIRoutes(repoAbs)
	if len(routes) == 0 {
		b.WriteString(`<p>未发现 HTTP 路由（无 internal/server 包）。</p></section>`)
		return b.String()
	}
	group := ""
	for _, r := range routes {
		g := "API"
		if r.Path == "/incremental" {
			g = "增量构建"
		} else if strings.HasPrefix(r.Path, "/wiki") {
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

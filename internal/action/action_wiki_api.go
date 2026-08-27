package action

// R100 待办9：wiki 数据源全部改 action——HTTP API 路由解析（R1——internal/server 源码）（wiki 只组合 action
// 结果到 html/md；cli 不再直连 sqlite/读源码）。

import (
"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// reRouteLine server.go 路由注册行。
var reRouteLine = regexp.MustCompile(`mux\.HandleFunc\("([^"]+)", s\.(\w+)\)(?: // (.*))?`)

// reHandlerDef handler 函数定义（提取上方注释）。
var reHandlerDef = regexp.MustCompile(`(?m)^func \(s \*Server\) (\w+)\(w http\.ResponseWriter`)

// APIRoutes 解析目标仓库 internal/server 包源码 → 路由清单（handler
// 上方注释作描述）。R1（原 cli parseAPIRoutes 迁 action）。
func (a *Actions) APIRoutes(repoAbs string) ([]domain.APIRoute, error) {
	logger := zap.L()
	logger.Info("enter (Actions).APIRoutes", zap.String("repo", repoAbs))
	defer logger.Info("exit (Actions).APIRoutes")
	dir := filepath.Join(repoAbs, "internal", "server")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
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
	var routes []domain.APIRoute
	src, err := os.ReadFile(filepath.Join(dir, "server.go"))
	if err == nil {
		for _, m := range reRouteLine.FindAllStringSubmatch(string(src), -1) {
			r := domain.APIRoute{Path: m[1], Handler: m[2]}
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
		_ = wsrc
		for _, p := range []string{"/wiki/", "/wiki/overview", "/wiki/mod/<短名>", "/wiki/er", "/wiki/tables"} {
			routes = append(routes, domain.APIRoute{Path: p, Desc: "wiki 网页版多页路由（serve 集成）"})
		}
	}
	sort.Slice(routes, func(i, j int) bool { return routes[i].Path < routes[j].Path })
	return routes, nil
}

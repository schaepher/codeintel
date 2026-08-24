package cli

// R31 `codeintel query http-routes`——HTTP 路由清单（待办 1 http 部分，
// Q1 契约）：构建期两个 resolver 各自识别（原生 net/http + gin）发射
// http_route 节点（method/path/handler/resolver/register），查询层直接
// 读节点输出 JSON。

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// httpRouteEntry 一条 HTTP 路由（Q1 契约 + R37 handler_id）。
type httpRouteEntry struct {
	Method    string `json:"method"`     // HTTP 方法（原生 HandleFunc 为空）
	Path      string `json:"path"`       // 路由路径（gin Group 前缀已拼接）
	Handler   string `json:"handler"`    // handler 函数名
	HandlerID string `json:"handler_id"` // handler canonical ID（R37 发射端解析；老索引为空）
	Resolver  string `json:"resolver"`   // 来源：native | gin
	Register  string `json:"register"`   // 注册调用点（file:line）
}

// httpRoutesResult 查询结果。
type httpRoutesResult struct {
	Routes []httpRouteEntry `json:"routes"`
}

// httpRoutes 读 http_route 节点（构建期发射）→ 契约结构，确定性排序。
func httpRoutes(repo *sqlite.Repo) (*httpRoutesResult, error) {
	res := &httpRoutesResult{Routes: []httpRouteEntry{}}
	// handler_id 老索引无该属性（json_extract 返回 NULL）——COALESCE 兜底
	// 否则 Scan string 失败丢整行（R34/R35 教训）
	rows, err := repo.Query(`SELECT json_extract(properties, '$.method'), json_extract(properties, '$.path'),
		json_extract(properties, '$.handler'), COALESCE(json_extract(properties, '$.handler_id'), ''),
		json_extract(properties, '$.resolver'), json_extract(properties, '$.register') FROM nodes WHERE kind = 'http_route'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var method, path, handler, handlerID, resolver, register string
		if err := rows.Scan(&method, &path, &handler, &handlerID, &resolver, &register); err != nil {
			continue
		}
		res.Routes = append(res.Routes, httpRouteEntry{
			Method: method, Path: path, Handler: handler, HandlerID: handlerID,
			Resolver: resolver, Register: register,
		})
	}
	sort.Slice(res.Routes, func(i, j int) bool {
		if res.Routes[i].Resolver != res.Routes[j].Resolver {
			return res.Routes[i].Resolver < res.Routes[j].Resolver
		}
		if res.Routes[i].Method != res.Routes[j].Method {
			return res.Routes[i].Method < res.Routes[j].Method
		}
		return res.Routes[i].Path < res.Routes[j].Path
	})
	return res, nil
}

// cmdHTTPRoutes 实现 `codeintel query http-routes [--repo <path>] [--json]`
// ——HTTP 路由清单（契约化 JSON，Agent 直接解析）。
func cmdHTTPRoutes(repoAbs string, f queryFlags) int {
	db, err := sqlite.Open(repoAbs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer db.Close()
	res, err := httpRoutes(sqlite.NewRepo(db))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if f.json {
		b, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Println(string(b))
		return 0
	}
	cur := ""
	for _, r := range res.Routes {
		key := r.Resolver
		if key != cur {
			cur = key
			fmt.Printf("\n[%s]\n", key)
		}
		m := r.Method
		if m == "" {
			m = "ANY"
		}
		fmt.Printf("  %-6s %-40s → %s（%s）\n", m, r.Path, r.Handler, r.Register)
	}
	fmt.Printf("\n共 %d 条 HTTP 路由\n", len(res.Routes))
	return 0
}

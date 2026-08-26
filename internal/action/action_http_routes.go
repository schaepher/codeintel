package action

// R92 迁移：`query http-routes` 查询逻辑（原 cli/query_http_routes.go）
// ——HTTP 路由清单（Q1 契约）：构建期两个 resolver 各自识别（原生
// net/http + gin）发射 http_route 节点（method/path/handler/resolver/
// register），查询层直接读节点输出。cli 只做参数转发与输出
// （cmdHTTPRoutes）。

import (
	"sort"

	"go.uber.org/zap"
)

// HTTPRouteEntry 一条 HTTP 路由（Q1 契约 + R37 handler_id）。
type HTTPRouteEntry struct {
	Method    string `json:"method"`     // HTTP 方法（原生 HandleFunc 为空）
	Path      string `json:"path"`       // 路由路径（gin Group 前缀已拼接）
	Handler   string `json:"handler"`    // handler 函数名
	HandlerID string `json:"handler_id"` // handler canonical ID（R37 发射端解析；老索引为空）
	Resolver  string `json:"resolver"`   // 来源：native | gin
	Register  string `json:"register"`   // 注册调用点（file:line）
}

// HTTPRoutesResult 查询结果。
type HTTPRoutesResult struct {
	Routes []HTTPRouteEntry `json:"routes"`
}

// HTTPRoutes 读 http_route 节点（构建期发射）→ 契约结构，确定性排序。
func (a *Actions) HTTPRoutes() (*HTTPRoutesResult, error) {
	logger := zap.L()
	logger.Info("enter (Actions).HTTPRoutes")
	defer logger.Info("exit (Actions).HTTPRoutes")
	return httpRoutes(a.repo)
}

// httpRoutes 读 http_route 节点 → 契约结构（r = 仓储窄接口）。
func httpRoutes(r Reader) (*HTTPRoutesResult, error) {
	res := &HTTPRoutesResult{Routes: []HTTPRouteEntry{}}
	// handler_id 老索引无该属性（json_extract 返回 NULL）——Property
	// 读取缺属性返回空，不丢行（R34/R35 教训的 COALESCE 等价）
	nodes, err := r.GetHTTPRouteNodes()
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		res.Routes = append(res.Routes, HTTPRouteEntry{
			Method: n.Property("method"), Path: n.Property("path"), Handler: n.Property("handler"),
			HandlerID: n.Property("handler_id"), Resolver: n.Property("resolver"), Register: n.Property("register"),
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

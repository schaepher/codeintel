package action

// R92 迁移：`query http-routes` 查询逻辑（原 cli/query_http_routes.go）
// ——HTTP 路由清单（Q1 契约）：构建期两个 resolver 各自识别（原生
// net/http + gin）发射 http_route 节点（method/path/handler/resolver/
// register），查询层直接读节点输出。cli 只做参数转发与输出
// （cmdHTTPRoutes）。
//
// Q?：明确区分**入站**（服务端路由：symbol:go:<pkg>:route.<path>，
// 有 handler/resolver）与**出站**（http_call 目标：
// symbol:http:<host>:route.<path>，无 handler）。出站若在同仓库找不到
// 同名入站 path，则为**纯出站**（MatchedInbound=false）。

import (
	"sort"
	"strings"

	"go.uber.org/zap"
)

// HTTP 路由方向。
const (
	HTTPRouteInbound  = "inbound"  // 服务端路由（本仓库暴露）
	HTTPRouteOutbound = "outbound" // 出站调用目标
)

// HTTPRouteEntry 一条 HTTP 路由（Q1 契约 + R37 handler_id）。
type HTTPRouteEntry struct {
	Method    string `json:"method"`     // HTTP 方法（原生 HandleFunc 为空）
	Path      string `json:"path"`       // 路由路径（gin Group 前缀已拼接）
	Handler   string `json:"handler"`    // handler 函数名（入站）
	HandlerID string `json:"handler_id"` // handler canonical ID（R37 发射端解析；老索引为空）
	Resolver  string `json:"resolver"`   // 来源：native | gin（入站）
	Register  string `json:"register"`   // 注册调用点（file:line，入站）
	// Q?：入站/出站区分
	Direction string `json:"direction"` // inbound | outbound
	Host      string `json:"host,omitempty"`
	// MatchedInbound 出站专用：同仓库存在同名入站 path（非纯出站——
	// 可能是调用本项目自身 HTTP 接口）；false = 纯出站。
	MatchedInbound bool `json:"matched_inbound,omitempty"`
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
	// 先收集入站 path（出站是否“纯出站”据此判定：无同名入站 path = 纯出站）
	inboundPaths := map[string]bool{}
	for _, n := range nodes {
		if isOutboundRoute(string(n.ID)) {
			continue
		}
		if p := n.Property("path"); p != "" {
			inboundPaths[p] = true
		}
	}
	for _, n := range nodes {
		id := string(n.ID)
		if isOutboundRoute(id) {
			path := n.Property("path")
			res.Routes = append(res.Routes, HTTPRouteEntry{
				Method: n.Property("method"), Path: path,
				Direction: HTTPRouteOutbound, Host: routeHostOf(id),
				MatchedInbound: inboundPaths[path],
			})
			continue
		}
		res.Routes = append(res.Routes, HTTPRouteEntry{
			Method: n.Property("method"), Path: n.Property("path"), Handler: n.Property("handler"),
			HandlerID: n.Property("handler_id"), Resolver: n.Property("resolver"), Register: n.Property("register"),
			Direction: HTTPRouteInbound,
		})
	}
	sort.Slice(res.Routes, func(i, j int) bool {
		a, b := res.Routes[i], res.Routes[j]
		if a.Direction != b.Direction {
			return a.Direction < b.Direction // inbound < outbound
		}
		if a.Direction == HTTPRouteOutbound {
			if a.Host != b.Host {
				return a.Host < b.Host
			}
			if a.Path != b.Path {
				return a.Path < b.Path
			}
			return a.Method < b.Method
		}
		if a.Resolver != b.Resolver {
			return a.Resolver < b.Resolver
		}
		if a.Method != b.Method {
			return a.Method < b.Method
		}
		return a.Path < b.Path
	})
	return res, nil
}

// isOutboundRoute 出站调用目标节点（AST emitHTTP 发射的 symbol:http: 前缀）。
func isOutboundRoute(id string) bool {
	return strings.HasPrefix(id, "symbol:http:")
}

// routeHostOf 从出站节点 ID 提取 host（symbol:http:<host>:route.<path>）。
func routeHostOf(id string) string {
	const p = "symbol:http:"
	if !strings.HasPrefix(id, p) {
		return ""
	}
	rest := strings.TrimPrefix(id, p)
	if i := strings.Index(rest, ":route."); i >= 0 {
		return rest[:i]
	}
	return ""
}

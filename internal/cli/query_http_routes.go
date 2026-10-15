package cli

// R31 `codeintel query http-routes`——HTTP 路由清单（Q1 契约）：构建期
// 两个 resolver 各自识别（原生 net/http + gin）发射 http_route 节点。
// R92：查询逻辑迁 action（Actions.HTTPRoutes）；cli 只做参数转发与输出。
// Q?：明确区分入站（服务端路由）与出站（http_call 目标）；--direction
// 可只看某一方向。

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/schaepher/codeintel/internal/action"
)

// cmdHTTPRoutes 实现 `codeintel query http-routes [--repo <path>] [--json]
// [--direction inbound|outbound]`——HTTP 路由清单（契约化 JSON，Agent 直接
// 解析）。入站 = 本仓库服务端路由（有 handler）；出站 = 调用外部 HTTP 的
// 目标（无 handler；同仓库无同名入站 path 为“纯出站”）。
func cmdHTTPRoutes(acts *action.Actions, f queryFlags) int {
	res, err := acts.HTTPRoutes()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if f.direction != "" && f.direction != action.HTTPRouteInbound && f.direction != action.HTTPRouteOutbound {
		fmt.Fprintf(os.Stderr, "error: --direction 只支持 inbound|outbound\n")
		return 2
	}
	routes := res.Routes
	if f.direction != "" {
		routes = filterHTTPDirection(routes, f.direction)
	}
	if f.json {
		b, err := json.MarshalIndent(action.HTTPRoutesResult{Routes: routes}, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Println(string(b))
		return 0
	}
	// 入站（服务端路由）
	inbound := filterHTTPDirection(routes, action.HTTPRouteInbound)
	outbound := filterHTTPDirection(routes, action.HTTPRouteOutbound)
	if f.direction != action.HTTPRouteOutbound {
		fmt.Printf("入站（服务端路由）: %d 条\n", len(inbound))
		cur := ""
		for _, r := range inbound {
			key := r.Resolver
			if key == "" {
				key = "unknown"
			}
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
	}
	// 出站（http_call 目标）
	if len(outbound) > 0 && f.direction != action.HTTPRouteInbound {
		pure := 0
		for _, r := range outbound {
			if !r.MatchedInbound {
				pure++
			}
		}
		fmt.Printf("\n出站（http_call 目标）: %d 条（纯出站 %d，其余命中同名入站 path）\n", len(outbound), pure)
		for _, r := range outbound {
			m := r.Method
			if m == "" {
				m = "ANY"
			}
			mark := "纯出站"
			if r.MatchedInbound {
				mark = "命中入站 " + r.Path
			}
			fmt.Printf("  %-6s %-40s [%s]\n", m, r.Host+r.Path, mark)
		}
	}
	if len(routes) == 0 {
		fmt.Println("（无 HTTP 路由）")
	}
	fmt.Printf("\n共 %d 条（入站 %d / 出站 %d）\n", len(routes), len(inbound), len(outbound))
	return 0
}

// filterHTTPDirection 按方向过滤（空 = 全部）。
func filterHTTPDirection(routes []action.HTTPRouteEntry, direction string) []action.HTTPRouteEntry {
	if direction == "" {
		return routes
	}
	out := make([]action.HTTPRouteEntry, 0, len(routes))
	for _, r := range routes {
		if r.Direction == direction {
			out = append(out, r)
		}
	}
	return out
}

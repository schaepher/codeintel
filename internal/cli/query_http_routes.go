package cli

// R31 `codeintel query http-routes`——HTTP 路由清单（Q1 契约）：构建期
// 两个 resolver 各自识别（原生 net/http + gin）发射 http_route 节点。
// R92：查询逻辑迁 action（Actions.HTTPRoutes）；cli 只做参数转发与输出。

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/schaepher/codeintel/internal/action"
)

// cmdHTTPRoutes 实现 `codeintel query http-routes [--repo <path>] [--json]`
// ——HTTP 路由清单（契约化 JSON，Agent 直接解析）。
func cmdHTTPRoutes(acts *action.Actions, f queryFlags) int {
	res, err := acts.HTTPRoutes()
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

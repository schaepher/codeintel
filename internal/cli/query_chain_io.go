package cli

// R83 `query grpc-callers <符号>` / `query http-callers <符号>`——查询
// 一个调用链最终调用了哪些 grpc 服务 / http 出站接口。
// R95：查询逻辑迁 action（Actions.ChainGrpcHTTP——缓存/接口具体化在
// action 层）；cli 只做参数转发与输出。

import (
	"fmt"
	"os"

	"github.com/schaepher/codeintel/internal/action"
)

// cmdChainGrpcHTTP 实现 `query grpc-callers|http-callers <符号>`。
func cmdChainGrpcHTTP(acts *action.Actions, symbol string, which string, jsonOut bool) int {
	out, err := acts.ChainGrpcHTTP(action.ChainGrpcHTTPRequest{Symbol: symbol})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if jsonOut {
		encodeJSON(out)
		return 0
	}
	if which == "grpc-callers" {
		if len(out.Grpc) == 0 {
			fmt.Printf("调用链 %s 未调用任何 grpc 服务\n", symbol)
			return 0
		}
		fmt.Printf("== %s 调用链最终调用的 grpc ==\n", symbol)
		for _, g := range out.Grpc {
			loc := ""
			if g.Line > 0 {
				loc = fmt.Sprintf("（%s:%d）", g.CalledBy, g.Line)
			} else {
				loc = "（" + g.CalledBy + "）"
			}
			fmt.Printf("  %s %s\n", g.Service, loc)
		}
		return 0
	}
	if len(out.HTTP) == 0 {
		fmt.Printf("调用链 %s 未调用任何 http 接口\n", symbol)
		return 0
	}
	fmt.Printf("== %s 调用链最终调用的 http ==\n", symbol)
	for _, h := range out.HTTP {
		loc := ""
		if h.Line > 0 {
			loc = fmt.Sprintf("（%s:%d）", h.CalledBy, h.Line)
		} else {
			loc = "（" + h.CalledBy + "）"
		}
		fmt.Printf("  %s %s %s\n", h.Service, h.Path, loc)
	}
	return 0
}

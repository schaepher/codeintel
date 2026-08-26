package cli

// R83 `query ext-chain <符号>`——外部系统调用链：从符号出发查最终
// 调用的 grpc/http（缓存优先），对每个 grpc 服务找服务端实现方法，
// 递归查服务端方法是否再调用其他 grpc/http——直到没有为止。
// http 是外部系统终点（无服务端可查）。
// R95：递归链逻辑迁 action（Actions.ExtChain）；cli 只做参数转发与
// 树状输出（writeExtChain）。

import (
	"fmt"
	"os"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
)

// cmdExtChain 实现 `query ext-chain <符号> [--json]`。
func cmdExtChain(acts *action.Actions, repoAbs, symbol string, jsonOut bool) int {
	root, err := acts.ExtChain(action.ExtChainRequest{Symbol: symbol, RepoAbs: repoAbs})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if jsonOut {
		encodeJSON(root)
		return 0
	}
	if len(root.Grpc) == 0 && len(root.HTTP) == 0 {
		fmt.Printf("%s 未调用任何外部系统（grpc/http）\n", symbol)
		return 0
	}
	fmt.Printf("== %s 外部系统调用链 ==\n", symbol)
	writeExtChain(root, 0)
	return 0
}

// writeExtChain 缩进树渲染（层级：服务 → 服务端方法 → 再调用）。
func writeExtChain(n *action.ExtChainNode, depth int) {
	pad := strings.Repeat("  ", depth)
	for _, g := range n.Grpc {
		fmt.Printf("%s▸ grpc %s", pad, g.Service)
		if g.Line > 0 {
			fmt.Printf("（%s:%d）", g.CalledBy, g.Line)
		} else if g.CalledBy != "" {
			fmt.Printf("（%s）", g.CalledBy)
		}
		fmt.Println()
		for _, s := range g.Server {
			fmt.Printf("%s  服务端 %s\n", pad, shortSymbolNameID(s.Symbol))
			writeExtChain(&s, depth+2)
		}
	}
	for _, h := range n.HTTP {
		loc := ""
		if h.Line > 0 {
			loc = fmt.Sprintf("（%s:%d）", h.CalledBy, h.Line)
		} else {
			loc = "（" + h.CalledBy + "）"
		}
		fmt.Printf("%s▸ http %s %s %s\n", pad, h.Service, h.Path, loc)
	}
}

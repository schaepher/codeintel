package cli

// R83 `query ext-chain <符号>`——外部系统调用链：从符号出发查最终
// 调用的 grpc/http（缓存优先），对每个 grpc 服务找服务端实现方法，
// 递归查服务端方法是否再调用其他 grpc/http——直到没有为止。
// http 是外部系统终点（无服务端可查）。

import (
	"fmt"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// extChainGrpc 一个 grpc 调用 + 服务端方法链（递归）。
type extChainGrpc struct {
	chainCallOut
	Server []extChainNode `json:"server,omitempty"` // 服务端方法（实现）的调用链
}

// extChainNode 调用链节点（递归树）。
type extChainNode struct {
	Symbol string        `json:"symbol"`
	Grpc   []extChainGrpc `json:"grpc,omitempty"`
	HTTP   []chainCallOut `json:"http,omitempty"`
}

// grpcServerMethods 服务名 → 服务端实现方法列表（(Impl).Method canonical ID）。
func grpcServerMethods(acts *action.Actions, repo *sqlite.Repo, repoAbs, svcName string) []string {
	res, err := grpcRoutes(repo, repoAbs)
	if err != nil {
		return nil
	}
	for _, s := range res.Services {
		if s.Name != svcName || s.ImplID == "" {
			continue
		}
		var out []string
		for _, m := range grpcProcMethods(acts, s) {
			if m.Name != "" {
				out = append(out, grpcMethodEntryID(s.ImplID, m.Name))
			}
		}
		return out
	}
	return nil
}

// extChain 递归构建外部系统调用链（服务方法 visited 防环；深度上限 6）。
func extChain(acts *action.Actions, repo *sqlite.Repo, repoAbs, symbol string, visited map[string]bool, depth int) extChainNode {
	node := extChainNode{Symbol: symbol, Grpc: []extChainGrpc{}, HTTP: []chainCallOut{}}
	if depth > 6 {
		return node
	}
	io := chainGrpcHTTPCached(acts, repo, symbol)
	seen := map[string]bool{}
	for _, g := range io.Grpc {
		eg := extChainGrpc{chainCallOut: g}
		// 服务端方法（本仓库实现）——递归查其调用链
		for _, m := range grpcServerMethods(acts, repo, repoAbs, g.Service) {
			key := g.Service + ":" + m
			if seen[key] || visited[m] {
				continue
			}
			seen[key] = true
			visited[m] = true
			sub := extChain(acts, repo, repoAbs, m, visited, depth+1)
			if len(sub.Grpc) > 0 || len(sub.HTTP) > 0 {
				eg.Server = append(eg.Server, sub)
			}
		}
		node.Grpc = append(node.Grpc, eg)
	}
	node.HTTP = io.HTTP
	return node
}

// cmdExtChain 实现 `query ext-chain <符号> [--json]`。
func cmdExtChain(acts *action.Actions, repo *sqlite.Repo, repoAbs, symbol string, jsonOut bool) int {
	root := extChain(acts, repo, repoAbs, symbol, map[string]bool{}, 0)
	if jsonOut {
		encodeJSON(root)
		return 0
	}
	if len(root.Grpc) == 0 && len(root.HTTP) == 0 {
		fmt.Printf("%s 未调用任何外部系统（grpc/http）\n", symbol)
		return 0
	}
	fmt.Printf("== %s 外部系统调用链 ==\n", symbol)
	writeExtChain(&root, 0)
	return 0
}

// writeExtChain 缩进树渲染（层级：服务 → 服务端方法 → 再调用）。
func writeExtChain(n *extChainNode, depth int) {
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

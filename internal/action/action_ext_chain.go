package action

// R95 迁移：`query ext-chain` 查询逻辑（原 cli/query_ext_chain.go）——
// 外部系统调用链：从符号出发查最终调用的 grpc/http（缓存优先），对
// 每个 grpc 服务找服务端实现方法，递归查服务端方法是否再调用其他
// grpc/http——直到没有为止。http 是外部系统终点（无服务端可查）。
// cli 只做参数转发与树状输出（writeExtChain 留 cli）。

import (
	"go.uber.org/zap"
)

// ExtChainGrpc 一个 grpc 调用 + 服务端方法链（递归）。
type ExtChainGrpc struct {
	ChainCallOut
	Server []ExtChainNode `json:"server,omitempty"` // 服务端方法（实现）的调用链
}

// ExtChainNode 调用链节点（递归树）。
type ExtChainNode struct {
	Symbol string         `json:"symbol"`
	Grpc   []ExtChainGrpc `json:"grpc,omitempty"`
	HTTP   []ChainCallOut `json:"http,omitempty"`
}

// ExtChainRequest query ext-chain 参数。
type ExtChainRequest struct {
	Symbol  string // 符号名或 canonical ID
	RepoAbs string // 仓库绝对路径（服务端 ServiceDesc 生成代码解析）
}

// ExtChain 递归构建外部系统调用链（服务方法 visited 防环；深度上限 6）。
func (a *Actions) ExtChain(req ExtChainRequest) (*ExtChainNode, error) {
	logger := zap.L()
	logger.Info("enter (Actions).ExtChain", zap.String("symbol", req.Symbol))
	defer logger.Info("exit (Actions).ExtChain")
	root := a.extChain(req.Symbol, req.RepoAbs, map[string]bool{}, 0)
	return &root, nil
}

// extChain 递归构建外部系统调用链（服务方法 visited 防环；深度上限 6）。
func (a *Actions) extChain(symbol, repoAbs string, visited map[string]bool, depth int) ExtChainNode {
	node := ExtChainNode{Symbol: symbol, Grpc: []ExtChainGrpc{}, HTTP: []ChainCallOut{}}
	if depth > 6 {
		return node
	}
	io := a.chainGrpcHTTPCached(symbol)
	seen := map[string]bool{}
	for _, g := range io.Grpc {
		eg := ExtChainGrpc{ChainCallOut: g}
		// 服务端方法（本仓库实现）——递归查其调用链
		for _, m := range grpcServerMethods(a, repoAbs, g.Service) {
			key := g.Service + ":" + m
			if seen[key] || visited[m] {
				continue
			}
			seen[key] = true
			visited[m] = true
			sub := a.extChain(m, repoAbs, visited, depth+1)
			if len(sub.Grpc) > 0 || len(sub.HTTP) > 0 {
				eg.Server = append(eg.Server, sub)
			}
		}
		node.Grpc = append(node.Grpc, eg)
	}
	node.HTTP = io.HTTP
	return node
}

// grpcServerMethods 服务名 → 服务端实现方法列表（(Impl).Method canonical ID）。
func grpcServerMethods(a *Actions, repoAbs, svcName string) []string {
	res, err := a.GrpcRoutes(repoAbs)
	if err != nil {
		return nil
	}
	for _, s := range res.Services {
		if s.Name != svcName || s.ImplID == "" {
			continue
		}
		var out []string
		for _, m := range a.GrpcProcMethods(s) {
			if m.Name != "" {
				out = append(out, GrpcMethodEntryID(s.ImplID, m.Name))
			}
		}
		return out
	}
	return nil
}

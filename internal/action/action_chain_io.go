package action

// R95 迁移：`query grpc-callers` / `query http-callers` 查询逻辑（原
// cli/query_chain_io.go + chain_symbols.go）——查询一个调用链最终调用
// 了哪些 grpc 服务 / http 出站接口。递归展开 CalleesConcrete（接口
// 具体化），收集链上每个符号的出站 grpc/http 调用边。结果缓存
// ext_chain_cache（索引 commit 变化自动失效）。cli 只做参数转发与输出。

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// ChainCallOut 调用链里的一个 grpc/http 出站调用。
type ChainCallOut struct {
	Service  string `json:"service"`            // grpc 服务名 / http host
	Method   string `json:"method,omitempty"`   // grpc 方法名（调用点未记录时为空）
	Path     string `json:"path,omitempty"`     // http path
	CalledBy string `json:"called_by"`          // 调用点符号短名
	Line     int    `json:"line,omitempty"`     // 调用行号
}

// ChainIOOut 输出契约。
type ChainIOOut struct {
	Symbol string         `json:"symbol"`
	Grpc   []ChainCallOut `json:"grpc,omitempty"`
	HTTP   []ChainCallOut `json:"http,omitempty"`
}

// ChainGrpcHTTPRequest query grpc-callers/http-callers 参数。
type ChainGrpcHTTPRequest struct {
	Symbol string // 符号名或 canonical ID
}

// ChainGrpcHTTP 收集调用链的 grpc/http 出站调用（缓存优先——结果存
// ext_chain_cache，索引 commit 变化自动失效；一次全量扫描：链符号
// BFS + 出站边过滤，避免逐符号查询）。
func (a *Actions) ChainGrpcHTTP(req ChainGrpcHTTPRequest) (*ChainIOOut, error) {
	logger := zap.L()
	logger.Info("enter (Actions).ChainGrpcHTTP", zap.String("symbol", req.Symbol))
	defer logger.Info("exit (Actions).ChainGrpcHTTP")
	out := a.chainGrpcHTTPCached(req.Symbol)
	return &out, nil
}

// chainGrpcHTTPCached 缓存优先的 chainGrpcHTTP（R83：外部系统调用链
// 递归查询的核心——结果缓存数据库，后续优先查；索引 commit 变化
// 自动失效）。
func (a *Actions) chainGrpcHTTPCached(symbol string) ChainIOOut {
	_ = a.repo.EnsureExtChainCache()
	sha := currentBuildSHA(a)
	if sha != "" {
		if result, ok := a.repo.ExtChainCacheGet(symbol, sha); ok {
			var cached ChainIOOut
			if err := json.Unmarshal([]byte(result), &cached); err == nil {
				return cached
			}
		}
	}
	out := chainGrpcHTTP(a, symbol)
	if sha != "" {
		if b, err := json.Marshal(out); err == nil {
			_ = a.repo.ExtChainCacheSet(symbol, sha, string(b))
		}
	}
	return out
}

// currentBuildSHA 当前索引 commit（缓存失效键——索引变化自动失效）。
func currentBuildSHA(a *Actions) string {
	meta, err := a.repo.GetLatest()
	if err != nil || meta == nil {
		return ""
	}
	return meta.CommitSHA
}

// chainGrpcHTTP 收集调用链的 grpc/http 出站调用（一次全量扫描——
// 链符号 BFS + 出站边过滤，避免逐符号查询）。
func chainGrpcHTTP(a *Actions, symbol string) ChainIOOut {
	out := ChainIOOut{Symbol: symbol, Grpc: []ChainCallOut{}, HTTP: []ChainCallOut{}}
	n, err := a.ResolveSymbol(symbol)
	if err != nil {
		return out
	}
	svcs := grpcSvcNames(a)
	seen := chainSymbols(a, string(n.ID))
	// 一次加载链内符号的全部出站边（grpc_call/http_call——metadata 全量）
	if facts, err := a.repo.GetFactsByKinds(string(domain.FactGrpcCall), string(domain.FactHTTPCall)); err == nil {
		for _, f := range facts {
			src := string(f.SourceID)
			if !seen[src] {
				continue
			}
			by := shortNameOf(src)
			if f.Kind == domain.FactHTTPCall {
				out.HTTP = append(out.HTTP, ChainCallOut{Service: metaStr(f.Metadata, "host"),
					Path: metaStr(f.Metadata, "path"), CalledBy: by, Line: metaLine(f.Metadata)})
			} else {
				svc := strings.TrimPrefix(string(f.TargetID), "symbol:go:")
				if i := strings.LastIndex(svc, ":"); i >= 0 {
					svc = strings.TrimPrefix(svc[i+1:], "svc.")
				}
				out.Grpc = append(out.Grpc, ChainCallOut{Service: svc, CalledBy: by, Line: metaLine(f.Metadata)})
			}
		}
	}
	// calls 边里的 grpc 客户端类型调用（构造/方法）
	if facts, err := a.repo.GetFactsByKinds(string(domain.FactCalls)); err == nil {
		for _, f := range facts {
			src := string(f.SourceID)
			if !seen[src] {
				continue
			}
			if svc := grpcClientName(string(f.TargetID), svcs); svc != "" {
				out.Grpc = append(out.Grpc, ChainCallOut{Service: svc, CalledBy: shortNameOf(src), Line: metaLine(f.Metadata)})
			}
		}
	}
	sortOut := func(xs []ChainCallOut) {
		sort.Slice(xs, func(i, j int) bool {
			if xs[i].Service != xs[j].Service {
				return xs[i].Service < xs[j].Service
			}
			return xs[i].CalledBy < xs[j].CalledBy
		})
	}
	sortOut(out.Grpc)
	sortOut(out.HTTP)
	return out
}

// chainSymbols 递归收集调用链符号（BFS——一次加载全部 calls/grpc_call
// 边做内存遍历，去重，上限 300 防爆炸）。R84：接口不停止解析——链上
// 遇到接口方法/类型（grpc 服务入口接口是动态入口，无直接 caller）经
// implements 边具体化到实现（接口类型 → 首个实现；接口方法 → 首个有
// 该方法者——与 InterfaceMethodImpl 语义一致，控制膨胀）。
func chainSymbols(a *Actions, root string) map[string]bool {
	adj := map[string][]string{}
	if facts, err := a.repo.GetFactsByKinds(string(domain.FactCalls), string(domain.FactGrpcCall)); err == nil {
		for _, f := range facts {
			adj[string(f.SourceID)] = append(adj[string(f.SourceID)], string(f.TargetID))
		}
	}

	ifaces := map[string][]string{}
	if facts, err := a.repo.GetImplementsEdges(); err == nil {
		for _, f := range facts {
			ifaces[string(f.SourceID)] = append(ifaces[string(f.SourceID)], string(f.TargetID))
		}
	}
	nodeSet := map[string]bool{}
	if ids, err := a.repo.AllNodeIDs(); err == nil {
		for _, id := range ids {
			nodeSet[string(id)] = true
		}
	}

	ifaceConcrete := func(t string) []string {
		var out []string
		if i := strings.Index(t, ":("); i >= 0 {
			pkg := t[:i]
			rest := t[i+2:]
			if j := strings.Index(rest, ")."); j >= 0 {
				iface := pkg + ":" + rest[:j]
				method := rest[j+2:]
				for _, impl := range ifaces[iface] {
					mi := strings.LastIndex(impl, ":")
					if mi < 0 {
						continue
					}
					m := impl[:mi+1] + "(" + impl[mi+1:] + ")." + method
					if nodeSet[m] {
						out = append(out, m)
						break
					}
				}
			}
		} else if impls, ok := ifaces[t]; ok && len(impls) > 0 {
			out = append(out, impls[0])
		}
		return out
	}
	seen := map[string]bool{}
	queue := []string{root}
	for len(queue) > 0 && len(seen) < 300 {
		id := queue[0]
		queue = queue[1:]
		if seen[id] {
			continue
		}
		seen[id] = true
		next := append([]string{}, adj[id]...)
		next = append(next, ifaceConcrete(id)...)
		for _, t := range next {
			if !seen[t] {
				queue = append(queue, t)
			}
		}
	}
	return seen
}

// grpcSvcNames 全部 grpc 服务名集合（内存——grpc 客户端判定）。
func grpcSvcNames(a *Actions) map[string]bool {
	out := map[string]bool{}
	svcs, err := a.repo.GetGrpcServices()
	if err != nil {
		return out
	}
	for _, n := range svcs {
		if name := n.Property("service_name"); name != "" {
			out[name] = true
		}
	}
	return out
}

// grpcClientName 被调符号是否为 grpc 客户端类型——短名 `<Svc>Client`
// 且 Svc 是已知 grpc 服务名（内存判定）。返回服务名。
func grpcClientName(symID string, svcs map[string]bool) string {
	rest := strings.TrimPrefix(symID, "symbol:go:")
	name := rest
	if i := strings.LastIndex(rest, ":"); i >= 0 {
		name = rest[i+1:]
	}
	if !strings.HasSuffix(name, "Client") {
		return ""
	}
	svc := strings.TrimSuffix(name, "Client")
	if svc != "" && svcs[svc] {
		return svc
	}
	return ""
}

// metaLine metadata line_num 提取（JSON 反序列化为 float64）。
func metaLine(m map[string]any) int {
	if v, ok := m["line_num"]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		}
	}
	return 0
}

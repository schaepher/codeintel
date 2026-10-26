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
	Service string `json:"service"` // grpc 服务名 / http host
	// todo13-16：服务节点的完整身份（proto 包 + 服务）——只有短服务名
	// 无法消歧同名服务（同名不同 proto 包会串线）。
	ServiceID string `json:"service_id,omitempty"`
	Method    string `json:"method,omitempty"` // grpc 方法名（调用点未记录时为空）
	Path      string `json:"path,omitempty"`   // http path
	CalledBy  string `json:"called_by"`        // 调用点符号短名
	Line      int    `json:"line,omitempty"`   // 调用行号
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
	out, _ := a.chainGrpcHTTPCachedDiag(symbol)
	return out
}

// chainGrpcHTTPCachedDiag 同 chainGrpcHTTPCached，但把读取失败作为诊断返回
// （todo12-2：读取失败 ≠ 合法无下游；失败结果不写缓存）。
func (a *Actions) chainGrpcHTTPCachedDiag(symbol string) (ChainIOOut, []SeqDiagCount) {
	_ = a.repo.EnsureExtChainCache()
	sha := currentBuildSHA(a)
	if sha != "" {
		if result, ok := a.repo.ExtChainCacheGet(symbol, sha); ok {
			var cached ChainIOOut
			if err := json.Unmarshal([]byte(result), &cached); err == nil {
				return cached, nil
			}
		}
	}
	out, diags := chainGrpcHTTPDiag(a, symbol)
	if len(diags) == 0 && sha != "" {
		if b, err := json.Marshal(out); err == nil {
			_ = a.repo.ExtChainCacheSet(symbol, sha, string(b))
		}
	}
	return out, diags
}

// currentBuildSHA 当前索引 commit（缓存失效键——索引变化自动失效）。
func currentBuildSHA(a *Actions) string {
	meta, err := a.repo.GetLatest()
	if err != nil || meta == nil {
		return ""
	}
	return meta.CommitSHA
}

// chainGrpcHTTP 收集调用链的 grpc/http 出站调用（一次全量扫描）。
func chainGrpcHTTP(a *Actions, symbol string) ChainIOOut {
	out, _ := chainGrpcHTTPDiag(a, symbol)
	return out
}

// chainGrpcHTTPDiag 同 chainGrpcHTTP，另返回读取失败诊断（todo12-2）。
func chainGrpcHTTPDiag(a *Actions, symbol string) (ChainIOOut, []SeqDiagCount) {
	out := ChainIOOut{Symbol: symbol, Grpc: []ChainCallOut{}, HTTP: []ChainCallOut{}}
	var diags []SeqDiagCount
	n, err := a.ResolveSymbol(symbol)
	if err != nil {
		return out, []SeqDiagCount{{Code: ExtStopQueryFailed, Count: 1,
			Detail: "符号读取失败（" + symbol + "）：" + err.Error()}}
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
				// todo10-4/11-1：RPC 方法名来自**关系身份** edge_key（旧的
				// metadata.method 作回退）；同一服务不同方法现在是不同的边，
				// ext-chain 才能逐方法精确续接
				method := f.Key
				if method == "" {
					method = metaStr(f.Metadata, "method")
				}
				out.Grpc = append(out.Grpc, ChainCallOut{Service: svc, ServiceID: string(f.TargetID),
					CalledBy: by, Method: method, Line: metaLine(f.Metadata)})
			}
		}
	} else {
		diags = append(diags, SeqDiagCount{Code: ExtStopQueryFailed, Count: 1,
			Detail: "读取 grpc/http 出站边失败：" + err.Error()})
	}
	// calls 边里的 grpc 客户端类型调用（构造/方法）——弱信号：同调用方已有
	// grpc_call 边（带 RPC 方法名）时跳过（todo10-4：避免同一次调用重复计入，
	// 且无方法名的那条无法精确续接）
	haveGrpcCall := map[string]bool{}
	for _, g := range out.Grpc {
		haveGrpcCall[g.Service+"\x00"+g.CalledBy] = true
	}
	if facts, err := a.repo.GetFactsByKinds(string(domain.FactCalls)); err == nil {
		for _, f := range facts {
			src := string(f.SourceID)
			if !seen[src] {
				continue
			}
			if svc := grpcClientName(string(f.TargetID), svcs); svc != "" {
				if haveGrpcCall[svc+"\x00"+shortNameOf(src)] {
					continue
				}
				out.Grpc = append(out.Grpc, ChainCallOut{Service: svc, ServiceID: uniqueSvcID(svc, a),
					CalledBy: shortNameOf(src), Line: metaLine(f.Metadata)})
			}
		}
	} else {
		diags = append(diags, SeqDiagCount{Code: ExtStopQueryFailed, Count: 1,
			Detail: "读取 calls 边失败：" + err.Error()})
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
	return out, diags
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


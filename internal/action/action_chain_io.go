package action

// R95 迁移：`query grpc-callers` / `query http-callers` 查询逻辑（原
// cli/query_chain_io.go + chain_symbols.go）——查询一个调用链最终调用
// 了哪些 grpc 服务 / http 出站接口。递归展开 CalleesConcrete（接口
// 具体化），收集链上每个符号的出站 grpc/http 调用边。结果缓存
// ext_chain_cache（索引 commit 变化自动失效）。cli 只做参数转发与输出。

import (
	"context"
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
	// todo22 P1：逐节点查链内符号的出边（不再全量扫 grpc_call/http_call）。
	// 遍历顺序按符号 ID 排序，保证同输入同输出（map 迭代顺序不参与结果）。
	chainIDs := make([]string, 0, len(seen))
	for id := range seen {
		chainIDs = append(chainIDs, id)
	}
	sort.Strings(chainIDs)
	ctx := context.Background()
	for _, src := range chainIDs {
		facts, err := a.repo.GetOutgoingFactsByKinds(ctx, domain.CanonicalID(src),
			string(domain.FactGrpcCall), string(domain.FactHTTPCall))
		if err != nil {
			diags = append(diags, SeqDiagCount{Code: ExtStopQueryFailed, Count: 1,
				Detail: "读取 grpc/http 出站边失败：" + err.Error()})
			continue
		}
		by := shortNameOf(src)
		for _, f := range facts {
			if f.Kind == domain.FactHTTPCall {
				out.HTTP = append(out.HTTP, ChainCallOut{Service: metaStr(f.Metadata, "host"),
					Path: metaStr(f.Metadata, "path"), CalledBy: by, Line: metaLine(f.Metadata)})
				continue
			}
			svc := strings.TrimPrefix(string(f.TargetID), "symbol:go:")
			if i := strings.LastIndex(svc, ":"); i >= 0 {
				svc = strings.TrimPrefix(svc[i+1:], "svc.")
			}
			// todo10-4/11-1：RPC 方法名来自**关系身份** edge_key（旧的
			// metadata.method 作回退）；同一服务不同方法现在是不同的边，
			// ext-chain 才能逐方法精确续接。
			method := f.Key
			if method == "" {
				method = metaStr(f.Metadata, "method")
			}
			out.Grpc = append(out.Grpc, ChainCallOut{Service: svc, ServiceID: string(f.TargetID),
				CalledBy: by, Method: method, Line: metaLine(f.Metadata)})
		}
	}
	// calls 边里的 grpc 客户端类型调用（构造/方法）——弱信号：同调用方已有
	// grpc_call 边（带 RPC 方法名）时跳过（todo10-4：避免同一次调用重复计入，
	// 且无方法名的那条无法精确续接）
	haveGrpcCall := map[string]bool{}
	for _, g := range out.Grpc {
		haveGrpcCall[g.Service+"\x00"+g.CalledBy] = true
	}
	for _, src := range chainIDs {
		facts, err := a.repo.GetOutgoingFactsByKinds(ctx, domain.CanonicalID(src), string(domain.FactCalls))
		if err != nil {
			diags = append(diags, SeqDiagCount{Code: ExtStopQueryFailed, Count: 1,
				Detail: "读取 calls 边失败：" + err.Error()})
			continue
		}
		for _, f := range facts {
			if svc := grpcClientName(string(f.TargetID), svcs); svc != "" {
				if haveGrpcCall[svc+"\x00"+shortNameOf(src)] {
					continue
				}
				out.Grpc = append(out.Grpc, ChainCallOut{Service: svc, ServiceID: uniqueSvcID(svc, a),
					CalledBy: shortNameOf(src), Line: metaLine(f.Metadata)})
			}
		}
	}
	sortOut := func(xs []ChainCallOut) {
		sort.Slice(xs, func(i, j int) bool {
			if xs[i].Service != xs[j].Service {
				return xs[i].Service < xs[j].Service
			}
			if xs[i].CalledBy != xs[j].CalledBy {
				return xs[i].CalledBy < xs[j].CalledBy
			}
			if xs[i].Method != xs[j].Method {
				return xs[i].Method < xs[j].Method
			}
			if xs[i].Path != xs[j].Path {
				return xs[i].Path < xs[j].Path
			}
			return xs[i].Line < xs[j].Line
		})
	}
	sortOut(out.Grpc)
	sortOut(out.HTTP)
	return out, diags
}

// chainSymbols 递归收集调用链符号（BFS——**逐节点**查询出边，不加载全量
// calls/grpc_call/implements 边；去重，上限 300 防爆炸）。R84：接口不停止
// 解析——链上遇到接口方法/类型（grpc 服务入口接口是动态入口，无直接
// caller）经 implements 精确邻接具体化到实现（接口类型 → 首个实现；接口
// 方法 → 首个有该方法者——与 InterfaceMethodImpl 语义一致，控制膨胀）。
//
// todo22 P1：旧实现一次性 `GetFactsByKinds` + `GetImplementsEdges` +
// `AllNodeIDs` 把全库边/节点读进内存（大仓几十万边）；现在每个节点只查
// 自己的整数邻接（calls/grpc_call 出边 + 该接口的 implements 目标），
// 节点存在性用精确 `GetSymbol`，语义与旧实现逐项等价（排序/首个实现
// 规则保持）。
func chainSymbols(a *Actions, root string) map[string]bool {
	ctx := context.Background()
	seen := map[string]bool{}
	queue := []string{root}
	outCache := map[string][]string{}  // 已查节点的出边目标（calls/grpc_call）
	implCache := map[string][]string{} // 接口 → implements 目标
	existsCache := map[string]bool{}   // 方法 ID 存在性（精确查询缓存）

	outTargets := func(id string) []string {
		if out, ok := outCache[id]; ok {
			return out
		}
		facts, err := a.repo.GetOutgoingFactsByKinds(ctx, domain.CanonicalID(id),
			string(domain.FactCalls), string(domain.FactGrpcCall))
		if err != nil {
			facts = nil
		}
		out := make([]string, 0, len(facts))
		for _, f := range facts {
			out = append(out, string(f.TargetID))
		}
		outCache[id] = out
		return out
	}

	ifaceImpls := func(iface string) []string {
		if impls, ok := implCache[iface]; ok {
			return impls
		}
		impls, err := a.repo.InterfaceImplsExact(iface)
		if err != nil {
			impls = nil
		}
		implCache[iface] = impls
		return impls
	}

	nodeExists := func(id string) bool {
		if ok, hit := existsCache[id]; hit {
			return ok
		}
		_, err := a.repo.GetSymbol(domain.CanonicalID(id))
		ok := err == nil
		existsCache[id] = ok
		return ok
	}

	ifaceConcrete := func(t string) []string {
		if i := strings.Index(t, ":("); i >= 0 {
			pkg := t[:i]
			rest := t[i+2:]
			j := strings.Index(rest, ").")
			if j < 0 {
				return nil
			}
			iface := pkg + ":" + rest[:j]
			method := rest[j+2:]
			for _, impl := range ifaceImpls(iface) {
				mi := strings.LastIndex(impl, ":")
				if mi < 0 {
					continue
				}
				m := impl[:mi+1] + "(" + impl[mi+1:] + ")." + method
				if nodeExists(m) {
					return []string{m}
				}
			}
			return nil
		}
		if impls := ifaceImpls(t); len(impls) > 0 {
			return impls[:1]
		}
		return nil
	}

	for len(queue) > 0 && len(seen) < 300 {
		id := queue[0]
		queue = queue[1:]
		if seen[id] {
			continue
		}
		seen[id] = true
		next := append([]string{}, outTargets(id)...)
		next = append(next, ifaceConcrete(id)...)
		for _, t := range next {
			if !seen[t] {
				queue = append(queue, t)
			}
		}
	}
	return seen
}

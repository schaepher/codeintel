package action

// R95 迁移：`query ext-chain` 查询逻辑（原 cli/query_ext_chain.go）——
// 外部系统调用链：从符号出发查最终调用的 grpc/http（缓存优先），对
// 每个 grpc 服务找服务端实现**同一个 RPC 方法**，递归查该方法是否再
// 调用其他 grpc/http——直到没有为止。http 是外部系统终点（无服务端）。
//
// todo10-4：精确方法匹配 + 跨仓续接 + 明确诊断
//   - 按完整 RPC 方法匹配目标实现（此前把同服务**全部方法**都当续接候选，
//     调 Submit 会混入 Cancel 的下游）
//   - 目标仓库经注册表候选列表解析（cli 注入 CandidateRepos/Open），
//     递归使用目标仓库的查询视图
//   - 输出源方法/目标方法/仓库/匹配依据；方法找不到、仓库不可用、
//     深度上限、循环都有 code 诊断
// cli 只做参数转发与树状输出（writeExtChain 留 cli）。

import (
	"go.uber.org/zap"
)

// ExtChainGrpc 一个 grpc 调用 + 服务端方法链（递归）。
type ExtChainGrpc struct {
	ChainCallOut
	Server []ExtChainNode `json:"server,omitempty"` // 服务端方法（实现）的调用链
	// todo10-4：续接依据（源方法 → 目标方法、目标仓库、匹配规则）
	SourceMethod string `json:"source_method,omitempty"`
	TargetMethod string `json:"target_method,omitempty"`
	TargetRepo   string `json:"target_repo,omitempty"`
	// todo13-16：完整服务身份（proto 包 + 服务）——短服务名不能作为唯一依据
	SourceServiceID string   `json:"source_service_id,omitempty"`
	TargetServiceID string   `json:"target_service_id,omitempty"`
	MatchReason     string   `json:"match_reason,omitempty"` // proto-identity|proto-name|go-method
	Stop            *SeqStop `json:"stop,omitempty"`         // 未续接原因（诊断）
	// todo12-2：目标读取/候选仓库探测失败——已匹配目标但查询失败不能
	// 静默变成“无下游”（partial 与“合法无下游”分开表示）
	Partial     bool           `json:"partial,omitempty"`
	Diagnostics []SeqDiagCount `json:"diagnostics,omitempty"`
}

// ExtChainNode 调用链节点（递归树）。
type ExtChainNode struct {
	Symbol string         `json:"symbol"`
	Repo   string         `json:"repo,omitempty"` // 该节点所在仓库（todo10-4 跨仓可见）
	// todo13-15：候选范围与依据（workspace 限定时不含范围外仓库）
	Scope          string   `json:"scope,omitempty"`
	ScopeReason    string   `json:"scope_reason,omitempty"`
	CandidateRepos []string `json:"candidate_repos,omitempty"`
	Grpc   []ExtChainGrpc `json:"grpc,omitempty"`
	HTTP   []ChainCallOut `json:"http,omitempty"`
	// todo12-2：本节点查询不完整（读取失败/预算/候选探测失败）
	Partial     bool           `json:"partial,omitempty"`
	Diagnostics []SeqDiagCount `json:"diagnostics,omitempty"`
}

// ExtChainRepoOpener 打开目标仓库的查询视图（cli 注入——action 不依赖
// 注册表与 DB 打开细节）。返回 actions 与 close（调用方负责关闭）。
type ExtChainRepoOpener func(repoPath string) (*Actions, func() error, error)

// ExtChainRequest query ext-chain 参数。
type ExtChainRequest struct {
	Symbol         string
	RepoAbs        string             // 起始仓库绝对路径
	CandidateRepos []string           // 其它已注册仓库（cli 按 workspace 范围提供）
	Open           ExtChainRepoOpener // 打开候选仓库（nil = 只看起始仓库）
	// todo13-15：候选范围标签/依据（workspace:<dir> | all-registered | registered）
	Scope       string
	ScopeReason string
}

// ext-chain 诊断代码（分析停止原因，与 seq 共用 SeqStop 结构）。
const (
	ExtStopMethodNotFound = "method_not_found" // 服务端没有该 RPC 方法
	ExtStopRepoMissing    = "repo_unavailable" // 没有任何仓库实现该服务
	ExtStopDepth          = "depth"            // 深度上限
	ExtStopCycle          = "cycle"            // 同一服务方法重复出现
	ExtStopQueryFailed    = "query_failed"     // 目标读取失败（索引/DB）——不是“无下游”
	// todo13-16：同名服务在多个 proto 包/仓库实现，且调用侧没有完整身份
	// （或完整身份不在候选范围）——列出候选，不静默取第一个。
	ExtStopServiceAmbiguous = "service_identity_ambiguous"
)

// ExtChain 递归构建外部系统调用链（服务方法 visited 防环；深度上限 6）。
func (a *Actions) ExtChain(req ExtChainRequest) (*ExtChainNode, error) {
	logger := zap.L()
	logger.Info("enter (Actions).ExtChain", zap.String("symbol", req.Symbol))
	defer logger.Info("exit (Actions).ExtChain")
	root := a.extChain(req, a, req.RepoAbs, map[string]bool{}, 0)
	root.Scope, root.ScopeReason, root.CandidateRepos = req.Scope, req.ScopeReason, req.CandidateRepos
	return &root, nil
}

// extChain 递归构建外部系统调用链（acts/repoAbs = 当前仓库的查询视图；
// 跨仓续接时切换到目标仓库的视图）。
func (a *Actions) extChain(req ExtChainRequest, acts *Actions, repoAbs string, visited map[string]bool, depth int) ExtChainNode {
	node := ExtChainNode{Symbol: req.Symbol, Repo: repoAbs, Grpc: []ExtChainGrpc{}, HTTP: []ChainCallOut{}}
	if depth > 6 {
		node.Grpc = append(node.Grpc, ExtChainGrpc{
			Stop: &SeqStop{Code: ExtStopDepth, Detail: "ext-chain 深度上限 6"},
		})
		return node
	}
	io, diags := acts.chainGrpcHTTPCachedDiag(req.Symbol)
	if len(diags) > 0 {
		node.Partial = true
		node.Diagnostics = append(node.Diagnostics, diags...)
	}
	for _, g := range io.Grpc {
		eg := ExtChainGrpc{ChainCallOut: g, SourceMethod: req.Symbol}
		if g.Method == "" {
			// 调用点未记录方法名 → 无法精确续接（不得把整个服务当候选）
			eg.Stop = &SeqStop{Code: ExtStopMethodNotFound,
				Detail: "调用点未记录 RPC 方法名（" + g.Service + "）——无法精确续接"}
			node.Grpc = append(node.Grpc, eg)
			continue
		}
		eg.SourceServiceID = g.ServiceID
		target, stop := resolveRemoteMethod(req, acts, repoAbs, g.ServiceID, g.Service, g.Method)
		if stop != nil {
			eg.Stop = stop
			if stop.Code == ExtStopServiceAmbiguous {
				eg.Partial = true
			}
			node.Grpc = append(node.Grpc, eg)
			continue
		}
		eg.TargetMethod, eg.TargetRepo, eg.MatchReason = target.methodID, target.repo, target.reason
		eg.TargetServiceID = target.serviceID
		if len(target.diags) > 0 {
			eg.Partial = true
			eg.Diagnostics = append(eg.Diagnostics, target.diags...)
		}
		// todo12-2：目标查询视图的所有权保留到本次递归结束（此前在
		// resolveRemoteMethod 里 defer 关闭 → 递归时 DB 已关，server
		// 链静默为空——正是“匹配到目标但下游丢失”的根因）。
		if stop := probeTarget(target); stop != nil {
			eg.Stop = stop
			eg.Partial = true
			eg.Diagnostics = append(eg.Diagnostics, seqDiagOf(stop))
			target.release()
			node.Grpc = append(node.Grpc, eg)
			continue
		}
		if visited[target.methodID] {
			eg.Stop = &SeqStop{Code: ExtStopCycle, Detail: "服务方法重复出现（" + target.methodID + "）"}
			target.release()
			node.Grpc = append(node.Grpc, eg)
			continue
		}
		visited[target.methodID] = true
		sub := target.acts.extChain(
			ExtChainRequest{Symbol: target.methodID, RepoAbs: target.repo,
				CandidateRepos: req.CandidateRepos, Open: req.Open},
			target.acts, target.repo, visited, depth+1)
		target.release()
		if len(sub.Grpc) > 0 || len(sub.HTTP) > 0 {
			eg.Server = append(eg.Server, sub)
		}
		if sub.Partial {
			eg.Partial = true
		}
		node.Grpc = append(node.Grpc, eg)
	}
	node.HTTP = io.HTTP
	return node
}

// probeTarget 目标方法在目标仓库视图里可读吗（读失败 ≠ 合法无下游）。
func probeTarget(t *remoteTarget) *SeqStop {
	if t == nil || t.acts == nil {
		return &SeqStop{Code: ExtStopQueryFailed, Detail: "目标仓库查询视图不可用"}
	}
	if _, err := t.acts.ResolveSymbol(t.methodID); err != nil {
		return &SeqStop{Code: ExtStopQueryFailed,
			Detail: "目标方法读取失败（" + t.methodID + "）：" + err.Error()}
	}
	return nil
}

// seqDiagOf 把停止原因转成诊断计数（保留 detail，便于定位）。
func seqDiagOf(st *SeqStop) SeqDiagCount {
	if st == nil {
		return SeqDiagCount{}
	}
	return SeqDiagCount{Code: st.Code, Count: 1, Detail: st.Detail}
}

// remoteTarget 跨仓续接目标：仓库 + 服务端方法入口 + 匹配依据。

// release 关闭目标查询视图（幂等；不重复关闭）。
func (t *remoteTarget) release() {
	if t == nil || t.closeFn == nil {
		return
	}
	_ = t.closeFn()
	t.closeFn = nil
}

// resolveRemoteMethod 在"当前仓库 + 注册表候选仓库"里找该服务的**同名 RPC
// 方法**实现（起始仓库优先）。找不到时返回带 code 的诊断（方法缺失时附
// 该服务的方法清单，便于判断是没实现还是名字不同）。

// uniqueStrings 去重（保持顺序）——诊断里的方法清单用。

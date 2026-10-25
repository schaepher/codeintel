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
	"strings"

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
	MatchReason  string `json:"match_reason,omitempty"` // proto-name|go-method
	Stop         *SeqStop `json:"stop,omitempty"`      // 未续接原因（诊断）
}

// ExtChainNode 调用链节点（递归树）。
type ExtChainNode struct {
	Symbol string         `json:"symbol"`
	Repo   string         `json:"repo,omitempty"` // 该节点所在仓库（todo10-4 跨仓可见）
	Grpc   []ExtChainGrpc `json:"grpc,omitempty"`
	HTTP   []ChainCallOut `json:"http,omitempty"`
}

// ExtChainRepoOpener 打开目标仓库的查询视图（cli 注入——action 不依赖
// 注册表与 DB 打开细节）。返回 actions 与 close（调用方负责关闭）。
type ExtChainRepoOpener func(repoPath string) (*Actions, func() error, error)

// ExtChainRequest query ext-chain 参数。
type ExtChainRequest struct {
	Symbol         string
	RepoAbs        string             // 起始仓库绝对路径
	CandidateRepos []string           // 其它已注册仓库（cli 从注册表提供）
	Open           ExtChainRepoOpener // 打开候选仓库（nil = 只看起始仓库）
}

// ext-chain 诊断代码（分析停止原因，与 seq 共用 SeqStop 结构）。
const (
	ExtStopMethodNotFound = "method_not_found" // 服务端没有该 RPC 方法
	ExtStopRepoMissing    = "repo_unavailable" // 没有任何仓库实现该服务
	ExtStopDepth          = "depth"            // 深度上限
	ExtStopCycle          = "cycle"            // 同一服务方法重复出现
)

// ExtChain 递归构建外部系统调用链（服务方法 visited 防环；深度上限 6）。
func (a *Actions) ExtChain(req ExtChainRequest) (*ExtChainNode, error) {
	logger := zap.L()
	logger.Info("enter (Actions).ExtChain", zap.String("symbol", req.Symbol))
	defer logger.Info("exit (Actions).ExtChain")
	root := a.extChain(req, a, req.RepoAbs, map[string]bool{}, 0)
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
	io := acts.chainGrpcHTTPCached(req.Symbol)
	for _, g := range io.Grpc {
		eg := ExtChainGrpc{ChainCallOut: g, SourceMethod: req.Symbol}
		if g.Method == "" {
			// 调用点未记录方法名 → 无法精确续接（不得把整个服务当候选）
			eg.Stop = &SeqStop{Code: ExtStopMethodNotFound,
				Detail: "调用点未记录 RPC 方法名（" + g.Service + "）——无法精确续接"}
			node.Grpc = append(node.Grpc, eg)
			continue
		}
		target, stop := resolveRemoteMethod(req, acts, repoAbs, g.Service, g.Method)
		if stop != nil {
			eg.Stop = stop
			node.Grpc = append(node.Grpc, eg)
			continue
		}
		eg.TargetMethod, eg.TargetRepo, eg.MatchReason = target.methodID, target.repo, target.reason
		if visited[target.methodID] {
			eg.Stop = &SeqStop{Code: ExtStopCycle, Detail: "服务方法重复出现（" + target.methodID + "）"}
			node.Grpc = append(node.Grpc, eg)
			continue
		}
		visited[target.methodID] = true
		sub := target.acts.extChain(
			ExtChainRequest{Symbol: target.methodID, RepoAbs: target.repo,
				CandidateRepos: req.CandidateRepos, Open: req.Open},
			target.acts, target.repo, visited, depth+1)
		if len(sub.Grpc) > 0 || len(sub.HTTP) > 0 {
			eg.Server = append(eg.Server, sub)
		}
		node.Grpc = append(node.Grpc, eg)
	}
	node.HTTP = io.HTTP
	return node
}

// remoteTarget 跨仓续接目标：仓库 + 服务端方法入口 + 匹配依据。
type remoteTarget struct {
	acts     *Actions
	repo     string
	methodID string
	reason   string // proto-name | go-method
}

// resolveRemoteMethod 在"当前仓库 + 注册表候选仓库"里找该服务的**同名 RPC
// 方法**实现（起始仓库优先）。找不到时返回带 code 的诊断（方法缺失时附
// 该服务的方法清单，便于判断是没实现还是名字不同）。
func resolveRemoteMethod(req ExtChainRequest, startActs *Actions, startRepo, svc, rpcMethod string) (*remoteTarget, *SeqStop) {
	type repoView struct {
		acts *Actions
		repo string
	}
	views := []repoView{{startActs, startRepo}}
	searched := []string{startRepo}
	var closes []func() error
	if req.Open != nil {
		for _, p := range req.CandidateRepos {
			if p == "" || p == startRepo {
				continue
			}
			acts, closeFn, err := req.Open(p)
			if err != nil || acts == nil {
				searched = append(searched, p+"（不可用）")
				continue
			}
			if closeFn != nil {
				closes = append(closes, closeFn)
			}
			views = append(views, repoView{acts, p})
			searched = append(searched, p)
		}
	}
	defer func() {
		for _, c := range closes {
			_ = c()
		}
	}()

	serviceSeen, implFound := false, false
	var available []string
	for _, v := range views {
		res, err := v.acts.GrpcRoutes(v.repo)
		if err != nil {
			continue
		}
		for _, s := range res.Services {
			if !strings.EqualFold(s.Name, svc) {
				continue
			}
			serviceSeen = true
			if s.ImplID == "" {
				continue // 只有调用侧占位节点（无实现）——不算实现仓库
			}
			implFound = true
			for _, m := range s.Methods {
				goName := GrpcHandlerGoMethod(m.Handler, s.Name)
				if goName == "" {
					goName = m.Name
				}
				available = append(available, m.Name)
				reason := ""
				switch {
				case strings.EqualFold(rpcMethod, m.Name):
					reason = "proto-name"
				case strings.EqualFold(rpcMethod, goName):
					reason = "go-method"
				}
				if reason == "" {
					continue
				}
				return &remoteTarget{acts: v.acts, repo: v.repo,
					methodID: GrpcMethodEntryID(s.ImplID, goName), reason: reason}, nil
			}
		}
	}
	if !implFound {
		detail := "没有任何仓库实现服务 " + svc + "（已查：" + strings.Join(searched, ", ") + "）"
		if serviceSeen {
			detail = "服务 " + svc + " 只有调用侧节点、无服务端实现（已查：" + strings.Join(searched, ", ") + "）"
		}
		return nil, &SeqStop{Code: ExtStopRepoMissing, Detail: detail}
	}
	detail := "服务 " + svc + " 无方法 " + rpcMethod
	if len(available) > 0 {
		detail += "（已实现：" + strings.Join(uniqueStrings(available), ", ") + "）"
	}
	return nil, &SeqStop{Code: ExtStopMethodNotFound, Detail: detail}
}

// uniqueStrings 去重（保持顺序）——诊断里的方法清单用。
func uniqueStrings(xs []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		if x == "" || seen[x] {
			continue
		}
		seen[x] = true
		out = append(out, x)
	}
	return out
}

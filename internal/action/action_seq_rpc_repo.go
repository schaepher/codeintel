package action

// todo22 §4.1：RPC 跨仓续接（候选仓库按显式范围提供；视图生命周期覆盖
// 全部子树使用期）。

import (
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// SeqRepoOpener 打开候选仓库的查询视图（与 ExtChainRepoOpener 同签名——
// cli 复用 workspace 候选范围；返回的 close 由本层负责调用）。
type SeqRepoOpener func(repoPath string) (*Actions, func() error, error)

// seqInitRPCState 初始化 §4.1 的请求级状态（映射/跨仓视图资源；重复调用幂等）。
// 提前调用=分析开始时创建，rpcRes 由外层 CodeSequence defer 关闭。
func seqInitRPCState(req *CodeSequenceRequest) {
	if req.rpcFwd == nil {
		req.rpcFwd = map[string]*domain.RPCForward{}
	}
	if req.rpcSvcs == nil {
		req.rpcSvcs = map[string]*seqRPCSvc{}
	}
	if req.rpcByFunc == nil {
		req.rpcByFunc = map[string][]*domain.RPCForward{}
	}
	if req.rpcVisited == nil {
		req.rpcVisited = map[string]bool{}
	}
	if req.rpcRes == nil {
		req.rpcRes = &seqRPCResources{}
	}
}

// seqRPCResources 请求级跨仓视图资源（defer 统一关闭；关闭顺序无关）。
type seqRPCResources struct {
	closes []func() error
	opened map[string]*Actions
	diags  []SeqDiagCount // 候选仓库打开/查询失败（不把"搜不全"当"没有"）
}

func (r *seqRPCResources) closeAll() {
	for _, fn := range r.closes {
		if fn != nil {
			_ = fn()
		}
	}
	r.closes = nil
}

// seqRPCRepoView 候选仓库视图（已打开则复用；失败缓存诊断）。
func (r *seqRPCResources) view(req CodeSequenceRequest, repoPath string) (*Actions, bool) {
	if r.opened == nil {
		r.opened = map[string]*Actions{}
	}
	if acts, hit := r.opened[repoPath]; hit {
		return acts, acts != nil
	}
	acts, closeFn, err := req.Open(repoPath)
	if err != nil || acts == nil {
		r.opened[repoPath] = nil
		detail := repoPath
		if err != nil {
			detail += "：" + err.Error()
		}
		r.diags = append(r.diags, SeqDiagCount{Code: SeqStopRPCQueryFailed, Count: 1,
			Detail: "RPC 候选仓库打开失败（" + detail + "）"})
		return nil, false
	}
	r.opened[repoPath] = acts
	r.closes = append(r.closes, closeFn)
	return acts, true
}

// seqRPCServiceAcrossRepos 在起始仓库 + 显式候选仓库里解析服务端。
// 纯查询（不展开子树）：返回目标视图/仓库/服务端解析结果；目标是候选仓库时
// target 记录，供调用方展开。
func seqRPCServiceAcrossRepos(a *Actions, req CodeSequenceRequest, fwd *domain.RPCForward) (*seqRPCSvc, string, *Actions) {
	handler := fwd.HandlerName
	if handler == "" {
		handler = fwd.ServiceName
	}
	key := fwd.FullIdentity + "|" + handler
	if req.rpcSvcs != nil {
		if s, hit := req.rpcSvcs[key]; hit {
			if s == nil || s.stop != nil || s.remoteRepo == "" {
				return s, s.remoteRepoOr(""), a
			}
			if req.rpcRes != nil {
				if acts, ok := req.rpcRes.view(req, s.remoteRepo); ok {
					return s, s.remoteRepo, acts
				}
			}
			return s, s.remoteRepo, nil
		}
	}
	local := seqMicroService(a, req, fwd)
	if local.stop == nil || local.stop.Code != SeqStopRPCRegistrationMissing {
		seqCacheRPCSvc(req, key, local)
		return local, "", a
	}
	// 起始仓库未找到注册：按显式候选范围逐个查（范围外仓库不参与）。没有
	// 候选范围（或没有 opener）时只保留起始仓库的未命中诊断。
	if len(req.CandidateRepos) == 0 || req.Open == nil || req.rpcRes == nil {
		seqCacheRPCSvc(req, key, local)
		return local, "", a
	}
	repos := append([]string(nil), req.CandidateRepos...)
	sort.Strings(repos)
	for _, repoPath := range repos {
		if repoPath == "" || repoPath == req.RepoAbs {
			continue
		}
		acts, ok := req.rpcRes.view(req, repoPath)
		if !ok {
			continue
		}
		another := req
		another.RepoAbs = repoPath
		another.rpcSvcs = map[string]*seqRPCSvc{} // 目标仓库自己的解析缓存
		another.rpcByFunc = map[string][]*domain.RPCForward{}
		another.fileCache = nil
		another.constPkgs = nil // 跨包常量表按仓库隔离
		another.diRegsAll = nil // 跨包 DI 注册表按仓库隔离
		another.sceneConsts = nil
		another.rpcRes = nil
		svc := seqMicroService(acts, another, fwd)
		if svc.stop != nil && svc.stop.Code == SeqStopRPCRegistrationMissing {
			continue
		}
		svc.remoteRepo = repoPath
		seqCacheRPCSvc(req, key, svc)
		return svc, repoPath, acts
	}
	seqCacheRPCSvc(req, key, local)
	return local, "", a
}

// remoteRepoOr 空安全。
func (s *seqRPCSvc) remoteRepoOr(fallback string) string {
	if s == nil || s.remoteRepo == "" {
		return fallback
	}
	return s.remoteRepo
}

// seqExpandRemoteRPC 在目标仓库视图里展开服务端方法（独立 CodeSequence 请求：
// 源码读取/包集合/文件缓存都以目标仓库为准），只取子节点拼回原树。
// 资源由请求级 seqRPCResources 在分析结束时统一关闭（覆盖子树使用期）。
func seqExpandRemoteRPC(target *Actions, req CodeSequenceRequest, node *CodeSeqNode,
	entry, remoteRepo string, depth int) bool {
	if target == nil {
		return false
	}
	childReq := req
	childReq.Target = entry
	childReq.RepoAbs = remoteRepo
	childReq.Depth = depth - 1
	childReq.rpcSvcs = nil
	childReq.rpcByFunc = nil
	childReq.rpcFwd = nil
	childReq.fileCache = nil
	childReq.tgtsCache = nil
	childReq.extPkgs = nil
	childReq.ifaceImpls = nil
	childReq.diRegs = nil
	// 复用请求级资源（视图在**外层**请求结束时关闭——覆盖全部子树使用期；
	// 嵌套 CodeSequence 不会重复 defer 关闭：rpcRes 非 nil）
	childReq.rpcRes = req.rpcRes
	childReq.focus = nil
	childReq.Focus = nil
	childReq.sceneConsts = nil
	childReq.pkgSet = nil
	childReq.Target = entry
	childReq.entryID = entry
	root, err := target.CodeSequence(childReq)
	if err != nil || root == nil {
		detail := "跨仓 RPC 目标展开失败（" + remoteRepo + "）"
		if err != nil {
			detail += "：" + err.Error()
		}
		node.Stop = &SeqStop{Code: SeqStopRPCQueryFailed, Detail: detail}
		return true
	}
	node.Nodes = root.Nodes
	if root.Stop != nil && node.Stop == nil {
		node.Stop = root.Stop
	}
	return true
}

// seqRPCScopeLabel 候选范围标签（证据/诊断用；空范围返回 repo-only）。
func seqRPCScopeLabel(req CodeSequenceRequest) string {
	if req.Scope != "" {
		return req.Scope
	}
	if len(req.CandidateRepos) == 0 {
		return "repo-only"
	}
	return "candidates:" + strings.Join(req.CandidateRepos, ",")
}

package action

// todo22 §4.1：RPC 服务端解析（服务身份 → 注册节点 → 实现类型）。
//
// 证明边界：
//   - 完整注册身份精确匹配优先（rpc_service 表达式索引）；身份不一致时
//     fail-closed（不按短名兜底续接到"另一个同名服务"）。
//   - 多实现/零实现都不猜：候选集合或明确诊断；命中与未命中都请求内缓存。

import (
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// seqRPCSvc 一次请求内缓存的服务端解析结果（命中/未命中都缓存）。
type seqRPCSvc struct {
	serviceID    string
	fullIdentity string
	handler      string
	impls        []string
	registerFile string
	registerLine int
	reason       string   // register-identity | register-handler-name
	methods      []string // 注册声明的方法（诊断用）
	remoteRepo   string   // 非空 = 服务端在候选仓库（跨仓续接目标）
	stop         *SeqStop // nil = 可续接；非 nil = 正常未命中原因
}

// seqRPCForwardOf 查调用目标是否是 RPC 客户端桩（grpc_call 出边，protocol=micro）。
// 结果按目标符号缓存在请求内（含"没有转发边"的否定结果）。
func seqRPCForwardOf(a *Actions, req CodeSequenceRequest, targetID string) (*domain.RPCForward, bool) {
	if req.rpcFwd != nil {
		if f, hit := req.rpcFwd[targetID]; hit {
			return f, f != nil
		}
	}
	if a.repo == nil {
		return nil, false
	}
	facts, err := a.repo.GetRPCForwards(domain.CanonicalID(targetID))
	req.acc.sqlQueries++
	if err != nil {
		return nil, false
	}
	var pick *domain.RPCForward
	for _, f := range facts {
		if f.Protocol == domain.RPCProtocolMicro {
			pick = f
			break
		}
	}
	if req.rpcFwd != nil {
		req.rpcFwd[targetID] = pick
	}
	return pick, pick != nil
}

// seqMicroService 解析 RPC 转发的服务端（服务节点 + 实现 + 注册站点）。
// 请求内缓存（key = 完整身份|短名），未命中同样缓存。
func seqMicroService(a *Actions, req CodeSequenceRequest, fwd *domain.RPCForward) *seqRPCSvc {
	handler := fwd.HandlerName
	if handler == "" {
		handler = fwd.ServiceName
	}
	key := fwd.FullIdentity + "|" + handler
	if req.rpcSvcs != nil {
		if s, hit := req.rpcSvcs[key]; hit {
			return s
		}
	}
	out := &seqRPCSvc{fullIdentity: fwd.FullIdentity, handler: handler}
	// 1) 完整身份精确匹配（rpc_service 表达式索引）
	if fwd.FullIdentity != "" {
		exact, err := a.repo.GetRPCServicesByFullIdentity(domain.RPCProtocolMicro, fwd.FullIdentity)
		req.acc.sqlQueries++
		if err != nil {
			out.stop = &SeqStop{Code: SeqStopRPCQueryFailed, Detail: "服务身份查询失败：" + err.Error()}
			seqCacheRPCSvc(req, key, out)
			return out
		}
		if len(exact) > 1 {
			out.stop = seqAmbiguousService("完整服务身份 " + fwd.FullIdentity + " 对应多个服务节点（注册重复）")
			seqCacheRPCSvc(req, key, out)
			return out
		}
		if len(exact) == 1 {
			out.serviceID, out.reason = string(exact[0].ID), "register-identity"
		}
	}
	// 2) 短名唯一匹配（完整身份缺失或注册侧没有完整身份时才允许——身份
	// 不一致必须 fail-closed，不能"按短名兜底续接"到别的实现）
	if out.serviceID == "" {
		all, err := a.repo.GetRPCServicesByProtocol(domain.RPCProtocolMicro)
		req.acc.sqlQueries++
		if err != nil {
			out.stop = &SeqStop{Code: SeqStopRPCQueryFailed, Detail: "服务注册查询失败：" + err.Error()}
			seqCacheRPCSvc(req, key, out)
			return out
		}
		var matches []*domain.CodeEntity
		for _, n := range all {
			if seqServiceHandlerName(n) != handler {
				continue
			}
			matches = append(matches, n)
		}
		// 身份冲突：注册身份与客户端声明不同（服务改名/配置不一致）
		if fwd.FullIdentity != "" {
			for _, m := range matches {
				if reg := m.Property("rpc_service"); reg != "" && reg != fwd.FullIdentity {
					out.stop = &SeqStop{Code: SeqStopRPCIdentityConflict,
						Detail: "客户端请求服务 " + fwd.FullIdentity + "，但注册身份是 " + reg +
							"（服务名不一致——不按短名兜底续接）"}
					seqCacheRPCSvc(req, key, out)
					return out
				}
			}
		}
		switch len(matches) {
		case 0:
			out.stop = &SeqStop{Code: SeqStopRPCRegistrationMissing,
				Detail: "未找到服务 " + seqServiceLabel(fwd, handler) + " 的 Register 注册（正常未命中：该服务可能不在本索引内）"}
			seqCacheRPCSvc(req, key, out)
			return out
		case 1:
			if matches[0].Property("rpc_service") == "" {
				// 只有客户端声明身份（生成客户端桩），没有服务端注册——按
				// "注册缺失"处理（跨仓续接可以继续到候选仓库找注册）
				out.stop = &SeqStop{Code: SeqStopRPCRegistrationMissing,
					Detail: "服务 " + seqServiceLabel(fwd, handler) + " 只有客户端声明身份，未找到服务端 Register 注册（正常未命中）"}
				seqCacheRPCSvc(req, key, out)
				return out
			}
			out.serviceID, out.reason = string(matches[0].ID), "register-handler-name"
		default:
			ids := make([]string, 0, len(matches))
			for _, m := range matches {
				ids = append(ids, string(m.ID))
			}
			sort.Strings(ids)
			out.stop = seqAmbiguousService("服务短名 " + handler + " 对应多个注册节点：" + strings.Join(ids, " | ") +
				"（缺少完整服务身份，不能串接到任一实现）")
			seqCacheRPCSvc(req, key, out)
			return out
		}
	}
	// 3) 注册声明的方法 + 注册站点（registers_service 属性 → 注册函数）
	if n, err := a.repo.GetSymbol(domain.CanonicalID(out.serviceID)); err == nil && n != nil {
		out.methods = splitSeqCSV(n.Property("methods"))
	}
	if f, line, ok := RegisterCallSite(a.repo, handler); ok {
		out.registerFile, out.registerLine = f, line
	} else if reg, err := a.repo.GetRegisterNode(handler); err == nil && reg != nil {
		out.registerFile, out.registerLine = reg.FilePath, reg.LineStart
	}
	// 4) 实现类型（grpc_impl 边）——多实现不猜
	impls, err := a.repo.GetGrpcImplNodes(domain.CanonicalID(out.serviceID))
	req.acc.sqlQueries++
	if err != nil {
		out.stop = &SeqStop{Code: SeqStopRPCQueryFailed, Detail: "服务实现查询失败：" + err.Error()}
		seqCacheRPCSvc(req, key, out)
		return out
	}
	for _, impl := range impls {
		out.impls = append(out.impls, string(impl.ID))
	}
	switch len(out.impls) {
	case 0:
		out.stop = &SeqStop{Code: SeqStopRPCImplMissing,
			Detail: "服务 " + seqServiceLabel(fwd, handler) + " 已注册，但注册实参无可证明实现类型（注册点未解析/实现未知）"}
	case 1:
	default:
		cands := append([]string(nil), out.impls...)
		sort.Strings(cands)
		stop := seqAmbiguousService("服务 " + seqServiceLabel(fwd, handler) + " 有多个注册实现：" + strings.Join(cands, " | "))
		stop.Code = SeqStopRPCAmbiguous
		out.stop = stop
	}
	seqCacheRPCSvc(req, key, out)
	return out
}

// seqAmbiguousService 服务身份/实现歧义诊断。
func seqAmbiguousService(detail string) *SeqStop {
	return &SeqStop{Code: SeqStopRPCAmbiguous, Detail: detail}
}

func seqCacheRPCSvc(req CodeSequenceRequest, key string, svc *seqRPCSvc) {
	if req.rpcSvcs != nil {
		req.rpcSvcs[key] = svc
	}
}

// seqServiceHandlerName 服务节点的短名（service_name；回退 handler_name）。
func seqServiceHandlerName(n *domain.CodeEntity) string {
	if n == nil {
		return ""
	}
	if v := n.Property("service_name"); v != "" {
		return v
	}
	return n.Property("handler_name")
}

// seqServiceLabel 诊断用服务标签（完整身份优先）。
func seqServiceLabel(fwd *domain.RPCForward, handler string) string {
	if fwd != nil && fwd.FullIdentity != "" {
		return fwd.FullIdentity + "（" + handler + "）"
	}
	return handler
}

// splitSeqCSV 逗号分隔属性 → 去空切片。
func splitSeqCSV(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

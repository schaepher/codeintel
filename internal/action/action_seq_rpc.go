package action

// todo22 §4.1：RPC 调用点续接服务端子树（go-micro 客户端桩 → Register 注册 →
// 实现方法）。native gRPC 的客户端调用点在 AST 层已直接建 grpc_call 边
// （source = 业务调用方），本文件只处理"生成客户端桩方法内部 Call"这种
// 转发形态（rpc_protocol=micro）——避免把桩的源码实现当成业务流程展开。
//
// 证明边界：
//   - 服务身份：NewRequest 第一个实参（完整身份）+ 端点前缀（handler 短名）；
//     完整身份缺失时按短名唯一匹配（证据 reason=handler-name，标记身份不完整）。
//   - 注册实参：grpc_impl 边（实现类型 → 服务节点）。多实现/无实现都不猜。
//   - 方法：实现类型上的同名方法（canonical ID 精确查询）。注册方法清单与
//     实现方法不一致时给出可核对诊断。
//   - 服务端解析（身份→注册→实现）在 action_seq_rpc_service.go。
//   - 未命中是**正常未命中**：缓存在请求内，不重复全库发现，也不报查询故障。

import (
	"go/ast"
	"go/token"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// seqRPCForwardAt 调用点（源函数 + pos/行号）的 RPC 出站转发。业务调用方
// 的 grpc_call 边 source 就是调用方函数（native gRPC 与 go-micro 接口调用
// 都如此）——按源函数一次性取全量出站边并缓存在请求内。
func seqRPCForwardAt(a *Actions, req CodeSequenceRequest, callerSym string,
	pos token.Position, method string) (*domain.RPCForward, bool, bool) {
	if callerSym == "" || a.repo == nil {
		return nil, false, false
	}
	if req.rpcByFunc == nil {
		return nil, false, false
	}
	fwds, hit := req.rpcByFunc[callerSym]
	if !hit {
		facts, err := a.repo.GetRPCForwards(domain.CanonicalID(callerSym))
		req.acc.sqlQueries++
		if err != nil {
			return nil, false, false
		}
		req.rpcByFunc[callerSym] = facts
		fwds = facts
	}
	for _, f := range fwds {
		if f.Pos != 0 && f.Pos == pos.Offset {
			return f, false, true
		}
	}
	// 旧索引/无 pos 的边：按行号回退（同函数同一行多 RPC 时取第一个，
	// 与调用点映射的既有回退口径一致）
	for _, f := range fwds {
		if f.Line != 0 && f.Line == pos.Line {
			return f, false, true
		}
	}
	// 同义边合并（UNIQUE(source,target,kind,edge_key)）：同一函数重复调用同一
	// 服务方法只留首个位置的 metadata——按 RPC 方法名唯一匹配回退，并标记为
	// 汇总命中（证据 corroborated，不是该位置的直接 metadata）。
	if method == "" {
		return nil, false, false
	}
	var matched []*domain.RPCForward
	for _, f := range fwds {
		if f.Method == method {
			matched = append(matched, f)
		}
	}
	if len(matched) != 1 {
		return nil, false, false
	}
	return matched[0], true, true
}

// seqExpandRPCAt 调用点（源函数 + 位置/方法名）的 RPC 续接（position 直命中
// 优先；重复调用的同义边按方法名唯一回退）。
func seqExpandRPCAt(a *Actions, req CodeSequenceRequest, node *CodeSeqNode, callerSym string,
	pos token.Position, method string, depth int, curFile string) bool {
	fwd, aggregated, ok := seqRPCForwardAt(a, req, callerSym, pos, method)
	if !ok {
		return false
	}
	handled := seqExpandRPCForward(a, req, node, fwd, depth, curFile, pos.Line)
	if handled && aggregated && node.Evidence != nil {
		node.Evidence.Reason += "-aggregated"
		node.Evidence.Verdict = SeqVerdictCorroborated
	}
	return handled
}

// seqCallMethodName 调用表达式的静态方法名（selector/ident；其余空）。
func seqCallMethodName(fun ast.Expr) string {
	switch f := fun.(type) {
	case *ast.SelectorExpr:
		return f.Sel.Name
	case *ast.Ident:
		return f.Name
	}
	return ""
}

// seqExpandRPC 若 targetID 是 go-micro 客户端桩，则在原调用点续接服务端子树。
// 返回 true 表示已处理（无论成功/明确未命中），调用方不得再展开桩源码。
func seqExpandRPC(a *Actions, req CodeSequenceRequest, node *CodeSeqNode, targetID string,
	depth int, curFile string, line int) bool {
	// RPC 客户端桩一定是方法（NewRequest/Call 在方法体内）——包级函数不做
	// 出站边查询（每次调用点一条 SQL 的成本不值得）。
	if !strings.Contains(targetID, ":(") {
		return false
	}
	fwd, ok := seqRPCForwardOf(a, req, targetID)
	if !ok {
		return false
	}
	return seqExpandRPCForward(a, req, node, fwd, depth, curFile, line)
}

// seqExpandRPCForward 按已解析的 RPC 转发续接服务端子树（协议限定 go-micro：
// native gRPC 的时序展开路径保持既有行为，不在这里改变）。
func seqExpandRPCForward(a *Actions, req CodeSequenceRequest, node *CodeSeqNode,
	fwd *domain.RPCForward, depth int, curFile string, line int) bool {
	if fwd == nil || fwd.Protocol != domain.RPCProtocolMicro {
		return false
	}
	handler := fwd.HandlerName
	if handler == "" {
		handler = fwd.ServiceName
	}
	base := &SeqEvidence{Source: SeqEvidenceRPC, CallFile: curFile, CallLine: line,
		Reason: "micro-client-stub", ServiceID: string(fwd.ServiceID),
		RPCService: fwd.FullIdentity, RPCProtocol: fwd.Protocol, RPCMethod: fwd.Method}
	// todo23 §4.1：先解析并证明服务实现方法，**再**应用展开深度——
	// depth=1 也必须输出与深查询相同的方法 canonical ID（不能用服务节点 ID
	// 代替方法前沿）；缺注册/多实现保持未知。
	svc, remoteRepo, targetActs := seqRPCServiceAcrossRepos(a, req, fwd)
	if svc == nil || svc.stop != nil {
		node.Evidence = base
		if svc != nil {
			node.Stop = svc.stop
		}
		return true
	}
	// 实现方法：canonical ID 精确构造（不做模糊名称搜索）
	entry := GrpcMethodEntryID(svc.impls[0], fwd.Method)
	if entry == "" {
		node.Evidence = base
		node.Stop = &SeqStop{Code: SeqStopRPCMethodMissing,
			Detail: "无法从实现类型构造方法入口：" + svc.impls[0] + " + " + fwd.Method}
		return true
	}
	lookup := a.repo
	if remoteRepo != "" && targetActs != nil {
		lookup = targetActs.repo
	}
	if _, err := lookup.GetSymbol(domain.CanonicalID(entry)); err != nil {
		req.acc.sqlQueries++
		node.Evidence = base
		detail := "服务 " + seqServiceLabel(fwd, handler) + " 的实现类型 " + svc.impls[0] + " 没有方法 " + fwd.Method
		if len(svc.methods) > 0 {
			detail += "（注册声明的方法：" + strings.Join(svc.methods, ", ") + "）"
		}
		node.Stop = &SeqStop{Code: SeqStopRPCMethodMissing, Detail: detail}
		return true
	}
	req.acc.symLookups++
	base.SymbolID = entry
	base.RegisterFile, base.RegisterSite = svc.registerFile, svc.registerLine
	base.RemoteRepo = remoteRepo
	if remoteRepo != "" {
		base.ServiceID = svc.serviceID
	}
	base.Confidence = 1.0
	base.Reason = "micro-" + svc.reason
	node.Evidence = base
	node.ImplType = implTypeShort(svc.impls[0])
	if depth <= 1 {
		// 身份已证明，仅不展开子树（深度是展开预算，不是身份解析预算）
		req.acc.depth = true
		base.File, base.Line = "", 0
		node.Stop = &SeqStop{Code: SeqStopDepthLimit,
			Detail: "depth 上限（--depth " + itoaSeq(req.Depth) + "），未展开 RPC 服务端子树"}
		// todo24 §2：RPC 前沿续查（服务端子树按原请求 depth 展开；跨仓由
		// seqExpandRPCForward 自己切换仓库上下文）。
		seqRegisterFrontier(req, node, entry, func(sub CodeSequenceRequest) {
			seqExpandRPCForward(a, sub, node, fwd, sub.Depth+1, curFile, line)
		}, node.Stop.Detail)
		return true
	}
	// 防环：跨仓库也按 (仓库|方法入口) 计——同仓时与既有 visited 同口径
	if req.rpcVisited != nil {
		key := req.RepoAbs + "|" + entry
		if req.rpcVisited[key] {
			node.Stop = &SeqStop{Code: SeqStopCycle, Detail: "RPC 服务方法重复出现（" + entry + "）"}
			return true
		}
		req.rpcVisited[key] = true
		defer delete(req.rpcVisited, key)
	}
	// 服务端方法展开：不再做 target 级 RPC 转发检查（这里已知目标是服务实现；
	// 多一次出站边查询是纯开销），但循环/depth 语义与统一入口保持一致。
	// 跨仓目标用独立 CodeSequence（目标仓库的源码/包集合/文件缓存）。
	if remoteRepo != "" {
		seqExpandRemoteRPC(targetActs, req, node, entry, remoteRepo, depth)
		return true
	}
	if child, stop := codeSeqForSymbol(a, req, entry, depth-1); child != nil {
		node.Nodes, node.Stop = child.Nodes, child.Stop
	} else if stop != nil {
		node.Stop = stop
	}
	return true
}

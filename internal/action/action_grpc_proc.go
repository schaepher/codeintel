package action

// R92 迁移：gRPC 服务方法入口数据（原 cli/wiki_processes_routes.go
// 的 grpcProcMethods/grpcMethodEntryID/grpcHandlerGoMethod）——wiki/
// query processes 渲染用的数据函数；渲染函数留 cli。

import (
	"strings"

	"go.uber.org/zap"
)

// GrpcMethodProc 一个 gRPC 服务方法入口（(Impl).Method 展开）。
type GrpcMethodProc struct {
	Name    string
	Handler string // ServiceDesc handler（生成代码包装）
	Chain   *ProcChain
}

// GrpcMethodEntryID 方法入口 canonical ID：ImplID（symbol:go:<pkg>:<Type>）
// → symbol:go:<pkg>:(Type).Method（canonicalizer 统一 (T).m 形态）。
func GrpcMethodEntryID(implID, method string) string {
	i := strings.LastIndex(implID, ":")
	if i < 0 {
		return ""
	}
	return implID[:i+1] + "(" + implID[i+1:] + ")." + method
}

// GrpcProcMethods 服务方法 → 调用链：ImplID 构造 (Impl).Method。
// 方法未索引/实现缺失 → Chain nil（渲染时说明，不崩溃）。
// R55：ServiceDesc 方法名是 proto 定义名（go2o 实测小写 sendCode/forbid/
// getPage），实现方法是 Go 导出名（SendCode/Forbid/GetPage）——用 handler
// 提取 Go 方法名构造入口 ID，否则 19 个方法索引中无符号显示无调用链。
func (a *Actions) GrpcProcMethods(svc GrpcRouteService) []GrpcMethodProc {
	logger := zap.L()
	logger.Info("enter (Actions).GrpcProcMethods", zap.String("service", svc.Name))
	defer logger.Info("exit (Actions).GrpcProcMethods")
	out := make([]GrpcMethodProc, 0, len(svc.Methods))
	for _, m := range svc.Methods {
		p := GrpcMethodProc{Name: m.Name, Handler: m.Handler}
		if svc.ImplID != "" && m.Name != "" {
			entry := m.Name
			goName := GrpcHandlerGoMethod(m.Handler, svc.Name)
			if goName != "" {
				p.Name = goName // 展示与调用链入口一致（Go 导出名）
				entry = goName
			}
			p.Chain = a.QueryChain(GrpcMethodEntryID(svc.ImplID, entry))
			// R55：ServiceDesc 声明了方法（handler 存在）但索引无此符号——
			// 真无效方法（go2o SaveAreaTemplate/FlushCache：实现嵌
			// Unimplemented 桩）——文案区别于"可能未重建索引"。
			// 仅覆盖 ResolveSymbol 失败（索引中无此符号）；"未调用项目内
			// 函数"（Ping/Hello 健康检查）保留诚实文案不覆盖。
			if p.Chain != nil && strings.Contains(p.Chain.Miss, "索引中无此符号") && goName != "" {
				p.Chain.Miss = "ServiceDesc 声明但实现类型无此方法（未实现——无效 gRPC 方法；或未重建索引）"
			}
		}
		out = append(out, p)
	}
	return out
}

// GrpcHandlerGoMethod handler 名提取 Go 方法名（R55）：生成代码 handler
// 格式 `_<Service>_<GoMethod>_Handler`——ServiceDesc 方法名是 proto 定义
// 名（小写 sendCode），Go 导出名在 handler 里（SendCode）。前缀/后缀不
// 匹配（手写 handler、服务名不符）返回 ""。
func GrpcHandlerGoMethod(handler, svcName string) string {
	prefix := "_" + svcName + "_"
	const sfx = "_Handler"
	if !strings.HasPrefix(handler, prefix) || !strings.HasSuffix(handler, sfx) {
		return ""
	}
	return strings.TrimSuffix(strings.TrimPrefix(handler, prefix), sfx)
}

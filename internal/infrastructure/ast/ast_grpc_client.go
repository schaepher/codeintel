package ast

// todo22 §18/§21：gRPC/外部客户端调用的识别（接收者类型 → 服务名 →
// grpc_call 边）。从 ast_grpc.go 拆出（行数治理）；服务注册/ServiceDesc
// 仍在 ast_grpc.go。

import (
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
	"go/ast"
	"go/types"
)

// grpcClientServiceOf 方法接收者类型 → gRPC 服务名（todo12-16）：
// 接收者 *pb.OrderServiceClient 且方法签名含 context.Context + error。
// 不依赖 NewXxxClient 构造器/变量追踪，覆盖 DI、结构体字段、外部生成代码。
//
// todo22 §4.1：go-micro 客户端接口的方法签名与 gRPC 同形（ctx + error），
// 但可选项是 `...client.CallOption`（go-micro 包类型）——按可选项类型排除，
// 否则同一调用会被同时标成 native gRPC（协议校验失败）。
func grpcClientServiceOf(callee *types.Func) (string, bool) {
	if callee == nil {
		return "", false
	}
	sig, ok := callee.Type().(*types.Signature)
	if !ok || sig.Recv() == nil || !isGrpcClientCallSig(sig) {
		return "", false
	}
	t := sig.Recv().Type()
	if p, okp := t.(*types.Pointer); okp {
		t = p.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok || named.Obj() == nil {
		return "", false
	}
	return clientTypeService(named.Obj().Name())
}

// isGrpcClientCallSig gRPC 客户端方法签名：首参 context.Context + 末返回 error。
func isGrpcClientCallSig(sig *types.Signature) bool {
	if sig == nil || sig.Params().Len() < 2 || sig.Results().Len() < 1 {
		return false
	}
	first := sig.Params().At(0).Type()
	if !strings.HasSuffix(types.TypeString(first, nil), "context.Context") {
		return false
	}
	last := sig.Results().At(sig.Results().Len() - 1).Type()
	return last.String() == "error"
}

// emitGrpcClientCall 发射客户端调用：svc 节点 + grpc_call 边（关系身份=方法名）。
func (ctx *fileCtx) emitGrpcClientCall(call *ast.CallExpr, callee *types.Func,
	method string, callerID domain.CanonicalID, svc string) {
	pkg := ctx.pkg
	// todo22 §4.1：go-micro 客户端接口的方法可选项是 `...client.CallOption`
	// （go-micro 包类型）——协议按调用点实际签名判定，不按服务名/包名猜。
	protocol := domain.RPCProtocolGrpc
	if sig, ok := callee.Type().(*types.Signature); ok && isMicroClientOptsSig(sig) {
		protocol = domain.RPCProtocolMicro
	}
	svcID := domain.CanonicalID("symbol:go:" + callee.Pkg().Path() + ":svc." + svc)
	props := map[string]any{"service_name": svc}
	if protocol == domain.RPCProtocolMicro {
		// 协议由调用点签名证明；完整客户端身份（NewRequest 第一参）只在
		// 生成客户端桩里可见，不在这里把短名冒充完整身份。
		props["rpc_protocol"] = protocol
	}
	_ = ctx.emit(domain.Item{Node: &domain.CodeEntity{
		ID:         svcID,
		Kind:       domain.KindGrpcService,
		Name:       "svc." + svc,
		Properties: props,
	}})
	reqType := ""
	if len(call.Args) > 1 {
		reqType = typePath(pkg.TypesInfo.TypeOf(call.Args[1]))
	}
	_ = ctx.emit(domain.Item{Fact: &domain.Fact{
		SourceID:   callerID,
		TargetID:   svcID,
		Kind:       domain.FactGrpcCall,
		ToolSource: domain.ToolCodeGraph,
		Confidence: 1.0,
		Key:        method,
		Metadata: map[string]any{
			"method":       method,
			"req_type":     reqType,
			"rpc_protocol": protocol,
			"line_num":     pkg.Fset.PositionFor(call.Pos(), false).Line,
			"pos":          pkg.Fset.PositionFor(call.Lparen, false).Offset,
		},
	}})
}

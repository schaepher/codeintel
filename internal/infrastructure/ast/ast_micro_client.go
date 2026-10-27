package ast

// todo22 §4.1：go-micro **客户端**识别（NewRequest/Call → RPC 身份 + 服务节点）
// 与变量绑定（micro.NewService/micro.Name、请求变量）。服务端注册在
// ast_micro.go（协议/类型判定与注册发射）。

import (
	"go/ast"
	"go/token"
	"go/types"

	"github.com/schaepher/codeintel/internal/canonicalizer"
	"github.com/schaepher/codeintel/internal/domain"
)

func (ctx *fileCtx) microRPCFromNewRequest(call *ast.CallExpr, callee *types.Func) (microRPC, bool) {
	if callee == nil || callee.Pkg() == nil || !isMicroClientPkg(callee.Pkg().Path()) ||
		callee.Name() != "NewRequest" || len(call.Args) < 2 {
		return microRPC{}, false
	}
	svc := microStringLiteral(ctx.pkg, call.Args[0])
	handler, method := splitMicroEndpoint(microStringLiteral(ctx.pkg, call.Args[1]))
	if handler == "" || method == "" {
		return microRPC{}, false
	}
	return microRPC{Service: svc, Handler: handler, Method: method,
		Line: ctx.pkg.Fset.PositionFor(call.Lparen, false).Line}, true
}

// microRPCFromExpr 解析 Call 的请求实参：变量（同函数内 NewRequest 赋值）
// 或内联 NewRequest 调用。
func (ctx *fileCtx) microRPCFromExpr(callerID string, expr ast.Expr) (microRPC, bool) {
	if id, ok := expr.(*ast.Ident); ok {
		rpc, hit := ctx.microReqs[callerID+"\x00"+id.Name]
		return rpc, hit
	}
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return microRPC{}, false
	}
	callee, ok := resolveCallee(ctx.pkg.TypesInfo, call.Fun)
	if !ok {
		return microRPC{}, false
	}
	return ctx.microRPCFromNewRequest(call, callee)
}

// microServiceNameOf 服务完整身份：`svc.Server()` / `micro.NewService(micro.Name("x"))`。
// 解析不到返回空串（身份未知，不猜）。
func (ctx *fileCtx) microServiceNameOf(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return ctx.microSvcs[v.Name]
	case *ast.CallExpr:
		if sel, ok := v.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Server" {
			return ctx.microServiceNameOf(sel.X)
		}
		if callee, ok := resolveCallee(ctx.pkg.TypesInfo, v.Fun); ok && callee.Pkg() != nil &&
			isMicroRootPkg(callee.Pkg().Path()) && callee.Name() == "NewService" {
			return ctx.microNameFromOpts(v.Args)
		}
	}
	return ""
}

// microNameFromOpts 从 NewService 的可选参数里找 `micro.Name("literal")`。
func (ctx *fileCtx) microNameFromOpts(args []ast.Expr) string {
	for _, a := range args {
		call, ok := a.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			continue
		}
		callee, ok := resolveCallee(ctx.pkg.TypesInfo, call.Fun)
		if !ok || callee.Pkg() == nil || !isMicroRootPkg(callee.Pkg().Path()) || callee.Name() != "Name" {
			continue
		}
		return microStringLiteral(ctx.pkg, call.Args[0])
	}
	return ""
}

// trackMicroBindings go-micro 变量绑定：
//   - `svc := micro.NewService(micro.Name("example.flow.AccountService"))`
//   - `req := c.c.NewRequest("example.flow.AccountService", "AccountService.Apply", in)`
//
// 两者都是"调用点的准确身份"来源——只记录能精确解析的字符串。
func (ctx *fileCtx) trackMicroBindings(assign *ast.AssignStmt) {
	if assign.Tok != token.DEFINE && assign.Tok != token.ASSIGN {
		return
	}
	for i, rhs := range assign.Rhs {
		if i >= len(assign.Lhs) {
			break
		}
		id, ok := assign.Lhs[i].(*ast.Ident)
		if !ok {
			continue
		}
		call, ok := rhs.(*ast.CallExpr)
		if !ok {
			continue
		}
		callee, ok := resolveCallee(ctx.pkg.TypesInfo, call.Fun)
		if !ok || callee.Pkg() == nil {
			continue
		}
		if isMicroRootPkg(callee.Pkg().Path()) && callee.Name() == "NewService" {
			if name := ctx.microNameFromOpts(call.Args); name != "" {
				ctx.microSvcs[id.Name] = name
			}
			continue
		}
		if rpc, okRPC := ctx.microRPCFromNewRequest(call, callee); okRPC {
			ctx.microReqs[ctx.currentCallerKey()+"\x00"+id.Name] = rpc
		}
	}
}

// currentCallerKey 当前所在函数的 canonical ID（变量作用域隔离用）。
func (ctx *fileCtx) currentCallerKey() string {
	d := findCallerDecl(ctx.stack)
	if d == nil {
		return ""
	}
	fn, ok := ctx.pkg.TypesInfo.Defs[d.Name].(*types.Func)
	if !ok {
		return ""
	}
	id, _ := ctx.a.fnIDOf(fn)
	return string(id)
}

// emitMicroCall 在调用表达式处识别 go-micro 的服务注册与客户端 RPC。

func (ctx *fileCtx) emitMicroClientCall(call *ast.CallExpr, callee *types.Func,
	callerID domain.CanonicalID) bool {
	if callee == nil || callee.Pkg() == nil || !isMicroClientPkg(callee.Pkg().Path()) ||
		callee.Name() != "Call" || len(call.Args) < 2 || callerID == "" {
		return false
	}
	rpc, ok := ctx.microRPCFromExpr(string(callerID), call.Args[1])
	if !ok {
		return false
	}
	svcID := canonicalizer.GoSymbolID(ctx.pkg.PkgPath, "svc."+rpc.Handler)
	props := map[string]any{
		"service_name": rpc.Handler,
		"handler_name": rpc.Handler,
		"rpc_protocol": domain.RPCProtocolMicro,
	}
	if rpc.Service != "" {
		// 客户端**声明**的身份（不覆盖服务端注册身份 rpc_service——两者不同
		// 说明服务改名/配置不一致，查询侧必须 fail-closed）
		props["rpc_client_service"] = rpc.Service
	}
	_ = ctx.emit(domain.Item{Node: &domain.CodeEntity{
		ID: svcID, Kind: domain.KindGrpcService, Name: "svc." + rpc.Handler, Properties: props,
	}})
	pos := ctx.pkg.Fset.PositionFor(call.Lparen, false)
	_ = ctx.emit(domain.Item{Fact: &domain.Fact{
		SourceID: callerID, TargetID: svcID, Kind: domain.FactGrpcCall,
		ToolSource: domain.ToolCodeGraph, Confidence: 1.0, Key: rpc.Method,
		Metadata: map[string]any{
			"method":       rpc.Method,
			"rpc_service":  rpc.Service,
			"rpc_protocol": domain.RPCProtocolMicro,
			"line_num":     pos.Line,
			"pos":          ctx.pkg.Fset.PositionFor(call.Lparen, false).Offset,
		},
	}})
	return true
}

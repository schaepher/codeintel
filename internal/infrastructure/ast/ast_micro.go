package ast

// todo22 §4.1：go-micro 服务协议识别（客户端 NewRequest/Call → 服务身份 +
// 服务端 Register 注册 → 实现类型）。
//
// 识别不依赖"服务名长什么样"（非标准服务名不能被优化过滤），只依赖：
//   - 客户端：调用的是 go-micro client 包的 `NewRequest` / `Call`；
//     NewRequest 前两参是**可精确解析的字符串**（字面量/常量——拼接出的
//     动态服务名保持未知，不猜）。
//   - 服务端：注册函数第一参是 go-micro server.Server；生成代码形态
//     `Register<X>Handler(s, hdlr XHandler, ...)` 从第二参接口名取服务短名，
//     实现类型取注册实参（`new(AccountServer)` / `&AccountServer{}`）。
//   - 完整服务身份来自 `micro.NewService(micro.Name("..."))` 的局部变量绑定；
//     拿不到就留空（只按短名匹配，并在诊断里说明身份不完整）。
//
// native gRPC 的识别路径（ServiceDesc/RegisterXxxServer）不改——本文件只
// 新增 go-micro 分支（`grpc_service` 节点带 rpc_protocol=micro，grpc_impl
// 边/grpc_call 边沿用既有语义）。

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"strconv"
	"strings"

	"github.com/schaepher/codeintel/internal/canonicalizer"
	"github.com/schaepher/codeintel/internal/domain"
	"golang.org/x/tools/go/packages"
)

// microRPC go-micro 一次客户端 RPC 调用的静态身份。
type microRPC struct {
	Service string // 完整服务身份（NewRequest 第 1 参，可能为空=未知）
	Handler string // 端点字符串前缀（"AccountService.Apply" → AccountService）
	Method  string // RPC 方法名（"AccountService.Apply" → Apply）
	Line    int
}

// isMicroPath 导入路径属于 go-micro 家族（覆盖官方 go-micro.dev/vN 与
// github.com/micro|asim/go-micro 的历史路径）。
func isMicroPath(p string) bool {
	if p == "" {
		return false
	}
	if p == "go-micro.dev" || strings.HasPrefix(p, "go-micro.dev/") {
		return true
	}
	return strings.Contains(p, "github.com/micro/go-micro") ||
		strings.Contains(p, "github.com/asim/go-micro")
}

// isMicroRootPkg 根包（micro.NewService / micro.Name / micro.RegisterHandler）。
func isMicroRootPkg(p string) bool {
	if !isMicroPath(p) {
		return false
	}
	if p == "go-micro.dev" {
		return true
	}
	for _, suffix := range []string{"/v2", "/v3", "/v4", "/v5", "/v6"} {
		if strings.HasSuffix(p, suffix) {
			return true
		}
	}
	return strings.HasSuffix(p, "go-micro") || strings.HasSuffix(p, "go-micro/v1")
}

// isMicroClientPkg go-micro 客户端包。
func isMicroClientPkg(p string) bool { return isMicroPath(p) && strings.HasSuffix(p, "/client") }

// isMicroServerPkg go-micro 服务端包。
func isMicroServerPkg(p string) bool { return isMicroPath(p) && strings.HasSuffix(p, "/server") }

// isMicroServerType 类型是 go-micro server.Server（接口或实现指针）。
func isMicroServerType(t types.Type) bool {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok || named.Obj() == nil || named.Obj().Pkg() == nil {
		return false
	}
	return isMicroServerPkg(named.Obj().Pkg().Path()) && named.Obj().Name() == "Server"
}

// microStringLiteral 精确字符串（字面量/字符串常量）——**不做**动态拼接的
// 部分解析：服务身份不完整时保持未知，不用前缀冒充完整身份。
func microStringLiteral(pkg *packages.Package, e ast.Expr) string {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind == token.STRING {
			if s, err := strconv.Unquote(v.Value); err == nil {
				return s
			}
		}
	case *ast.Ident:
		if obj := pkg.TypesInfo.ObjectOf(v); obj != nil {
			if c, ok := obj.(*types.Const); ok && c.Val() != nil && c.Val().Kind() == constant.String {
				return constant.StringVal(c.Val())
			}
		}
	}
	return ""
}

// isMicroClientOptsSig 方法可选项是 go-micro client 包类型（`...client.CallOption`）。
func isMicroClientOptsSig(sig *types.Signature) bool {
	if sig == nil || !sig.Variadic() || sig.Params().Len() == 0 {
		return false
	}
	t := sig.Params().At(sig.Params().Len() - 1).Type()
	if sl, ok := t.(*types.Slice); ok {
		t = sl.Elem()
	}
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok || named.Obj() == nil || named.Obj().Pkg() == nil {
		return false
	}
	return isMicroPath(named.Obj().Pkg().Path())
}

// splitMicroEndpoint "AccountService.Apply" → (AccountService, Apply)。
// 端点必须至少两段且都非空（gRPC 风格 "/pkg.Svc/M" 不属于 go-micro 形态）。
func splitMicroEndpoint(ep string) (handler, method string) {
	i := strings.LastIndex(ep, ".")
	if i <= 0 || i >= len(ep)-1 {
		return "", ""
	}
	handler, method = ep[:i], ep[i+1:]
	if strings.ContainsAny(handler, "/ ") || strings.ContainsAny(method, "/ ") {
		return "", ""
	}
	return handler, method
}

// emitMicroCall 在调用表达式处识别 go-micro 的服务注册与客户端 RPC
// （注册在服务端包，客户端桩在生成代码里——两者都可能出现在本模块）。
func (ctx *fileCtx) emitMicroCall(call *ast.CallExpr, callee *types.Func,
	callerID domain.CanonicalID, callerKind domain.EntityKind) {
	if callee == nil || callee.Pkg() == nil {
		return
	}
	ctx.emitMicroRegistration(call, callee, callerID, callerKind)
	ctx.emitMicroClientCall(call, callee, callerID)
}

// microRegisterShape 注册调用形态：服务短名（handler 名）+ 方法全集。

// microHandlerIface 注册第二参（handler 接口）→ 服务短名 + 方法名。
// 接口名 `<X>Handler` → X；非接口（`any`/具体类型）返回空（不猜）。

// microConcreteHandler 注册实参是**具体 handler 类型**（`new(AccountServer)`）：
// go-micro 的 handler 名 = 反射得到的**类型名本身**（endpoint 前缀
// `AccountServer.Apply`），因此服务短名取类型名，不做后缀裁剪；方法取该
// 类型的指针方法集（导出方法）。类型名不可识别 → 不猜。

// typePkgPath 具名类型的包路径（指针自动解引用；非具名返回空）。

// typeCanonicalID 具名类型 → (canonical ID, 短名)；非具名（接口字面量/
// 内建）返回 false。

// emitMicroRegistration 服务端注册调用 → grpc_service 节点（rpc_protocol=micro）
// + grpc_impl 边（实现 → 服务）+ 注册函数的 registers_service 属性。
func (ctx *fileCtx) emitMicroRegistration(call *ast.CallExpr, callee *types.Func,
	callerID domain.CanonicalID, callerKind domain.EntityKind) bool {
	sig, ok := callee.Type().(*types.Signature)
	if !ok || sig.Params().Len() < 2 || len(call.Args) < 2 {
		return false
	}
	if !isMicroServerType(sig.Params().At(0).Type()) {
		return false
	}
	argType := ctx.pkg.TypesInfo.TypeOf(call.Args[1])
	// todo25 §4.1：注册实参经过类型断言/DI 工厂取值时，静态类型是接口；
	// 具体实现类型取断言/取值的**来源表达式**类型（仍要求具名具体类型，
	// 动态 any 不猜）。handler 短名仍按注册实参的静态接口类型推导。
	implArgType := microConcreteArgType(ctx.pkg, call.Args[1], argType)
	handler, methods, ok := microRegisterShape(callee, sig, argType)
	if !ok {
		return false
	}
	full := ctx.microServiceNameOf(call.Args[0])
	svcPkg := callee.Pkg().Path()
	// 根包注册（形参 any）没有 pb 包上下文：用实现类型所在包做服务节点身份，
	// 避免不同包的同名 handler 在 go-micro 根包下碰撞。
	if isMicroRootPkg(svcPkg) {
		if p := typePkgPath(implArgType); p != "" {
			svcPkg = p
		}
	}
	svcID := canonicalizer.GoSymbolID(svcPkg, "svc."+handler)
	props := map[string]any{
		"service_name": handler,
		"handler_name": handler,
		"rpc_protocol": domain.RPCProtocolMicro,
	}
	if full != "" {
		props["rpc_service"] = full
	}
	if len(methods) > 0 {
		props["methods"] = strings.Join(methods, ",")
	}
	pos := ctx.pkg.Fset.PositionFor(call.Lparen, false)
	_ = ctx.emit(domain.Item{Node: &domain.CodeEntity{
		ID: svcID, Kind: domain.KindGrpcService, Name: "svc." + handler,
		FilePath: relPath(ctx.repo.Path, pos.Filename), LineStart: pos.Line, LineEnd: pos.Line,
		Properties: props,
	}})
	if implID, implName, okImpl := typeCanonicalID(implArgType); okImpl {
		implPos := ctx.pkg.Fset.PositionFor(call.Args[1].Pos(), false)
		_ = ctx.emit(domain.Item{Node: &domain.CodeEntity{
			ID: implID, Kind: domain.KindStruct, Name: implName,
			FilePath: relPath(ctx.repo.Path, implPos.Filename), LineStart: implPos.Line, LineEnd: implPos.Line,
		}})
		_ = ctx.emit(domain.Item{Fact: &domain.Fact{
			SourceID: implID, TargetID: svcID, Kind: domain.FactGrpcImpl,
			ToolSource: domain.ToolCodeGraph, Confidence: 1.0,
		}})
	}
	// 注册函数节点打 registers_service 属性（与 native gRPC 的
	// emitGrpcServiceEntry 同口径：属性打在**注册函数**上，不是调用方——
	// 同一个 Setup 注册多个服务时，调用方的单属性值会互相覆盖）。
	// registers_rpc_service 保留 go-micro 完整身份；调用点（Setup 里的注册
	// 行）由 GetFirstCallTo（calls 入边 + 行号）恢复。
	_ = callerID
	_ = callerKind
	if cid, ckind := ctx.a.fnIDOf(callee); cid != "" {
		regProps := map[string]any{"registers_service": handler}
		if full != "" {
			regProps["registers_rpc_service"] = full
		}
		if ckind == "" {
			ckind = domain.KindFunction
		}
		calleePos := ctx.pkg.Fset.PositionFor(callee.Pos(), false)
		_ = ctx.emit(domain.Item{Node: &domain.CodeEntity{
			ID: cid, Kind: ckind, Name: callee.Name(),
			FilePath:  relPath(ctx.repo.Path, calleePos.Filename),
			LineStart: calleePos.Line, LineEnd: calleePos.Line, Properties: regProps,
		}})
	}
	return true
}

// microConcreteArgType 注册实参的具体类型：类型断言 `expr.(I)` 取 expr 的
// 静态类型（`container.Get().(pb.XHandler)` → 工厂返回的具体类型）；其它
// 表达式用自身静态类型。接口值/any 仍保持非具名（调用方不猜）。
func microConcreteArgType(pkg *packages.Package, arg ast.Expr, static types.Type) types.Type {
	for {
		switch e := arg.(type) {
		case *ast.ParenExpr:
			arg = e.X
			continue
		case *ast.TypeAssertExpr:
			if t := pkg.TypesInfo.TypeOf(e.X); t != nil {
				return t
			}
		}
		return static
	}
}

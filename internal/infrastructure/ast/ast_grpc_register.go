package ast

import (
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
	"golang.org/x/tools/go/packages"
)

// markGrpcServiceInterfaces 通过接口方法签名识别 gRPC 服务接口（R30-2，
// 用户要求"类型是否实现了 grpc 的方法"——方法参数/返回值固定模式：
// 每个方法末返回值是 error，且首参是 context.Context 或参数/返回值含
// google.golang.org/grpc 类型（流式）；不依赖注册点/函数名/文件）。
// 发射 grpc_service 节点（service_name + methods 属性——手写服务无
// ServiceDesc 时的方法数据源；与注册调用点发射的节点按 ID UPSERT 合并）。
func (a *Adapter) markGrpcServiceInterfaces(repo *domain.Repository, pkg *packages.Package, emit domain.EmitFunc) error {
	for _, f := range pkg.Syntax {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				obj := pkg.TypesInfo.Defs[ts.Name]
				if obj == nil {
					continue
				}
				named, ok := obj.Type().(*types.Named)
				if !ok {
					continue
				}
				iface, ok := named.Underlying().(*types.Interface)
				if !ok || iface.NumMethods() == 0 {
					continue
				}
				// R37：排除客户端接口（XxxServiceClient——方法签名同模式但
				// 是调用方桩，R30-2 签名识别误伤：go2o 实测 62 子页 = 31
				// 服务 + 31 Client）
				if strings.HasSuffix(named.Obj().Name(), "Client") {
					continue
				}
				svcName, methods, paramTypes := grpcServiceFromInterface(pkg, iface, named.Obj().Name())
				if svcName == "" {
					continue
				}
				// R91：注册佐证——无对应 Register（RegisterXxxServer 包装
				// 函数或 grpc.RegisterService 直接注册）的接口签名识别
				// 跳过：自定义客户端（方法签名与 grpc 一致但用于调用
				// 外部服务——无注册）误识别为系统服务防护
				if !a.registeredServices[svcName] {
					continue
				}
				svcID := domain.CanonicalID("symbol:go:" + pkg.PkgPath + ":svc." + svcName)
				if err := emit(domain.Item{Node: &domain.CodeEntity{
					ID: svcID, Kind: domain.KindGrpcService,
					Name: "svc." + svcName,
					Properties: map[string]any{
						"service_name": svcName,
						"methods":      strings.Join(methods, ","),
						// R45：方法首参类型完整路径（外部接口判定——
						// 客户端实参类型 ∉ 本项目服务参数集合）
						"param_types": strings.Join(paramTypes, ","),
					},
				}}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// grpcServiceFromInterface 接口方法全部符合 grpc 模式 → 服务名（接口名
// 去 Server 后缀）+ 方法名列表 + 首参类型完整路径列表（R45 外部接口
// 判定）；任一方法不符合返回空。R48：方法须有 .pb.go 业务类型信号
// （仅 ctx+err 不算 grpc）。
func grpcServiceFromInterface(pkg *packages.Package, iface *types.Interface, typeName string) (string, []string, []string) {
	var methods, paramTypes []string
	for i := 0; i < iface.NumMethods(); i++ {
		m := iface.Method(i)
		sig, ok := m.Type().(*types.Signature)
		if !ok || !isGrpcMethodSig(pkg, sig) {
			return "", nil, nil
		}
		methods = append(methods, m.Name())
		// R45：请求对象类型——首参是 context.Context（常规形态 (ctx, req)）
		// 取第 2 参；否则取第 1 参（流式形态 (req, stream)）
		if pt := grpcRequestType(sig); pt != "" {
			paramTypes = append(paramTypes, pt)
		}
	}
	if len(methods) == 0 {
		return "", nil, nil
	}
	svc := typeName
	if strings.HasSuffix(svc, "Server") && len(svc) > len("Server") {
		svc = strings.TrimSuffix(svc, "Server")
	}
	return svc, methods, paramTypes
}

// typePath 类型 → 完整路径（pkg.Type；指针解包；非 Named 返回类型字符串）。
func typePath(t types.Type) string {
	if p, ok := t.(*types.Pointer); ok {
		return typePath(p.Elem())
	}
	if n, ok := t.(*types.Named); ok && n.Obj().Pkg() != nil {
		return n.Obj().Pkg().Path() + "." + n.Obj().Name()
	}
	return t.String()
}

// grpcRequestType 服务方法请求对象类型（R45）：首参是 context.Context
// → 第 2 参（常规 (ctx, req)）；否则第 1 参（流式 (req, stream)）。
func grpcRequestType(sig *types.Signature) string {
	if sig.Params().Len() == 0 {
		return ""
	}
	if sig.Params().Len() > 1 && isContextType(sig.Params().At(0).Type()) {
		return typePath(sig.Params().At(1).Type())
	}
	return typePath(sig.Params().At(0).Type())
}

// isGrpcMethodSig / hasPBType / pbDefinedType 已拆到 ast_grpc_sig.go
// （R48 判定——行数治理）。

// isContextType context.Context 类型。
func isContextType(t types.Type) bool {
	n, ok := t.(*types.Named)
	if !ok {
		return false
	}
	return n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == "context" && n.Obj().Name() == "Context"
}

// isErrorType error 接口类型（types.Universe 内置）。
func isErrorType(t types.Type) bool {
	return types.Identical(t, types.Universe.Lookup("error").Type())
}

// inGrpcPackage 类型是否引用 google.golang.org/grpc 包（指针解包；
// 泛型实例化如 grpc.ServerStreamingServer[Resp] 的 Obj 直接命中）。
func inGrpcPackage(t types.Type) bool {
	if p, ok := t.(*types.Pointer); ok {
		return inGrpcPackage(p.Elem())
	}
	n, ok := t.(*types.Named)
	if !ok {
		return false
	}
	return n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == "google.golang.org/grpc"
}

// ---- R86 外部注册场景：从 Register 函数定义发射（不依赖调用点） ----

// grpcRegisterDef Register 函数定义（.pb.go 签名识别）提取的服务注册
// 信息：服务名 + srv 参数接口类型。调用点在外部仓库（单独编译）时
// 调用点路径（emitGrpcServiceEntry）不触发——定义路径兜底。
type grpcRegisterDef struct {
	svcName string
	iface   *types.Named // srv 参数接口类型（XxxServer）
	fnPkg   string       // Register 函数所在包路径
	fnName  string
	line    int
	file    string
}

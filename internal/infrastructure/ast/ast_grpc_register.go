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
				svcName, methods := grpcServiceFromInterface(iface, named.Obj().Name())
				if svcName == "" {
					continue
				}
				svcID := domain.CanonicalID("symbol:go:" + pkg.PkgPath + ":svc." + svcName)
				if err := emit(domain.Item{Node: &domain.CodeEntity{
					ID: svcID, Kind: domain.KindGrpcService,
					Name: "svc." + svcName,
					Properties: map[string]any{
						"service_name": svcName,
						"methods":      strings.Join(methods, ","),
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
// 去 Server 后缀）+ 方法名列表；任一方法不符合返回空。
func grpcServiceFromInterface(iface *types.Interface, typeName string) (string, []string) {
	var methods []string
	for i := 0; i < iface.NumMethods(); i++ {
		m := iface.Method(i)
		sig, ok := m.Type().(*types.Signature)
		if !ok || !isGrpcMethodSig(sig) {
			return "", nil
		}
		methods = append(methods, m.Name())
	}
	if len(methods) == 0 {
		return "", nil
	}
	svc := typeName
	if strings.HasSuffix(svc, "Server") && len(svc) > len("Server") {
		svc = strings.TrimSuffix(svc, "Server")
	}
	return svc, methods
}

// isGrpcMethodSig grpc 方法签名模式：末返回值是 error，且首参是
// context.Context 或参数/返回值含 grpc 包类型（流式方法）。
func isGrpcMethodSig(sig *types.Signature) bool {
	res := sig.Results()
	if res == nil || res.Len() == 0 {
		return false
	}
	if !isErrorType(res.At(res.Len() - 1).Type()) {
		return false
	}
	if sig.Params().Len() > 0 && isContextType(sig.Params().At(0).Type()) {
		return true
	}
	for i := 0; i < sig.Params().Len(); i++ {
		if inGrpcPackage(sig.Params().At(i).Type()) {
			return true
		}
	}
	for i := 0; i < res.Len(); i++ {
		if inGrpcPackage(res.At(i).Type()) {
			return true
		}
	}
	return false
}

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

// collectRegisterServers 遍历项目内包，按**签名**识别 gRPC 注册函数
// （R30：不限 .pb.go 文件、不限 RegisterXxxServer 命名——手写注册
// 同样识别）：① 第一个参数类型是 grpc.ServiceRegistrar 接口或
// *grpc.Server；② 函数体调用 RegisterService 方法（protoc 生成与
// 手写通用，最强信号）。返回 map[string]string：pkgPath:funcName →
// 服务名（参数 2 类型名或 RegisterService 第一实参 desc 名提取）。
func collectRegisterServers(pkgs []*packages.Package, modules []string) map[string]string {
	out := map[string]string{}
	for _, pkg := range pkgs {
		if !isInModule(pkg.PkgPath, modules) {
			continue
		}
		for _, f := range pkg.Syntax {
			for _, decl := range f.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv != nil || fn.Body == nil {
					continue
				}
				obj := pkg.TypesInfo.Defs[fn.Name]
				if obj == nil {
					continue
				}
				sig, ok := obj.Type().(*types.Signature)
				if !ok || sig.Params().Len() < 2 {
					continue
				}

				if !isGrpcRegistrar(sig.Params().At(0).Type()) && !callsRegisterService(fn) {
					continue
				}
				if svc := registerServiceName(pkg, fn, sig); svc != "" {
					out[pkg.PkgPath+":"+fn.Name.Name] = svc
				}
			}
		}
	}
	return out
}

// isGrpcRegistrar 参数类型是 grpc.ServiceRegistrar 接口或 *grpc.Server。
func isGrpcRegistrar(t types.Type) bool {
	switch tt := t.(type) {
	case *types.Pointer:
		if n, ok := tt.Elem().(*types.Named); ok {
			return n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == "google.golang.org/grpc" &&
				n.Obj().Name() == "Server"
		}
	case *types.Named:
		return tt.Obj().Pkg() != nil && tt.Obj().Pkg().Path() == "google.golang.org/grpc" &&
			tt.Obj().Name() == "ServiceRegistrar"
	}
	return false
}

// callsRegisterService 函数体调用 RegisterService 方法（选择器
// x.RegisterService——注册动作的强信号）。
func callsRegisterService(fn *ast.FuncDecl) bool {
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if found {
			return false
		}
		if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "RegisterService" {
			found = true
			return false
		}
		return true
	})
	return found
}

// registerServiceName 从注册函数提取服务名：
// ① 参数 2 类型名去 Server 后缀（QueryServiceServer → QueryService）；
// ② 否则函数体内 RegisterService 第一实参 desc 名（&QueryService_
// ServiceDesc → QueryService；手写常用形态）。
func registerServiceName(pkg *packages.Package, fn *ast.FuncDecl, sig *types.Signature) string {
	_ = pkg
	if sig.Params().Len() >= 2 {
		if name := svcFromType(sig.Params().At(1).Type()); name != "" {
			return name
		}
	}
	var desc string
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if desc != "" {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); !ok || sel.Sel.Name != "RegisterService" {
			return true
		}
		switch a := call.Args[0].(type) {
		case *ast.UnaryExpr:
			if id, ok := a.X.(*ast.Ident); ok {
				desc = id.Name
			}
		case *ast.Ident:
			desc = a.Name
		}
		return false
	})
	return strings.TrimSuffix(desc, "_ServiceDesc")
}

// svcFromType 类型名去 Server 后缀（QueryServiceServer → QueryService；
// XxxServer → Xxx；非 Server 结尾返回空——impl struct 形态回退 desc）。
func svcFromType(t types.Type) string {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	n, ok := t.(*types.Named)
	if !ok {
		return ""
	}
	name := n.Obj().Name()
	if strings.HasSuffix(name, "Server") && len(name) > len("Server") {
		return strings.TrimSuffix(name, "Server")
	}
	return ""
}

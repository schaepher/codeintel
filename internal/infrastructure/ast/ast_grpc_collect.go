package ast

import (
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/packages"
)

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
	// R90：外部仓库 grpc 工具生成的 Register 定义（依赖包——fast 模式
	// 无 Syntax 仅 types；go/packages 顶层只返回 pattern 匹配包，依赖
	// 须经 Imports 递归）——types 级扫描：首参 grpc.ServiceRegistrar/
	// *grpc.Server + 第二参接口类型（svcFromType 去 Server 后缀）。
	// 本仓库调用点（markServiceEntry）据此识别注册调用。
	seen := map[string]bool{}
	var walk func(p *packages.Package)
	walk = func(p *packages.Package) {
		if p == nil || seen[p.PkgPath] {
			return
		}
		seen[p.PkgPath] = true
		if !isInModule(p.PkgPath, modules) && p.Types != nil {
			scope := p.Types.Scope()
			for _, name := range scope.Names() {
				fn, ok := scope.Lookup(name).(*types.Func)
				if !ok || !fn.Exported() {
					continue
				}
				sig, ok := fn.Type().(*types.Signature)
				if !ok || sig.Params().Len() < 2 {
					continue
				}
				// 判定：标准 grpc 首参 / 首参接口含 RegisterService 方法
				// （自定义 Registrar 形态——proto 生成常用）/ 函数名
				// Register<X>Server（配合第二参接口）
				if !isGrpcRegistrar(sig.Params().At(0).Type()) &&
					!ifaceHasMethod(sig.Params().At(0).Type(), "RegisterService") &&
					!isRegisterServerName(fn.Name()) {
					continue
				}
				if svc := svcFromType(sig.Params().At(1).Type()); svc != "" {
					out[p.PkgPath+":"+fn.Name()] = svc
				}
			}
		}
		for _, imp := range p.Imports {
			walk(imp)
		}
	}
	for _, p := range pkgs {
		walk(p)
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
		// R91：参数 2 是接口（手写注册自命名接口——无 Server 后缀）→
		// 接口名即服务名（注册佐证匹配）
		if named, ok := sig.Params().At(1).Type().(*types.Named); ok {
			if _, isIface := named.Underlying().(*types.Interface); isIface {
				return named.Obj().Name()
			}
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

// ifaceHasMethod 类型是否为含指定方法的接口（types 级——依赖包无
// Syntax 时的注册形态判定：自定义 Registrar 接口含 RegisterService）。
func ifaceHasMethod(t types.Type, method string) bool {
	named, ok := t.(*types.Named)
	if !ok {
		return false
	}
	iface, ok := named.Underlying().(*types.Interface)
	if !ok {
		return false
	}
	for i := 0; i < iface.NumMethods(); i++ {
		if iface.Method(i).Name() == method {
			return true
		}
	}
	return false
}

// collectDirectRegisters 手写直接注册（grpc.RegisterService(&Xxx_ServiceDesc,
// impl)——无 RegisterXxxServer 包装函数）的服务名集合（R91 注册佐证：
// desc 名去 _ServiceDesc 后缀）。
func collectDirectRegisters(pkgs []*packages.Package, modules []string) map[string]bool {
	out := map[string]bool{}
	for _, pkg := range pkgs {
		if !isInModule(pkg.PkgPath, modules) {
			continue
		}
		for _, f := range pkg.Syntax {
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) == 0 {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "RegisterService" {
					return true
				}
				// 调用方是 grpc.ServiceRegistrar（直接注册——真 grpc 包
				// 或本地模拟形态）
				if !inGrpcPackage(pkg.TypesInfo.TypeOf(sel.X)) &&
					!ifaceHasMethod(pkg.TypesInfo.TypeOf(sel.X), "RegisterService") {
					return true
				}
				switch a := call.Args[0].(type) {
				case *ast.UnaryExpr:
					if id, ok := a.X.(*ast.Ident); ok {
						out[strings.TrimSuffix(id.Name, "_ServiceDesc")] = true
					}
				case *ast.Ident:
					out[strings.TrimSuffix(a.Name, "_ServiceDesc")] = true
				}
				return true
			})
		}
	}
	return out
}

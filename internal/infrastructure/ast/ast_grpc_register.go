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

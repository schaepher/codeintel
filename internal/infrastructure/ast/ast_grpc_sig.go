package ast

// R48 grpc 方法签名判定（从 ast_grpc_register.go 拆出——行数治理）。
// 用户要求：仅"含 ctx 参数 + 含 err 返回值"不应视为 grpc——注册点
// 之外，仅当请求/响应业务类型定义在 .pb.go（protoc 生成）才算：
// Xxx(ctx, pbreq) (pbresp, err) 的 pbreq/pbresp 来自 .pb.go。

import (
	"go/types"
	"strings"

	"golang.org/x/tools/go/packages"
)

// isGrpcMethodSig grpc 方法签名模式：
// ① 末返回值是 error
// ② 首参是 context.Context 或参数/返回值含 grpc 包类型（流式/基本形态）
// ③ 业务类型（非 ctx/err/grpc 的 Named 参数/返回）定义在 .pb.go
//    （protoc 生成——grpc 服务强信号；普通业务接口 (ctx, req)(resp,
//    err) 形态无 pb 类型 → 排除）
func isGrpcMethodSig(pkg *packages.Package, sig *types.Signature) bool {
	res := sig.Results()
	if res == nil || res.Len() == 0 {
		return false
	}
	if !isErrorType(res.At(res.Len() - 1).Type()) {
		return false
	}
	if sig.Params().Len() > 0 && isContextType(sig.Params().At(0).Type()) {
		return hasPBType(pkg, sig)
	}
	for i := 0; i < sig.Params().Len(); i++ {
		if inGrpcPackage(sig.Params().At(i).Type()) {
			return hasPBType(pkg, sig)
		}
	}
	for i := 0; i < res.Len(); i++ {
		if inGrpcPackage(res.At(i).Type()) {
			return hasPBType(pkg, sig)
		}
	}
	return false
}

// hasPBType 参数/返回中任一业务类型定义在 .pb.go 文件（protoc 生成——
// pbreq/pbresp 的强信号）。ctx（首参）/err（末返回）跳过；grpc 包类型
// （流式 stream）跳过。
func hasPBType(pkg *packages.Package, sig *types.Signature) bool {
	for i := 0; i < sig.Params().Len(); i++ {
		t := sig.Params().At(i).Type()
		if i == 0 && isContextType(t) {
			continue
		}
		if pbDefinedType(pkg, t) {
			return true
		}
	}
	for i := 0; i < sig.Results().Len(); i++ {
		t := sig.Results().At(i).Type()
		if i == sig.Results().Len()-1 && isErrorType(t) {
			continue
		}
		if pbDefinedType(pkg, t) {
			return true
		}
	}
	return false
}

// pbDefinedType 类型（解指针/解容器）定义文件是否 .pb.go（protoc
// 生成）；内置类型/未命名类型返回 false。
func pbDefinedType(pkg *packages.Package, t types.Type) bool {
	switch tt := t.(type) {
	case *types.Pointer:
		return pbDefinedType(pkg, tt.Elem())
	case *types.Slice:
		return pbDefinedType(pkg, tt.Elem())
	case *types.Map:
		return pbDefinedType(pkg, tt.Key()) || pbDefinedType(pkg, tt.Elem())
	case *types.Named:
		if tt.Obj().Pkg() == nil {
			return false // 内置（string 等）
		}
		f := pkg.Fset.PositionFor(tt.Obj().Pos(), false).Filename
		return strings.HasSuffix(f, ".pb.go")
	}
	return false
}

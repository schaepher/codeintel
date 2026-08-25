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

// isGrpcMethodSig grpc 方法签名模式（R48/R49 严格化——用户要求：
// 匹配 grpc 实现时参数一定要只有两个——context + pb request，返回
// (pbresp, err)，严格遵守 Xxx(ctx, pbreq) (pbresp, err)）：
// ① 参数恰好 2：ctx（context.Context）+ pbreq（定义在 .pb.go）
// ② 返回恰好 2：pbresp（定义在 .pb.go）+ err
// 流式形态（(req, stream) (error)）不满足 → 排除。
func isGrpcMethodSig(pkg *packages.Package, sig *types.Signature) bool {
	if sig.Params().Len() != 2 || sig.Results().Len() != 2 {
		return false
	}
	if !isContextType(sig.Params().At(0).Type()) {
		return false
	}
	if !pbDefinedType(pkg, sig.Params().At(1).Type()) {
		return false
	}
	if !pbDefinedType(pkg, sig.Results().At(0).Type()) {
		return false
	}
	return isErrorType(sig.Results().At(1).Type())
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

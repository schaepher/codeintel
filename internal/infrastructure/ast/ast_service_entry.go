package ast

import (
	"go/ast"
	"go/types"

	"github.com/schaepher/codeintel/internal/canonicalizer"
	"github.com/schaepher/codeintel/internal/domain"
	"golang.org/x/tools/go/packages"
)

// handlerFuncNode 提取 http.Handle/HandleFunc 的 handler 参数（第二个参数），
// 支持形态：
//
//	http.Handle("/", myHandler)          // 变量（具名函数）
//	http.Handle("/", http.HandlerFunc(f)) // HandlerFunc 包装
//	http.HandleFunc("/", home)            // 具名函数
//
// 返回标记 serves_http 的节点；匿名函数（FuncLit）与外部函数返回 nil。
func handlerFuncNode(pkg *packages.Package, call *ast.CallExpr, repo *domain.Repository) *domain.CodeEntity {
	if len(call.Args) < 2 {
		return nil
	}
	arg := call.Args[1]

	if ce, ok := arg.(*ast.CallExpr); ok {
		if sel, ok2 := ce.Fun.(*ast.SelectorExpr); ok2 && sel.Sel.Name == "HandlerFunc" && len(ce.Args) > 0 {
			arg = ce.Args[0]
		}
	}
	id, ok := arg.(*ast.Ident)
	if !ok {
		return nil
	}
	obj := pkg.TypesInfo.Uses[id]
	if obj == nil {
		obj = pkg.TypesInfo.Defs[id]
	}
	fn, ok := obj.(*types.Func)
	if !ok || fn.Pkg() == nil || !isInModule(fn.Pkg().Path(), repo.Modules) {
		return nil
	}
	fnID, fnKind := fnID(fn)
	if fnID == "" {
		return nil
	}
	return nodeFor(repo, pkg, fn, fnID, fnKind, map[string]bool{"serves_http": true})
}

// serviceImplNodes 提取 RegisterXxxServer 调用的第二个参数（服务实现），
// 生成标记 serves_grpc 的节点（作为顶层服务入口）。参数形态支持：
//
//	pb.RegisterGreeterServer(s, &greeterImpl{})   // 复合字面量
//	pb.RegisterGreeterServer(s, newGreeterServer()) // 构造函数
//	pb.RegisterGreeterServer(s, impl)               // 变量
//	pb.RegisterGreeterServer(s, di.Get("x"))        // DI 容器（返回接口）
//
// 接口形态（DI/变量注入——无直接调用关系）：类型匹配找业务实现
// （types.Implements 全模块扫描——R95-2，排除 Unimplemented 桩），
// grpc_impl 边直指具体实现；无实现时回退接口节点（查询端 implements
// 追链兜底）。返回 nil 表示无法解析为项目内类型。
func serviceImplNodes(a *Adapter, pkg *packages.Package, call *ast.CallExpr, repo *domain.Repository) []*domain.CodeEntity {
	if len(call.Args) < 2 {
		return nil
	}
	t := pkg.TypesInfo.TypeOf(call.Args[1])
	if t == nil {
		return nil
	}
	// R95：第二参是构造器调用（函数声明返回接口、函数体 return 具体
	// 实现）→ concreteReturnType 追踪 return 的具体类型
	if callArg, ok := call.Args[1].(*ast.CallExpr); ok {
		if ct := a.concreteReturnType(pkg, callArg); ct != nil {
			t = ct
		}
	}
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok {
		return nil
	}
	obj := named.Obj()
	if obj.Pkg() == nil {
		return nil
	}
	nodeOf := func(n *types.Named) *domain.CodeEntity {
		pos := pkg.Fset.PositionFor(n.Obj().Pos(), false)
		return &domain.CodeEntity{
			ID:        canonicalizer.GoSymbolID(n.Obj().Pkg().Path(), n.Obj().Name()),
			Kind:      domain.KindStruct,
			Name:      n.Obj().Name(),
			FilePath:  relPath(repo.Path, pos.Filename),
			LineStart: pos.Line,
			LineEnd:   pos.Line,
			Properties: map[string]any{
				"serves_grpc": "true",
			},
		}
	}
	if _, isIface := named.Underlying().(*types.Interface); isIface {
		// R95-2：接口形态（DI 容器/变量注入——无直接调用关系）→ 类型
		// 匹配找业务实现（全模块 types.Implements）
		if impls := a.loadedInterfaceImpls(named); len(impls) > 0 {
			var out []*domain.CodeEntity
			for _, impl := range impls {
				out = append(out, nodeOf(impl))
			}
			return out
		}
		// 无实现 → 接口节点（查询端 implements 追链兜底）
		pos := pkg.Fset.PositionFor(obj.Pos(), false)
		return []*domain.CodeEntity{{
			ID:        canonicalizer.GoSymbolID(obj.Pkg().Path(), obj.Name()),
			Kind:      domain.KindInterface,
			Name:      obj.Name(),
			FilePath:  relPath(repo.Path, pos.Filename),
			LineStart: pos.Line,
			LineEnd:   pos.Line,
			Properties: map[string]any{
				"serves_grpc": "true",
			},
		}}
	}
	if !isInModule(obj.Pkg().Path(), repo.Modules) {
		return nil
	}
	return []*domain.CodeEntity{nodeOf(named)}
}

// loadedInterfaceImpls 从已加载包（pkgsByPath——Index 时填充）收集
// 实现接口的具体类型（types.Implements 指针/值方法集任一；排除
// Unimplemented 桩）。DI 场景无直接调用关系——纯类型匹配。
func (a *Adapter) loadedInterfaceImpls(iface *types.Named) []*types.Named {
	var pkgs []*packages.Package
	for _, p := range a.pkgsByPath {
		pkgs = append(pkgs, p)
	}
	return interfaceImplsInModule(pkgs, a.modules, iface)
}

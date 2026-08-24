package ast

// R31 `query http-routes` 数据源：构建期识别 HTTP 路由注册（待办 1
// http 部分——两个 resolver 各自实现识别模式）：
// - 原生 net/http：http.Handle/HandleFunc 包级调用 + ServeMux 方法
//   调用（mux := http.NewServeMux(); mux.HandleFunc(...)）；method 空
//   （HandleFunc 匹配所有方法）
// - gin：*gin.Engine/*gin.RouterGroup 的 GET/POST/.../Any 方法调用，
//   Group 前缀拼接（变量赋值继承 + 一级链式）；Static 系列静态资源
//   不算（噪音）
// 每注册点发射 http_route 节点（method/path/handler/resolver/register
// ——Q1 契约字段）。

import (
	"fmt"
	"go/ast"
	"go/types"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
	"golang.org/x/tools/go/packages"
)

// ginRouterMethods gin 路由注册方法（排除 Static/StaticFS 等静态资源）。
var ginRouterMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "DELETE": true, "PATCH": true,
	"HEAD": true, "OPTIONS": true, "Any": true,
}

// scanGinGroups 文件级收集 gin Group 前缀（`g := r.Group("/api")`——
// r 是已收集组变量则继承前缀：嵌套 Group）。两遍：先直接字面量，
// 再嵌套拼接。
func scanGinGroups(pkg *packages.Package, f *ast.File) map[string]string {
	groups := map[string]string{}
	for pass := 0; pass < 2; pass++ {
		ast.Inspect(f, func(n ast.Node) bool {
			rhs := ""
			switch t := n.(type) {
			case *ast.AssignStmt:
				if len(t.Lhs) == 1 && len(t.Rhs) == 1 {
					if id, ok := t.Lhs[0].(*ast.Ident); ok {
						rhs = id.Name
					}
				}
			case *ast.ValueSpec:
				if len(t.Names) == 1 && len(t.Values) == 1 {
					rhs = t.Names[0].Name
				}
			}
			if rhs == "" {
				return true
			}
			if _, done := groups[rhs]; done {
				return true
			}
			if p, ok := ginGroupPath(pkg, assignRHS(n), groups); ok {
				groups[rhs] = p
			}
			return true
		})
	}
	return groups
}

// assignRHS 从 AssignStmt/ValueSpec 取右值表达式。
func assignRHS(n ast.Node) ast.Expr {
	switch t := n.(type) {
	case *ast.AssignStmt:
		if len(t.Rhs) == 1 {
			return t.Rhs[0]
		}
	case *ast.ValueSpec:
		if len(t.Values) == 1 {
			return t.Values[0]
		}
	}
	return nil
}

// ginGroupPath 表达式是 gin Group 调用 → 前缀路径（r.Group("/api")；
// r 名已在 groups → 拼接继承）。非 Group 调用返回 false。
func ginGroupPath(pkg *packages.Package, e ast.Expr, groups map[string]string) (string, bool) {
	call, ok := e.(*ast.CallExpr)
	if !ok || len(call.Args) < 1 {
		return "", false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Group" || !isGinRouter(pkg, sel.X) {
		return "", false
	}
	base := ""
	if id, ok := sel.X.(*ast.Ident); ok {
		base = groups[id.Name]
	}
	path := extractStringArg(pkg, nil, call.Args[0])
	if path == "" {
		return "", false
	}
	return strings.TrimSuffix(base, "/") + "/" + strings.TrimPrefix(path, "/"), true
}

// isGinRouter 表达式类型是 *gin.Engine 或 *gin.RouterGroup。
func isGinRouter(pkg *packages.Package, e ast.Expr) bool {
	t := pkg.TypesInfo.TypeOf(e)
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	n, ok := t.(*types.Named)
	if !ok {
		return false
	}
	return n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == "github.com/gin-gonic/gin" &&
		(n.Obj().Name() == "Engine" || n.Obj().Name() == "RouterGroup")
}

// isServeMux 表达式类型是 *http.ServeMux。
func isServeMux(pkg *packages.Package, e ast.Expr) bool {
	t := pkg.TypesInfo.TypeOf(e)
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	n, ok := t.(*types.Named)
	if !ok {
		return false
	}
	return n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == "net/http" && n.Obj().Name() == "ServeMux"
}

// routeHandlerName 路由 handler 名——形态矩阵：函数 Ident /
// http.HandlerFunc(f) 包装 / 方法值 x.Method（recv.Method——ana serve
// 的 s.handleRoots 形态，R31 实测遗漏后补）。
func routeHandlerName(pkg *packages.Package, arg ast.Expr) string {
	_ = pkg
	switch a := arg.(type) {
	case *ast.Ident:
		return a.Name
	case *ast.CallExpr:
		if sel, ok := a.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "HandlerFunc" && len(a.Args) > 0 {
			if id, ok := a.Args[0].(*ast.Ident); ok {
				return id.Name
			}
		}
	case *ast.SelectorExpr:
		if id, ok := a.X.(*ast.Ident); ok {
			return id.Name + "." + a.Sel.Name
		}
	}
	return ""
}

// emitHTTPRoute 发射 http_route 节点（每注册点一个；Q1 契约字段：
// method/path/handler/resolver/register）。
func (ctx *fileCtx) emitHTTPRoute(method, path, handler, resolver string, call *ast.CallExpr) {
	if path == "" || handler == "" {
		return
	}
	pos := ctx.pkg.Fset.PositionFor(call.Pos(), false)
	ctx.routeSeq++
	_ = ctx.emit(domain.Item{Node: &domain.CodeEntity{
		ID:   domain.CanonicalID(fmt.Sprintf("symbol:go:%s:route.%d", ctx.pkg.PkgPath, ctx.routeSeq)),
		Kind: domain.KindHTTPRoute,
		Name: strings.TrimSpace(method + " " + path),
		Properties: map[string]any{
			"method":   method,
			"path":     path,
			"handler":  handler,
			"resolver": resolver,
			"register": fmt.Sprintf("%s:%d", relPath(ctx.repo.Path, pos.Filename), pos.Line),
		},
	}})
}

// emitGinRouteCall gin 路由注册调用（x.GET("/path", h)——x 是
// *gin.Engine/*gin.RouterGroup；Group 前缀拼接）。
func (ctx *fileCtx) emitGinRouteCall(call *ast.CallExpr, sel *ast.SelectorExpr, xid *ast.Ident) {
	if !ginRouterMethods[sel.Sel.Name] || len(call.Args) < 2 {
		return
	}
	if !isGinRouter(ctx.pkg, xid) {
		return
	}
	path := extractStringArg(ctx.pkg, ctx.methodVars, call.Args[0])
	if base := ctx.ginGroups[xid.Name]; base != "" {
		path = strings.TrimSuffix(base, "/") + "/" + strings.TrimPrefix(path, "/")
	}
	ctx.emitHTTPRoute(sel.Sel.Name, path, routeHandlerName(ctx.pkg, call.Args[1]), "gin", call)
}

// emitGinChainedCall gin 链式路由注册（r.Group("/api").GET("/x", h)）：
// 接收者是 Group 调用表达式——组前缀递归解析后按路由注册处理。
func (ctx *fileCtx) emitGinChainedCall(call *ast.CallExpr, sel *ast.SelectorExpr, callee *types.Func) {
	_ = callee
	if !ginRouterMethods[sel.Sel.Name] || len(call.Args) < 2 {
		return
	}
	prefix, ok := ginChainedPrefix(ctx.pkg, sel.X)
	if !ok {
		return
	}
	path := extractStringArg(ctx.pkg, ctx.methodVars, call.Args[0])
	path = strings.TrimSuffix(prefix, "/") + "/" + strings.TrimPrefix(path, "/")
	ctx.emitHTTPRoute(sel.Sel.Name, path, routeHandlerName(ctx.pkg, call.Args[1]), "gin", call)
}

// ginChainedPrefix 链式组表达式（r.Group("/a").Group("/b")...）→ 前缀
// 路径；底部变量须是 gin router（*gin.Engine/*gin.RouterGroup）。
func ginChainedPrefix(pkg *packages.Package, e ast.Expr) (string, bool) {
	prefix := ""
	cur := e
	for {
		call, ok := cur.(*ast.CallExpr)
		if !ok || len(call.Args) < 1 {
			return "", false
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Group" {
			return "", false
		}
		seg := extractStringArg(pkg, nil, call.Args[0])
		if seg == "" {
			return "", false
		}
		prefix = strings.TrimSuffix(prefix, "/") + "/" + strings.TrimPrefix(seg, "/")
		if id, isID := sel.X.(*ast.Ident); isID {
			if isGinRouter(pkg, id) {
				return prefix, true
			}
			return "", false
		}
		cur = sel.X
	}
}

// emitServeMuxCall ServeMux 方法调用（mux.Handle/HandleFunc——method 空）。
func (ctx *fileCtx) emitServeMuxCall(call *ast.CallExpr, callee *types.Func, xid *ast.Ident) {
	if callee == nil || callee.Pkg() == nil || callee.Pkg().Path() != "net/http" ||
		(callee.Name() != "Handle" && callee.Name() != "HandleFunc") || len(call.Args) < 2 {
		return
	}
	if !isServeMux(ctx.pkg, xid) {
		return
	}
	path := extractStringArg(ctx.pkg, ctx.methodVars, call.Args[0])
	ctx.emitHTTPRoute("", path, routeHandlerName(ctx.pkg, call.Args[1]), "native", call)
}

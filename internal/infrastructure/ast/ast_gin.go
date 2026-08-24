package ast

// R31 gin resolver（从 http_routes.go 拆出——行数治理）：*gin.Engine/
// *gin.RouterGroup 路由注册识别——路由方法（GET/POST/.../Any）+ Group
// 前缀拼接（变量继承 + 链式）+ Handle 通用注册 + 多 handler（中间件）。

import (
	"go/ast"
	"go/types"
	"strings"

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

// emitGinRouteCall gin 路由注册调用（x.GET("/path", h)——x 是
// *gin.Engine/*gin.RouterGroup；Group 前缀拼接）。形态：
// - 路由方法 x.GET/POST/...：args[0]=路径、args[1:]=handlers
//   （多 handler 时最后一个为业务 handler——中间件在前）
// - 通用注册 x.Handle("GET", "/path", h)：args[0]=method、args[1]=路径
func (ctx *fileCtx) emitGinRouteCall(call *ast.CallExpr, sel *ast.SelectorExpr, xid *ast.Ident) {
	if !isGinRouter(ctx.pkg, xid) {
		return
	}
	var method, path string
	var handlerArg ast.Expr
	switch {
	case ginRouterMethods[sel.Sel.Name] && len(call.Args) >= 2:
		method = sel.Sel.Name
		path = extractStringArg(ctx.pkg, ctx.methodVars, call.Args[0])
		handlerArg = call.Args[len(call.Args)-1] // 多 handler：最后一个为业务 handler
	case sel.Sel.Name == "Handle" && len(call.Args) >= 3:
		method = extractStringArg(ctx.pkg, ctx.methodVars, call.Args[0])
		path = extractStringArg(ctx.pkg, ctx.methodVars, call.Args[1])
		handlerArg = call.Args[len(call.Args)-1]
	default:
		return
	}
	if base := ctx.ginGroups[xid.Name]; base != "" {
		path = strings.TrimSuffix(base, "/") + "/" + strings.TrimPrefix(path, "/")
	}
	hn, hid := routeHandlerName(ctx.pkg, handlerArg)
	ctx.emitHTTPRoute(method, path, hn, hid, "gin", call)
}

// emitGinChainedCall gin 链式路由注册（r.Group("/api").GET("/x", h)）：
// 接收者是 Group 调用表达式——组前缀递归解析后按路由注册处理（与
// emitGinRouteCall 同形态：路由方法 / Handle 通用注册 / 多 handler）。
func (ctx *fileCtx) emitGinChainedCall(call *ast.CallExpr, sel *ast.SelectorExpr, callee *types.Func) {
	_ = callee
	var method, path string
	var handlerArg ast.Expr
	switch {
	case ginRouterMethods[sel.Sel.Name] && len(call.Args) >= 2:
		method = sel.Sel.Name
		path = extractStringArg(ctx.pkg, ctx.methodVars, call.Args[0])
		handlerArg = call.Args[len(call.Args)-1]
	case sel.Sel.Name == "Handle" && len(call.Args) >= 3:
		method = extractStringArg(ctx.pkg, ctx.methodVars, call.Args[0])
		path = extractStringArg(ctx.pkg, ctx.methodVars, call.Args[1])
		handlerArg = call.Args[len(call.Args)-1]
	default:
		return
	}
	prefix, ok := ginChainedPrefix(ctx.pkg, sel.X)
	if !ok {
		return
	}
	path = strings.TrimSuffix(prefix, "/") + "/" + strings.TrimPrefix(path, "/")
	hn, hid := routeHandlerName(ctx.pkg, handlerArg)
	ctx.emitHTTPRoute(method, path, hn, hid, "gin", call)
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

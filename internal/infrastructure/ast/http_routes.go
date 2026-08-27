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
	"go/token"
	"go/types"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
	"golang.org/x/tools/go/packages"
)

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
// 的 s.handleRoots 形态，R31 实测遗漏后补）/ 匿名函数（FuncLit——
// gin 内联 handler 常见，标注 "(匿名)" 不丢路由）。R37：同时解析
// handler 的 canonical ID（handler_id——流程页按符号展开调用链；
// 短名无法解析方法值/跨包同名）。无法解析（匿名/内置）返回空 id。
func routeHandlerName(pkg *packages.Package, arg ast.Expr) (string, string) {
	switch a := arg.(type) {
	case *ast.Ident:
		return a.Name, funcCanonicalID(pkg, a)
	case *ast.CallExpr:
		if sel, ok := a.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "HandlerFunc" && len(a.Args) > 0 {
			return routeHandlerName(pkg, a.Args[0])
		}
	case *ast.SelectorExpr:
		if id, ok := a.X.(*ast.Ident); ok {
			return id.Name + "." + a.Sel.Name, methodValueCanonicalID(pkg, id, a.Sel.Name)
		}
	case *ast.FuncLit:
		return "(匿名)", ""
	}
	return "", ""
}

// funcCanonicalID 函数 Ident → canonical ID（symbol:go:<pkg>:<name>）。
func funcCanonicalID(pkg *packages.Package, id *ast.Ident) string {
	if id == nil {
		return ""
	}
	obj := pkg.TypesInfo.ObjectOf(id)
	fn, ok := obj.(*types.Func)
	if !ok || fn.Pkg() == nil {
		return ""
	}
	return "symbol:go:" + fn.Pkg().Path() + ":" + fn.Name()
}

// methodValueCanonicalID 方法值 x.Method → canonical ID：x 是包名（跨包
// 函数，render.Home）或变量（方法值，s.orders → (T).Method——解指针取
// 类型名；指针/值接收者 canonicalizer 不区分）。
func methodValueCanonicalID(pkg *packages.Package, x *ast.Ident, method string) string {
	if x == nil {
		return ""
	}
	if pn, ok := pkg.TypesInfo.ObjectOf(x).(*types.PkgName); ok {
		return "symbol:go:" + pn.Imported().Path() + ":" + method
	}
	t := pkg.TypesInfo.TypeOf(x)
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	n, ok := t.(*types.Named)
	if !ok || n.Obj().Pkg() == nil {
		return ""
	}
	return "symbol:go:" + n.Obj().Pkg().Path() + ":(" + n.Obj().Name() + ")." + method
}

// ginBindMethods gin 请求绑定方法（ShouldBind 系列 + Bind 系列——handler
// 请求对象采集 R100 待办11：外部接口请求对象判定）。
var ginBindMethods = map[string]bool{
	"ShouldBind": true, "ShouldBindJSON": true, "ShouldBindQuery": true,
	"ShouldBindXML": true, "ShouldBindYAML": true, "ShouldBindForm": true,
	"Bind": true, "BindJSON": true, "BindQuery": true, "BindXML": true,
}

// handlerReqTypes 路由 handler 的请求对象类型（ShouldBind/Bind(&req) 的
// req 类型全路径，逗号分隔写入路由节点 req_types——R100 待办11）：
// FuncLit 直接解析函数体；Ident 查同包 FuncDecl；http.HandlerFunc 包装
// 递归。方法值（x.Method）暂不解析（跨文件/跨包成本高——形态矩阵先
// 覆盖内联 + 具名函数）。
func handlerReqTypes(pkg *packages.Package, arg ast.Expr) []string {
	switch a := arg.(type) {
	case *ast.FuncLit:
		if a.Body != nil {
			return reqTypesFromBody(pkg, a.Body)
		}
	case *ast.Ident:
		for _, f := range pkg.Syntax {
			for _, decl := range f.Decls {
				fd, ok := decl.(*ast.FuncDecl)
				if !ok || fd.Name.Name != a.Name || fd.Body == nil {
					continue
				}
				return reqTypesFromBody(pkg, fd.Body)
			}
		}
	case *ast.CallExpr:
		if sel, ok := a.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "HandlerFunc" && len(a.Args) > 0 {
			return handlerReqTypes(pkg, a.Args[0])
		}
	}
	return nil
}

// reqTypesFromBody 函数体请求绑定调用（c.ShouldBind(&req)）的第一实参
// （&X）类型全路径——去重保持顺序。
func reqTypesFromBody(pkg *packages.Package, body *ast.BlockStmt) []string {
	var out []string
	seen := map[string]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, isSel := call.Fun.(*ast.SelectorExpr)
		if !isSel || !ginBindMethods[sel.Sel.Name] || len(call.Args) == 0 {
			return true
		}
		un, isUn := call.Args[0].(*ast.UnaryExpr)
		if !isUn || un.Op != token.AND {
			return true
		}
		if t := typePath(pkg.TypesInfo.TypeOf(un.X)); t != "" && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
		return true
	})
	return out
}

// emitHTTPRoute 发射 http_route 节点（每注册点一个；Q1 契约字段：
// method/path/handler/resolver/register + R37 handler_id + R100 req_types）。
func (ctx *fileCtx) emitHTTPRoute(method, path, handler, handlerID, resolver string, reqTypes []string, call *ast.CallExpr) {
	if path == "" || handler == "" {
		return
	}
	pos := ctx.pkg.Fset.PositionFor(call.Pos(), false)
	ctx.routeSeq++
	props := map[string]any{
		"method":     method,
		"path":       path,
		"handler":    handler,
		"handler_id": handlerID,
		"resolver":   resolver,
		"register":   fmt.Sprintf("%s:%d", relPath(ctx.repo.Path, pos.Filename), pos.Line),
	}
	if len(reqTypes) > 0 {
		props["req_types"] = strings.Join(reqTypes, ",")
	}
	_ = ctx.emit(domain.Item{Node: &domain.CodeEntity{
		ID:         domain.CanonicalID(fmt.Sprintf("symbol:go:%s:route.%d", ctx.pkg.PkgPath, ctx.routeSeq)),
		Kind:       domain.KindHTTPRoute,
		Name:       strings.TrimSpace(method + " " + path),
		Properties: props,
	}})
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
	hn, hid := routeHandlerName(ctx.pkg, call.Args[1])
	ctx.emitHTTPRoute("", path, hn, hid, "native", nil, call)
}

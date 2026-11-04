package action

// T09b：生成注册 helper 的源码证明与具体注册实参类型。

import (
	"go/ast"
	"go/token"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// seqRegisterHelperDecl 注册 helper 的声明与包路径（helper canonical ID）。
func (a *Actions) seqRegisterHelperDecl(req CodeSequenceRequest,
	hid domain.CanonicalID) (*ast.FuncDecl, *seqFileCtx, string, bool) {
	n, err := a.repo.GetSymbol(hid)
	if err != nil || n == nil || n.FilePath == "" {
		return nil, nil, "", false
	}
	ctx := seqFileCtxFor(req, n.FilePath)
	if ctx == nil || ctx.file == nil {
		return nil, nil, "", false
	}
	name := seqDeclNameOf(string(hid))
	for _, d := range ctx.file.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if ok && fd.Name != nil && fd.Name.Name == name && fd.Body != nil {
			return fd, ctx, pkgOfID(hid), true
		}
	}
	return nil, nil, "", false
}

// seqRegisterHelperProof 生成注册 helper 的源码证明：第二参为 <X>Handler
// 接口，函数体内把该接口（或其生成的 wrapper）交给 NewHandler。
type seqRegisterHelperProofInfo struct {
	ifaceID   string
	ifaceName string
	handler   string
}

func seqRegisterHelperProof(ctx *seqFileCtx, fn *ast.FuncDecl, helperPkg string) (seqRegisterHelperProofInfo, bool) {
	var out seqRegisterHelperProofInfo
	if ctx == nil || ctx.file == nil || fn == nil || fn.Type == nil || fn.Type.Params == nil {
		return out, false
	}
	params := fn.Type.Params.List
	if len(params) < 2 {
		return out, false
	}
	// 第二参接口身份（helper 所在包或导入包）。
	ifaceID, ok := seqTypeExprCanonical(ctx, helperPkg, params[1].Type)
	if !ok {
		return out, false
	}
	ifaceName := pkgNameOfID(ifaceID)
	if !strings.HasSuffix(ifaceName, "Handler") || ifaceName == "Handler" {
		return out, false
	}
	out.ifaceID = ifaceID
	out.ifaceName = ifaceName
	out.handler = strings.TrimSuffix(ifaceName, "Handler")
	// 第一参必须是 go-micro server.Server（selector + import 路径）。
	first := params[0].Type
	if star, ok := first.(*ast.StarExpr); ok {
		first = star.X
	}
	sel, ok := first.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Server" {
		return out, false
	}
	if id, ok := sel.X.(*ast.Ident); ok {
		if p := seqImportPath(ctx.file, id.Name); !strings.Contains(p, "go-micro") || !strings.HasSuffix(p, "/server") {
			return out, false
		}
	} else {
		return out, false
	}
	// 函数体：真实 NewHandler(wrapper) —— wrapper 必须是本地结构体且嵌入接口。
	proven := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if proven {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "NewHandler" || len(call.Args) == 0 {
			return true
		}
		arg := runtimeUnparen(call.Args[0])
		if u, ok := arg.(*ast.UnaryExpr); ok && u.Op == token.AND {
			arg = runtimeUnparen(u.X)
		}
		// 形态一：`NewHandler(hdlr)` 直接转发声明的 handler 形参。
		if id, isID := arg.(*ast.Ident); isID && len(params[1].Names) > 0 {
			for _, name := range params[1].Names {
				if name.Name == id.Name {
					proven = true
					return false
				}
			}
			return true
		}
		// 形态二：真实生成代码的 wrapper `NewHandler(&Ledger{h}, ...)`——
		// wrapper 必须嵌入声明的接口。
		lit, ok := arg.(*ast.CompositeLit)
		if !ok {
			return true
		}
		wrapperName := exprTypeName(lit.Type)
		if wrapperName == "" || !seqWrapperEmbedsIface(fn.Body, wrapperName, out.ifaceName) {
			return true
		}
		proven = true
		return false
	})
	return out, proven
}

// seqWrapperEmbedsIface 生成 wrapper 结构体是否嵌入该 handler 接口字段
// （`type Ledger struct { LedgerHandler }`）。
func seqWrapperEmbedsIface(body *ast.BlockStmt, wrapperName, ifaceName string) bool {
	if body == nil {
		return false
	}
	ok := false
	ast.Inspect(body, func(n ast.Node) bool {
		if ok {
			return false
		}
		gd, isGen := n.(*ast.GenDecl)
		if !isGen || gd.Tok != token.TYPE {
			return true
		}
		for _, spec := range gd.Specs {
			ts, isType := spec.(*ast.TypeSpec)
			if !isType || ts.Name == nil || ts.Name.Name != wrapperName {
				continue
			}
			st, isStruct := ts.Type.(*ast.StructType)
			if !isStruct || st.Fields == nil {
				continue
			}
			for _, f := range st.Fields.List {
				if len(f.Names) != 0 {
					continue
				}
				if exprTypeName(f.Type) == ifaceName {
					ok = true
					return false
				}
			}
		}
		return true
	})
	return ok
}

// seqConcreteRegistrationArg 注册实参的准确具体类型 canonical ID：
// `&Handler{...}` / `Handler{...}` / `new(Handler)` / 变量声明类型。
func seqConcreteRegistrationArg(ctx *seqFileCtx, callerPkg string, arg ast.Expr) (string, bool) {
	for {
		switch x := runtimeUnparen(arg).(type) {
		case *ast.UnaryExpr:
			if x.Op == token.AND {
				arg = x.X
				continue
			}
		case *ast.CompositeLit:
			return seqTypeExprCanonical(ctx, callerPkg, x.Type)
		case *ast.CallExpr:
			if id, ok := x.Fun.(*ast.Ident); ok && id.Name == "new" && id.Obj == nil && len(x.Args) == 1 {
				return seqTypeExprCanonical(ctx, callerPkg, x.Args[0])
			}
		case *ast.Ident:
			if x.Obj == nil {
				return "", false
			}
			switch d := x.Obj.Decl.(type) {
			case *ast.Field:
				return seqTypeExprCanonical(ctx, callerPkg, d.Type)
			case *ast.ValueSpec:
				return seqTypeExprCanonical(ctx, callerPkg, d.Type)
			}
		}
		return "", false
	}
}

// seqTypeExprCanonical 类型表达式 → canonical ID（当前包或导入包）。
func seqTypeExprCanonical(ctx *seqFileCtx, fallbackPkg string, e ast.Expr) (string, bool) {
	for {
		star, ok := e.(*ast.StarExpr)
		if !ok {
			break
		}
		e = star.X
	}
	switch t := e.(type) {
	case *ast.Ident:
		if t.Name == "" {
			return "", false
		}
		return "symbol:go:" + fallbackPkg + ":" + t.Name, true
	case *ast.SelectorExpr:
		id, ok := t.X.(*ast.Ident)
		if !ok {
			return "", false
		}
		p := seqImportPath(ctx.file, id.Name)
		if p == "" {
			return "", false
		}
		return "symbol:go:" + p + ":" + t.Sel.Name, true
	}
	return "", false
}

// seqContainsString 切片包含。
func seqContainsString(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// seqRegistrationConflict 扫描 helper 的全部调用点：注册实参解析到不同具体
// 实现 → 冲突（返回去重后的实现列表）。同一实现重复注册不算冲突。
func (a *Actions) seqRegistrationConflict(req CodeSequenceRequest,
	helperIDs []domain.CanonicalID, helperName string) ([]string, bool) {
	seen := map[string]bool{}
	var impls []string
	for _, hid := range helperIDs {
		calls, err := a.repo.GetCallers(hid, 1, MinConfidence)
		req.acc.sqlQueries++
		if err != nil {
			continue
		}
		for _, call := range calls {
			if call == nil || call.SourceID == "" {
				continue
			}
			line := seqFactLine(call)
			if line <= 0 {
				continue
			}
			caller, serr := a.repo.GetSymbol(call.SourceID)
			if serr != nil || caller == nil || caller.FilePath == "" {
				continue
			}
			ctx := seqFileCtxFor(req, caller.FilePath)
			if ctx == nil {
				continue
			}
			callExpr := seqCallAtLineNamed(ctx.file, ctx.fset, line, helperName)
			if callExpr == nil || len(callExpr.Args) < 2 {
				continue
			}
			if _, ok := seqRegistrationIfaceArg(ctx, callExpr.Args[1]); ok {
				// 类型断言路径：实现集合本来就可能多候选，由既有歧义逻辑处理。
				continue
			}
			implID, ok := seqConcreteRegistrationArg(ctx, pkgOfID(call.SourceID), callExpr.Args[1])
			if !ok || seen[implID] {
				continue
			}
			seen[implID] = true
			impls = append(impls, implID)
		}
	}
	if len(impls) <= 1 {
		return nil, false
	}
	sort.Strings(impls)
	return impls, true
}

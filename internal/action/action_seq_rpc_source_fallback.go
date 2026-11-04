package action

import "go/ast"
import "go/token"
import "strings"
import "github.com/schaepher/codeintel/internal/domain"

// seqMicroRegisterSourceFallback 旧索引（缺 registers_service/rpc_service
// 属性与 grpc_impl 边）的**源码回退**：按生成注册函数名
// `Register<Handler>Handler` + 真实调用边（未被调用的 helper 不是路由）+
// 注册实参的类型断言/接口 implements 证明实现类型。
//
// 返回 nil 表示没有源码证明（保持原诊断）。
func (a *Actions) seqMicroRegisterSourceFallback(req CodeSequenceRequest,
	fwd *domain.RPCForward, handler string) *seqRPCSvc {
	helperName := "Register" + handler + "Handler"
	funcs, err := a.repo.GetFunctions()
	if err != nil || len(funcs) == 0 {
		return nil
	}
	var helperIDs []domain.CanonicalID
	for _, fn := range funcs {
		if fn == nil || fn.ID == "" {
			continue
		}
		if seqDeclNameOf(string(fn.ID)) == helperName || fn.Name == helperName {
			helperIDs = append(helperIDs, fn.ID)
		}
	}
	if len(helperIDs) == 0 {
		return nil
	}
	for _, hid := range helperIDs {
		call, cerr := a.repo.GetFirstCallTo(hid)
		if cerr != nil || call == nil {
			continue
		}
		line := seqFactLine(call)
		if line <= 0 {
			continue
		}
		caller, serr := a.repo.GetSymbol(call.SourceID)
		if serr != nil || caller.FilePath == "" {
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
		// T09b：helper 声明证明（第一参 server.Server、第二参 <X>Handler、
		// 体内真实 NewHandler(&wrapper{...})）+ 生成 wrapper 的接口嵌入。
		helperFn, helperCtx, helperPkg, pok := a.seqRegisterHelperDecl(req, hid)
		if !pok {
			continue
		}
		proof, ok := seqRegisterHelperProof(helperCtx, helperFn, helperPkg)
		if !ok {
			continue
		}
		ifaceID := proof.ifaceID
		var impls []string
		if asserted, okArg := seqRegistrationIfaceArg(ctx, callExpr.Args[1]); okArg {
			// 已有类型断言路径（todo27 T09）：断言目标接口为准。
			ifaceID = asserted
			all, ierr := a.InterfaceImplements(req.Ctx, ifaceID)
			if ierr != nil || len(all) == 0 {
				continue
			}
			impls = all
		} else {
			// T09b：注册实参是**具体复合字面量**（&Handler{...}）——按实参
			// 静态类型得到实现类型，并要求它确实实现 helper 声明的接口。
			callerPkg := pkgOfID(call.SourceID)
			implID, okImpl := seqConcreteRegistrationArg(ctx, callerPkg, callExpr.Args[1])
			if !okImpl {
				continue
			}
			all, ierr := a.InterfaceImplements(req.Ctx, ifaceID)
			if ierr != nil || !seqContainsString(all, implID) {
				continue
			}
			impls = []string{implID}
			// 服务身份取生成 wrapper 类型（wire:Ledger）——与客户端声明的
			// 服务名一致，而不是把接口名当服务名。
			helperPkgPath := pkgOfID(hid)
			out := &seqRPCSvc{fullIdentity: fwd.FullIdentity, handler: handler,
				serviceID: "symbol:go:" + helperPkgPath + ":" + handler,
				reason:    "register-handler-source", registerFile: caller.FilePath, registerLine: line,
				methods: seqIfaceMethodsOf(a, req, ifaceID)}
			out.impls = impls
			return out
		}
		if len(impls) > 1 {
			if narrowed, ok := a.seqRegistrationImplNarrow(req, ctx, caller, callExpr, ifaceID, impls); ok {
				impls = narrowed
			}
		}
		out := &seqRPCSvc{fullIdentity: fwd.FullIdentity, handler: handler,
			serviceID: ifaceID, reason: "register-handler-source",
			registerFile: caller.FilePath, registerLine: line,
			methods: seqIfaceMethodsOf(a, req, ifaceID)}
		out.impls = append(out.impls, impls...)
		if len(out.impls) > 1 {
			out.stop = seqAmbiguousService("服务 " + handler + " 有多个注册实现：" +
				strings.Join(out.impls, " | "))
		}
		return out
	}
	return nil
}

// seqRegistrationIfaceArg 注册实参的类型断言/复合字面量 → 接口 canonical ID。
//   - `c.Get().(pb.AccountServiceHandler)` → 断言目标接口
//   - `pb.RegisterXxx(svc, &Impl{})` → 具体类型时用 implements 反查（返回空）
func seqRegistrationIfaceArg(ctx *seqFileCtx, arg ast.Expr) (string, bool) {
	var t ast.Expr
	switch x := arg.(type) {
	case *ast.TypeAssertExpr:
		t = x.Type
	case *ast.ParenExpr:
		return seqRegistrationIfaceArg(ctx, x.X)
	default:
		return "", false
	}
	switch ty := t.(type) {
	case *ast.Ident:
		return "symbol:go:" + pkgPathOf(seqFileRelOfCtx(ctx)) + ":" + ty.Name, false
	case *ast.SelectorExpr:
		if id, ok := ty.X.(*ast.Ident); ok {
			if p := seqImportPath(ctx.file, id.Name); p != "" {
				return "symbol:go:" + p + ":" + ty.Sel.Name, true
			}
		}
	}
	return "", false
}

// seqFileRelOfCtx 文件上下文的仓库相对路径（pkgPathOf 需要 canonical ID 形式，
// 这里用占位包路径避免误用）。
func seqFileRelOfCtx(ctx *seqFileCtx) string { return "file:" + ctx.rel }

// seqIfaceMethodsOf 接口声明的方法名（诊断用）。
func seqIfaceMethodsOf(a *Actions, req CodeSequenceRequest, ifaceID string) []string {
	n, err := a.repo.GetSymbol(domain.CanonicalID(ifaceID))
	if err != nil || n == nil || n.FilePath == "" {
		return nil
	}
	ctx := seqFileCtxFor(req, n.FilePath)
	if ctx == nil {
		return nil
	}
	name := seqDeclNameOf(ifaceID)
	for _, decl := range ctx.file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name.Name != name {
				continue
			}
			it, ok := ts.Type.(*ast.InterfaceType)
			if !ok {
				return nil
			}
			var out []string
			for _, m := range it.Methods.List {
				for _, nm := range m.Names {
					out = append(out, nm.Name)
				}
			}
			return out
		}
	}
	return nil
}

// seqCallAtLineNamed 文件内指定行、被调名匹配的 CallExpr（嵌套调用取外层）。
func seqCallAtLineNamed(f *ast.File, fset *token.FileSet, line int, name string) *ast.CallExpr {
	var hit *ast.CallExpr
	ast.Inspect(f, func(nd ast.Node) bool {
		call, ok := nd.(*ast.CallExpr)
		if !ok || fset.Position(call.Pos()).Line != line {
			return true
		}
		callee := ""
		switch fn := call.Fun.(type) {
		case *ast.Ident:
			callee = fn.Name
		case *ast.SelectorExpr:
			callee = fn.Sel.Name
		}
		if callee == name {
			hit = call
		}
		return true
	})
	return hit
}

// seqRegistrationImplNarrow 注册实参 `x.(I)` 的 x 若是本仓方法调用的结果，
// 用方法体的唯一具体返回来源收窄实现候选；无法证明时保持原候选集。
func (a *Actions) seqRegistrationImplNarrow(req CodeSequenceRequest, ctx *seqFileCtx,
	caller *domain.CodeEntity, call *ast.CallExpr, ifaceID string, impls []string) ([]string, bool) {
	arg := call.Args[1]
	for {
		p, ok := arg.(*ast.ParenExpr)
		if !ok {
			break
		}
		arg = p.X
	}
	ta, ok := arg.(*ast.TypeAssertExpr)
	if !ok {
		return nil, false
	}
	inner, ok := ta.X.(*ast.CallExpr)
	if !ok {
		return nil, false
	}
	sel, ok := inner.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil, false
	}
	recvID, ok := sel.X.(*ast.Ident)
	if !ok {
		return nil, false
	}

	pkg := pkgOfID(caller.ID)
	fn := seqEnclosingFunc(ctx.file, call.Pos())
	if fn == nil {
		return nil, false
	}
	var typeName string
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
			return true
		}
		id, ok := as.Lhs[0].(*ast.Ident)
		if !ok || id.Name != recvID.Name {
			return true
		}
		if _, t, ok2 := seqCompositeTypeName(as.Rhs[0]); ok2 {
			typeName = t
			return false
		}
		return true
	})
	if typeName == "" {
		return nil, false
	}
	methodID := "symbol:go:" + pkg + ":(" + typeName + ")." + sel.Sel.Name
	mctx, fd, ok := loadSeqFuncDecl(a, req, methodID)
	if !ok || fd == nil {
		return nil, false
	}
	srcs := funcDeclReturnTypes(a, req, mctx.file, importAliases(mctx.file), fd, 3, pkg)
	known := knownWriteSources(srcs)
	if len(known) != 1 || hasUnknownWriteSource(srcs) || a.isIfaceTypeName(req, known[0].typeName) {
		return nil, false
	}
	want := "symbol:go:" + known[0].pkgPath + ":" + known[0].typeName
	for _, impl := range impls {
		if impl == want {
			return []string{want}, true
		}
	}
	return nil, false
}

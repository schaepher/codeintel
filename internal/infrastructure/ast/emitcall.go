package ast

import (
	"go/ast"
	"go/types"

	"github.com/schaepher/codeintel/internal/domain"
)

// emitCall 调用表达式处理（原 processFile 闭包主体）：caller 上下文 →
// callee 解析（含函数值/内建 new 兜底）→ gRPC/HTTP/uses/passes_to/
// 服务入口标记/普通 calls 建边。逻辑与原闭包逐行一致。
func (ctx *fileCtx) emitCall(call *ast.CallExpr) {
	pkg := ctx.pkg
	// 调用者上下文（callee 解析失败的分支也要用）
	callerDecl := findCallerDecl(ctx.stack)
	if callerDecl == nil {
		return // 包级初始化中的调用，MVP 不建边
	}
	caller, ok := pkg.TypesInfo.Defs[callerDecl.Name].(*types.Func)
	if !ok {
		return
	}
	callerID, callerKind := ctx.a.fnIDOf(caller)
	if callerID == "" {
		return
	}
	// 函数集合嵌套调用：内建 append(list, g()...) / 变参展开
	// outer(g()...) 的 g() 返回函数集合时补 caller→g 的 CALLS 边
	// （passes_result 由 handleNestedArg 建；内建 append 到达不了那里）。
	ctx.emitFuncSetNestedCalls(call, caller, callerID, callerKind)
	// 遍历函数集合注册表并执行（for _, v := range registry { v.F() }）
	ctx.emitFuncSetTraversalCall(call, caller, callerID, callerKind)
	// 内建 new(T)：callee 解析失败（builtin 无 Pkg）时单独处理
	callee, ok := resolveCallee(pkg.TypesInfo, call.Fun)
	if !ok {
		if id, isID := call.Fun.(*ast.Ident); isID {
			// P2-1：函数值调用 f()——f 由本函数 f := g 赋值
			// （callee 是局部变量无法解析），查 varFuncs 建 calls
			if fnRef, hasF := ctx.varFuncs[id.Name]; hasF {
				if fid, fkind := ctx.a.fnIDOf(fnRef); fid != "" {
					_ = ctx.emit(domain.Item{Node: ctx.a.nodeFor(ctx.repo, pkg, caller, callerID, callerKind, ctx.serviceFlags[callerID])})
					_ = ctx.emit(domain.Item{Node: ctx.a.nodeFor(ctx.repo, pkg, fnRef, fid, fkind, nil)})
					_ = ctx.emit(domain.Item{Fact: &domain.Fact{
						SourceID:   callerID,
						TargetID:   fid,
						Kind:       domain.FactCalls,
						ToolSource: domain.ToolCodeGraph,
						Confidence: 0.8,
						Metadata:   map[string]any{"line_num": pkg.Fset.PositionFor(call.Pos(), false).Line, "pos": pkg.Fset.PositionFor(call.Lparen, false).Offset},
					}})
				}
			}
			if id.Name == "new" && len(call.Args) == 1 {
				ctx.a.createObject(pkg, call, ctx.stack, ctx.emit, ctx.repo, ctx.objCache)
			}
		}
		return
	}
	// 参数位置的调用（如 A(B(C())) 里的 B(C())）：由外层调用处理为
	// "持有返回参数"链，不建 calls
	if isArgCall(ctx.stack, call) {
		// R36：参数位置的外部依赖调用仍识别（不建 calls 但建外部边——
		// redis.Values(conn.Do(...)) 嵌套形态，go2o 实测）
		if sel, isSel := call.Fun.(*ast.SelectorExpr); isSel {
			if xid, isID := sel.X.(*ast.Ident); isID {
				ctx.emitRedisCall(call, callee, sel, xid, callerID)
				ctx.emitKafkaCall(call, callee, sel, xid, callerID)
			}
		}
		return
	}

	// 对象使用处：x.Method()（x 是初始化的对象）→ uses 边（对象 → 方法）
	if sel, isSel := call.Fun.(*ast.SelectorExpr); isSel {
		if xid, isID := sel.X.(*ast.Ident); isID {
			ctx.emitSelectorCall(call, callee, sel, xid, callerID)
		} else if _, isCall := sel.X.(*ast.CallExpr); isCall {
			// R31：gin 链式（r.Group("/v1").GET(...)）——组前缀继承
			ctx.emitGinChainedCall(call, sel, callee)
		}
	}
	// 参数位置的嵌套调用：接收者持有返回参数（A(B(C)) → A→B、B→C）。
	// P2-2：外部接收者（如 fmt.Errorf("%v", joinIDs(x))）也处理——
	// 否则 joinIDs 无入边被 unused 误报。
	if callee.Pkg() != nil {
		calleeID, _ := ctx.a.fnIDOf(callee)
		for i, arg := range call.Args {
			if inner, isCall := arg.(*ast.CallExpr); isCall {
				// Q185：argName = 接收者签名第 i 个参数名（信息栏
				// "实参来源"分组标注具体是哪个实参）
				argName := ""
				if sig, ok := callee.Type().(*types.Signature); ok && i < sig.Params().Len() {
					argName = sig.Params().At(i).Name()
				}
				ctx.a.handleNestedArg(pkg, inner, calleeID, i, argName, ctx.emit, ctx.repo)
			}
		}
	}
	// 对象去处：f(x) / f(&T{}) → passes_to 边（对象 → 接收函数）
	if callee.Pkg() != nil && isInModule(callee.Pkg().Path(), ctx.repo.Modules) {
		calleeID, calleeKind := ctx.a.fnIDOf(callee)
		for _, arg := range call.Args {
			var objID domain.CanonicalID
			var ok2 bool
			if xid, isID := arg.(*ast.Ident); isID {
				objID, ok2 = ctx.objVars[xid.Name]
			}
			if !ok2 {
				objID, ok2 = ctx.a.createObject(pkg, arg, ctx.stack, ctx.emit, ctx.repo, ctx.objCache)
			}
			if ok2 && calleeID != "" && objID != calleeID {
				_ = ctx.emit(domain.Item{Node: ctx.a.nodeFor(ctx.repo, pkg, callee, calleeID, calleeKind, nil)})
				_ = ctx.emit(domain.Item{Fact: &domain.Fact{
					// 方向：接收函数 → 参数（用户确认：接收者指向参数）
					SourceID:   calleeID,
					TargetID:   objID,
					Kind:       domain.FactPassesTo,
					ToolSource: domain.ToolCodeGraph,
					Confidence: 0.8,
				}})
			}
		}
	}
	// 服务入口标记：调用 net/http / grpc 包的函数作为顶层入口。
	// 注意：外部调用点随后会直接 return（不建 CALLS 边），因此标记
	// fires 时必须立即 emit 节点，否则节点永远不会带上标记。
	if callee.Pkg() != nil {
		ctx.markServiceEntry(call, callee, caller, callerID, callerKind)
	}
	calleeID, calleeKind := ctx.a.fnIDOf(callee)
	// 函数集合注册表：callee 是"函数集合入参 + 函数集合返回"的 getter 时，
	// 为 callee → 实参里注册的每个函数补 CALLS 边（默认可见；不按签名
	// 猜测全部候选函数）。
	if callee.Pkg() != nil && isInModule(callee.Pkg().Path(), ctx.repo.Modules) {
		ctx.emitFuncSetRegistry(call, caller, callerID, callerKind, callee, calleeID, calleeKind)
	}
	// 函数作为参数传入（回调）：参数函数 → 接收函数（passes_to）。
	// 接收者可为外部框架函数（如 net/http.HandleFunc），为其建轻量节点
	// （file_path 为空），使"作为谁的参数"关系可见。须在外部函数
	// 跳过逻辑之前处理。
	// externalCallee：接收函数是外部包函数时，补调用者 → 接收函数的
	// calls 边——"允许展开一层外部包"：从调用链（如 New）展开即可见
	// 外部接收者（HandleFunc），进而展开它的持有参数关系。仅限"函数
	// 作为参数"场景（普通外部调用如 fmt.Println 不建边，避免图爆炸）。
	externalCallee := callee.Pkg() != nil && !isInModule(callee.Pkg().Path(), ctx.repo.Modules)
	if calleeID != "" && calleeID != callerID {
		for _, arg := range call.Args {
			fn := argFuncRef(pkg, arg)
			if fn == nil || fn.Pkg() == nil || !isInModule(fn.Pkg().Path(), ctx.repo.Modules) {
				continue // 参数函数必须是项目内符号（有节点）
			}
			paramID, paramKind := ctx.a.fnIDOf(fn)
			if paramID == "" || paramID == calleeID {
				continue
			}
			_ = ctx.emit(domain.Item{Node: ctx.a.nodeFor(ctx.repo, pkg, fn, paramID, paramKind, nil)})
			_ = ctx.emit(domain.Item{Node: ctx.a.nodeFor(ctx.repo, pkg, callee, calleeID, calleeKind, nil)})
			_ = ctx.emit(domain.Item{Fact: &domain.Fact{
				// 方向：接收函数 → 参数函数（用户确认：run -参数→ callback）
				SourceID:   calleeID,
				TargetID:   paramID,
				Kind:       domain.FactPassesTo,
				ToolSource: domain.ToolCodeGraph,
				Confidence: 0.8,
			}})
			// R18：方法值实参（x.Method，如 ast.Inspect(f, ctx.visit)）——
			// 调用者确实调用了该方法（作为回调交给接收者执行），建
			// calls 边（实体图/调用链可见；fileCtx 无入边问题的根因）。
			// 普通函数回调不建（保持 unused 语义——回调函数由接收者
			// 调用，参数传递关系 passes_to 已表达）
			if _, isSel := arg.(*ast.SelectorExpr); isSel {
				_ = ctx.emit(domain.Item{Fact: &domain.Fact{
					SourceID:   callerID,
					TargetID:   paramID,
					Kind:       domain.FactCalls,
					ToolSource: domain.ToolCodeGraph,
					Confidence: 0.8,
					Metadata: map[string]any{
						"line_num": pkg.Fset.PositionFor(call.Pos(), false).Line, "pos": pkg.Fset.PositionFor(call.Lparen, false).Offset,
					},
				}})
			}
			if externalCallee {
				_ = ctx.emit(domain.Item{Fact: &domain.Fact{
					// 调用者 → 外部接收函数：允许展开一层外部包
					SourceID:   callerID,
					TargetID:   calleeID,
					Kind:       domain.FactCalls,
					ToolSource: domain.ToolCodeGraph,
					Confidence: 0.8,
				}})
			}
		}
	}
	if callee.Pkg() == nil {
		return // 内建（new/len 等）不建边
	}
	// R99-2（用户）：第三方依赖包调用建边——方法/接口方法（业务集成
	// 点：c.Get()、ext.NewService().DoSth()——轻量节点 file_path 空，
	// 不深入解析：外部包无 Syntax，内部调用天然不展开，时序图
	// codeSeqForSymbol 对 FilePath 空返回 nil 不深入）；纯包函数
	// （fmt.Println 等）不建——防图爆炸（旧设计保留）
	if !isInModule(callee.Pkg().Path(), ctx.repo.Modules) {
		sig, _ := callee.Type().(*types.Signature)
		if sig == nil || sig.Recv() == nil {
			return // 外部纯函数（无 receiver）不建边
		}
	}
	if calleeID == "" || calleeID == callerID {
		return
	}
	if isInterfaceMethod(callee) {
		// 接口方法不作为独立节点（用户确认）。但链式调用场景
		// （NewService().DoSth()）仍要建调用边：静态分析接收者
		// 表达式的实际类型——NewService 返回接口但 return 具体
		// 类型 → 指向具体类型的实现方法；无法确定 → 指向接口类型
		_ = ctx.emit(domain.Item{Node: ctx.a.nodeFor(ctx.repo, pkg, caller, callerID, callerKind, ctx.serviceFlags[callerID])})
		targetID, _, targetNode := ctx.a.concreteMethodFor(pkg, call, callee, ctx.repo)
		if targetID == "" {
			return
		}
		if targetNode != nil {
			_ = ctx.emit(domain.Item{Node: targetNode})
		}
		_ = ctx.emit(domain.Item{Fact: &domain.Fact{
			SourceID:   callerID,
			TargetID:   targetID,
			Kind:       domain.FactCalls,
			ToolSource: domain.ToolCodeGraph,
			Confidence: 0.8,
			Metadata:   map[string]any{"line_num": pkg.Fset.PositionFor(call.Pos(), false).Line, "pos": pkg.Fset.PositionFor(call.Lparen, false).Offset},
		}})
		return
	}
	// 保障两端节点存在（INSERT OR IGNORE，不覆盖 SCIP 的完整节点）
	if err := ctx.emit(domain.Item{Node: ctx.a.nodeFor(ctx.repo, pkg, caller, callerID, callerKind, ctx.serviceFlags[callerID])}); err != nil {
		return
	}
	if err := ctx.emit(domain.Item{Node: ctx.a.nodeFor(ctx.repo, pkg, callee, calleeID, calleeKind, nil)}); err != nil {
		return
	}
	_ = ctx.emit(domain.Item{Fact: &domain.Fact{
		SourceID:   callerID,
		TargetID:   calleeID,
		Kind:       domain.FactCalls,
		ToolSource: domain.ToolCodeGraph,
		Confidence: 0.8,
		Metadata:   map[string]any{"line_num": pkg.Fset.PositionFor(call.Pos(), false).Line, "pos": pkg.Fset.PositionFor(call.Lparen, false).Offset},
	}})
}

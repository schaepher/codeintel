package action

// todo22 §2.3/§2.4：工厂返回函数值 + 函数列表预算。
//
// 「工厂」= 任何被调用的函数（具名函数/局部函数值/字面量）：它的 return 表达式
// 决定调用结果是什么函数值。解析只按**准确符号 + return 语句**，不从函数名
// 猜返回类型；多条 return（条件返回）保留为未消歧的多候选。
//
// 工厂本身的副作用由调用节点的正常展开体现（与"返回函数被调用"分开）。

import (
	"go/ast"
	"go/token"
)

// 预算（todo22 §2.4：路径 ≤16、每条 ≤32 项）。
const (
	seqMaxListItems  = 32
	seqMaxRangeIters = 16
	// T02：列表路径上限（转发层数）。
	seqMaxListPath     = 16
	seqMaxFactoryDepth = 3
)

// seqCalleeReturnValues 解析一次调用的返回值：函数值候选 + 是否解析成功。
func seqCalleeReturnValues(a *Actions, req CodeSequenceRequest, ctx *seqFileCtx,
	call *ast.CallExpr, depth int) ([]seqValue, bool) {
	// ① 被调者本身是函数值（局部变量/形参/字面量）——按值的身份解析
	if fv, ok := seqCallFuncValue(a, req, call); ok {
		return seqValueReturnValues(a, req, ctx, call, fv, depth)
	}
	// ② 包级函数：AST 对象身份/import 路径 → 准确符号
	curPkg := pkgPathOf(req.curSym)
	symID, ok := seqASTCallTarget(call.Fun, ctx.file, curPkg)
	if !ok || !seqTargetIndexed(req, curPkg, symID) {
		// T10：外部包函数（本仓未索引）——按外部源码 return 表达式解析
		// 闭包/具名回调（创建处上下文 = 外部包文件）。
		return seqExternalReturnValues(a, req, ctx, call, depth)
	}
	fileCtx, fn, ok := loadSeqFuncDecl(a, req, symID)
	if !ok {
		return nil, false
	}
	env := newSeqEnv(nil)
	bindSeqParams(env, fn.Type.Params, captureSeqClosureArgs(a, req, ctx, call))
	// T10：工厂返回的闭包按**创建处**（被调函数）身份捕获——否则字面量里的
	// 同包调用会用调用者的包路径（跨模块时产生 index_gap）。
	sub := req
	sub.curSym = symID
	return seqReturnValuesOf(a, sub, fileCtx, fn, env, call, depth)
}

// seqValueReturnValues 函数值（字面量/具名/列表/多候选）的返回候选。
func seqValueReturnValues(a *Actions, req CodeSequenceRequest, ctx *seqFileCtx,
	call *ast.CallExpr, fv seqValue, depth int) ([]seqValue, bool) {
	switch fv.kind {
	case seqValueLit:
		if fv.lit == nil || fv.lit.Body == nil {
			return nil, false
		}
		env := newSeqEnv(fv.env)
		bindSeqParams(env, fv.lit.Type.Params, captureSeqClosureArgs(a, req, ctx, call))
		sub := req
		sub.curSym = fv.enclosing
		sub.env = env
		return seqReturnValuesOf(a, sub, fv.ctx, &ast.FuncDecl{Type: fv.lit.Type, Body: fv.lit.Body}, env, call, depth)
	case seqValueNamed:
		fileCtx, fn, ok := loadSeqFuncDecl(a, req, fv.symID)
		if !ok {
			return nil, false
		}
		env := newSeqEnv(nil)
		bindSeqParams(env, fn.Type.Params, captureSeqClosureArgs(a, req, ctx, call))
		sub := req
		sub.curSym = fv.symID
		sub.env = env
		return seqReturnValuesOf(a, sub, fileCtx, fn, env, call, depth)
	case seqValueMulti:
		// 条件返回的多候选（上一次折叠的结果）——逐候选解析返回值
		var out []seqValue
		okAny := false
		for _, cand := range fv.cands {
			vals, ok := seqValueReturnValues(a, req, ctx, call, cand, depth)
			if ok {
				okAny = true
				out = append(out, vals...)
			}
		}
		return out, okAny
	}
	return nil, false
}

// seqReturnValuesOf 收集函数体（不含嵌套字面量体）里每个 return 表达式的
// 函数值——条件返回 → 多候选（不任选一条）。

// seqReturnGuard 工厂返回的到达条件（if cond body 上的 return → want=true）。

// seqReturnGuardsAllow 路径上全部守卫在已知实参下都可满足 → 该 return 可达。
// 任一守卫被排除 → 不可达；未知 → 保守保留（可达）。

// seqCalleeArgLiterals 被调工厂的形参 → 调用点实参字面量（只认可静态求值
// 的实参；未知保持缺失 = 该守卫不裁剪）。

// loadSeqFuncDecl 按 canonical ID 读源码并定位函数声明（名字 + receiver 精确
// 匹配；不按可能陈旧的行号切片）。

// seqFileCtxFor 取（并缓存）仓库文件的解析上下文。

// seqDeclNameOf canonical ID → 声明名（`(T).m` / `Func`）。

// seqTargetsCached 按函数缓存"调用点 → 目标"映射（字面量内联展开会反复用到
// 同一个外层函数的映射）。

// seqRangeElems range 的对象是否是已知函数列表（统一走 captureSeqCallbackLists
// ——只认确定的列表；多候选不套用"按顺序展开"）。
func seqRangeElems(a *Actions, req CodeSequenceRequest, x ast.Expr) (seqValue, bool) {
	if req.env == nil {
		return seqValue{}, false
	}
	v, ok := captureSeqCallbackLists(a, req, req.curCtx, x)
	if !ok {
		return seqValue{}, false
	}
	// todo24 §11.5：只有**函数值列表**才按元素迭代展开。`for _, step := range
	// steps`（普通数据列表）不得当成函数列表逐项展开——否则会把重试间隔画成
	// 必然执行的多次调用，还会清掉循环内只读回调的既有来源。
	if !seqListHoldsFuncValues(a, req, x, v) {
		return seqValue{}, false
	}
	if v.kind == seqValueMulti {
		// 多候选列表走 seqRangeMultiListsValue（公共前缀 + 互斥尾部）。
		return seqValue{}, false
	}
	return v, true
}

// seqListHoldsFuncValues 列元素是否函数值（空列表必须由声明类型证明）。
// T02：多候选列表（条件分支 append）按每个候选列表分别判定。
func seqListHoldsFuncValues(a *Actions, req CodeSequenceRequest, x ast.Expr, v seqValue) bool {
	if v.kind == seqValueMulti {
		for _, c := range v.cands {
			if c.kind != seqValueList || !seqListHoldsFuncValues(a, req, x, c) {
				return false
			}
		}
		return len(v.cands) > 0
	}
	for _, e := range v.elems {
		switch e.kind {
		case seqValueLit, seqValueNamed, seqValueMulti, seqValueList:
		default:
			return false
		}
	}
	if len(v.elems) == 0 {
		// 空列表：声明类型是函数类型或 []func(...)（nil slice = 确定空列表）
		if seqExprFuncTypedIn(a, req, req.curCtx, x) {
			return true
		}
		if id, ok := x.(*ast.Ident); ok && id.Obj != nil {
			switch d := id.Obj.Decl.(type) {
			case *ast.ValueSpec:
				return d.Type != nil && seqEmptyFuncSliceTypeIn(a, req, req.curCtx, d.Type)
			case *ast.Field:
				return seqEmptyFuncSliceTypeIn(a, req, req.curCtx, d.Type)
			}
		}
		return false
	}
	return true
}

// seqRangeOverFuncList 按列表元素顺序展开循环体（每次迭代绑定元素值；
// 循环体里的赋值对下一次迭代生效 = 折叠）。超过迭代预算 → 明确诊断，
// 不截取前几项冒充完整列表。
func seqRangeOverFuncList(a *Actions, req CodeSequenceRequest, fset *token.FileSet, src []byte,
	s *ast.RangeStmt, list seqValue, tgts *seqTargets, depth int, recvType string,
	recvDecl map[string]string, f *ast.File, curPkg, curFile string) *CodeSeqNode {
	node := &CodeSeqNode{Kind: "loop", Label: "range " + seqExprText(fset, src, s.X),
		Line: fset.Position(s.Pos()).Line}
	if list.why != "" {
		node.Stop = &SeqStop{Code: SeqStopFuncValueUnbound, Detail: list.why}
		return node
	}
	elems := list.elems
	if list.listPath > seqMaxListPath {
		node.Stop = &SeqStop{Code: SeqStopFuncValueUnbound,
			Detail: "函数列表路径超过预算 " + itoaSeq(seqMaxListPath) + "（T02：转发层数不截断冒充完整）"}
		return node
	}
	if len(elems) > seqMaxListItems {
		node.Stop = &SeqStop{Code: SeqStopFuncValueUnbound,
			Detail: "函数列表元素超过预算 " + itoaSeq(seqMaxListItems) + "（T02：不截取前几项冒充完整）"}
		return node
	}
	if len(elems) == 0 {
		// T02：确定的空列表 → 零元素执行（不生成任何循环体节点）。
		return node
	}
	// T02：函数列表按**元素实际调用**展开——每项一个互斥的"item i/N"循环
	// （顺序来自列表字面量），整体标"callback list path"（taken）。
	branch := seqListItemsNode(a, req, fset, src, s, list, tgts, depth, recvType, recvDecl, f, curPkg, curFile)
	if branch != nil {
		node.Nodes = append(node.Nodes, branch)
	}
	return node
}

// seqListItemsNode 一个已证明列表 → "callback list path" 分支（每元素一个
// item 循环）。空列表返回 nil（确定零元素执行）。
func seqListItemsNode(a *Actions, req CodeSequenceRequest, fset *token.FileSet, src []byte,
	s *ast.RangeStmt, list seqValue, tgts *seqTargets, depth int, recvType string,
	recvDecl map[string]string, f *ast.File, curPkg, curFile string) *CodeSeqNode {
	elems := list.elems
	if list.why != "" || len(elems) == 0 || list.listPath > seqMaxListPath || len(elems) > seqMaxListItems {
		return nil
	}
	sub := req
	sub.inCallbackList = true
	branch := &CodeSeqNode{Kind: "branch", Label: "callback list path",
		Line:  fset.Position(s.Pos()).Line,
		Scene: &SceneFit{Status: SceneTaken, Reason: "source-callback-list"}}
	for i, elem := range elems {
		if sub.env != nil {
			if id, ok := s.Value.(*ast.Ident); ok && id.Obj != nil {
				sub.env.set(id.Obj, elem)
			}
			if id, ok := s.Key.(*ast.Ident); ok && id.Obj != nil {
				sub.env.set(id.Obj, unknownSeqValue("range 索引（与函数值执行无关）"))
			}
		}
		item := &CodeSeqNode{Kind: "loop",
			Label: "item " + itoaSeq(i+1) + "/" + itoaSeq(len(elems)),
			Line:  fset.Position(s.Pos()).Line}
		item.Nodes = walkStmts(a, sub, fset, src, s.Body.List, tgts,
			depth, recvType, recvDecl, f, curPkg, curFile)
		branch.Nodes = append(branch.Nodes, item)
	}
	return branch
}

// seqStmtsTerminate 语句列表是否必然终止（return / panic；if-else 两支都
// 终止）。用于 guard 子句后的可达性（后续 return 受 !cond 约束）。

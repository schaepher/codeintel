package action

// todo22 §2（第一增量）：函数值调用的展开。
//
// 触发展开的三种情形（都被证明"在这个调用点执行"）：
//   1. IIFE：`func(){…}()`——调用处直接是字面量（literal）
//   2. 函数变量/形参：`f()` / `process(inner)`——按调用点的到达定义取值
//      （var），形参值来自调用实参绑定（arg → callback）
//   3. 局部变量指向包级函数：`f := Second; f()`——from var
//
// 不能证明的三种情形都不展开内部执行，但**保留节点并给出原因**：
//   未绑定/被未知调用改写（unknown）、条件/循环下多值未消歧（multi）、
//   参数在外部调用里逃逸（调用节点的 Stop.Detail 标注）。

import (
	"go/ast"
)

// seqCallFuncValue 判断被调者本身是否是函数值（字面量/变量/形参）。
// ok=false 表示按索引调用边/接口派发等既有路径处理（包级函数的直接调用）。
func seqCallFuncValue(a *Actions, req CodeSequenceRequest, call *ast.CallExpr) (seqValue, bool) {
	switch f := call.Fun.(type) {
	case *ast.FuncLit:
		v := captureSeqClosureValue(a, req, req.curCtx, f)
		v.origin = seqOriginLiteral
		return v, true
	case *ast.Ident:
		if req.env != nil {
			if v, ok := req.env.lookup(f.Obj); ok {
				return v, true
			}
		}
	}
	return seqValue{}, false
}

// seqFillFuncValueCall 把函数值调用的展开结果写进调用节点（node 已含
// label/actor/line）。
func seqFillFuncValueCall(a *Actions, req CodeSequenceRequest, call *ast.CallExpr,
	fv seqValue, node *CodeSeqNode, depth int) {
	switch fv.kind {
	case seqValueUnknown:
		node.Evidence = &SeqEvidence{Source: SeqEvidenceClosure, CallFile: req.curFile,
			CallLine: node.Line, Reason: seqReasonUnknown, Ambiguous: true}
		node.Stop = &SeqStop{Code: SeqStopFuncValueUnbound, Detail: fv.why}
		return
	case seqValueMulti:
		// 条件/循环下多个值都可能到达：分别展开每个候选，整体标未消歧
		// （不把其中任何一个当唯一结论）。
		node.Evidence = &SeqEvidence{Source: SeqEvidenceClosure, CallFile: req.curFile,
			CallLine: node.Line, Reason: seqReasonMulti, Ambiguous: true,
			Candidates: seqValueCandidates(fv)}
		for _, cand := range fv.cands {
			child := &CodeSeqNode{Kind: "call", Label: cand.label(), Actor: seqValueActor(cand), Line: node.Line}
			seqExpandSingleValue(a, req, call, cand, child, depth)
			node.Nodes = append(node.Nodes, child)
		}
		if len(node.Nodes) == 0 {
			node.Stop = &SeqStop{Code: SeqStopFuncValueUnbound, Detail: fv.why}
		}
		return
	case seqValueList:
		// 函数列表：按列表顺序逐项展开（顺序由列表与 range 决定，不是猜）
		node.Evidence = &SeqEvidence{Source: SeqEvidenceClosure, CallFile: req.curFile,
			CallLine: node.Line, Reason: seqReasonList, Candidates: seqValueCandidates(fv)}
		if fv.why != "" {
			node.Stop = &SeqStop{Code: SeqStopFuncValueUnbound, Detail: fv.why}
			node.Evidence.Ambiguous = true
			return
		}
		for _, elem := range fv.elems {
			child := &CodeSeqNode{Kind: "call", Label: elem.label(), Actor: seqValueActor(elem), Line: node.Line}
			seqExpandSingleValue(a, req, call, elem, child, depth)
			node.Nodes = append(node.Nodes, child)
		}
		if len(node.Nodes) == 0 {
			node.Stop = &SeqStop{Code: SeqStopNoCalls, Detail: "空函数列表（确定无元素执行）"}
		}
	default:
		seqExpandSingleValue(a, req, call, fv, node, depth)
	}
}

// 展开证据 reason（origin → 可读依据）。
const (
	seqReasonLiteral = "iife"               // 调用处直接是字面量
	seqReasonArg     = "callback-binding"   // 形参值来自调用实参（已绑定）
	seqReasonVar     = "func-value-var"     // 局部变量/包级函数的到达定义
	seqReasonUnknown = "func-value-unknown" // 调用点无法确定回调值
	seqReasonMulti   = "func-value-multi"   // 多值未消歧
	seqReasonList    = "func-list"          // 函数列表逐项执行
)

func seqReasonForOrigin(origin string) string {
	switch origin {
	case seqOriginLiteral:
		return seqReasonLiteral
	case seqOriginArg:
		return seqReasonArg
	}
	return seqReasonVar
}

// seqExpandSingleValue 展开**一个**函数值：字面量走内联遍历，具名函数走
// codeSeqForSymbol（两者都把本次调用的实参绑定到被展开函数的形参）。
func seqExpandSingleValue(a *Actions, req CodeSequenceRequest, call *ast.CallExpr,
	fv seqValue, node *CodeSeqNode, depth int) {
	reason := seqReasonForOrigin(fv.origin)
	// 本次调用的实参值（按位置）——被展开函数的形参绑定。
	args := captureSeqClosureArgs(a, req, req.curCtx, call)
	switch fv.kind {
	case seqValueLit:
		node.Evidence = &SeqEvidence{Source: SeqEvidenceClosure, CallFile: req.curFile,
			CallLine: node.Line, Reason: reason + "-literal"}
		if depth <= 1 {
			req.acc.depth = true
			node.Stop = &SeqStop{Code: SeqStopDepthLimit,
				Detail: "depth 上限（--depth " + itoaSeq(req.Depth) + "），未展开该回调内部"}
			return
		}
		if fv.ctx == nil || fv.lit == nil || fv.lit.Body == nil {
			node.Stop = &SeqStop{Code: SeqStopFuncValueUnbound, Detail: "函数字面量缺失源码上下文"}
			return
		}
		key := seqLitKey(fv)
		if key != "" {
			if req.litVisited[key] {
				node.Stop = &SeqStop{Code: SeqStopCycle, Detail: "该函数字面量已在展开路径上（防环）"}
				return
			}
			req.litVisited[key] = true
			defer delete(req.litVisited, key)
		}
		sub := req
		sub.env = newSeqEnv(fv.env) // 闭包捕获：父环境 = 创建时的环境
		bindSeqParams(sub.env, fv.lit.Type.Params, args)
		// 字面量内的调用边/包身份归属 = **字面量所在函数**（不是把它内联展开的
		// 调用者——闭包经包级函数/工厂回调后可能在任何包里执行；DI 注册表、
		// 同包符号解析都以它为准）。
		sub.curSym = fv.enclosing
		if sub.curSym == "" {
			sub.curSym = req.curSym
		}
		sub.curTgts = seqTargetsCached(a, req, fv.enclosing)
		sub.curCtx = fv.ctx
		// 字面量的**源码文件**是它自己的（不是当前展开到的调用者——闭包可能
		// 在另一个文件/包里创建，经包级函数回调后在别处被调用）。DI 注册表
		// 扫描、证据 call_file、同包常量都依赖这个口径。
		sub.curFile = fv.ctx.rel
		// todo23 §2.5：进入函数字面量 = 入站回调上下文变更（防环键用它，
		// 同一 executor 经不同回调路径可继续展开）。
		sub.callbackCtx = seqCallbackVisitCtx(fv)
		sub.curRecvType = fv.recvType
		sub.curRecvDecl = fv.recvDecl
		sub.sceneConsts = seqConstValues(a, req, fv.ctx.rel)
		// todo22 P1：字面量形参的结构体字段值（捕获变量条件仍在 sub.sceneArgs，
		// 这里只**追加**本次调用的字段绑定，不覆盖捕获环境）。
		sub.sceneArgs = copyStrMap(req.sceneArgs)
		sub.sceneSrc = copyStrMap(req.sceneSrc)
		sub.sceneWhy = copyStrMap(req.sceneWhy)
		applySceneFieldArgs(sub.sceneArgs, sub.sceneSrc, sub.sceneWhy, fv.lit.Type.Params, req.callArgFields)
		sub.paramValues = nil
		node.Nodes = walkStmts(a, sub, fv.ctx.fset, fv.ctx.src, fv.lit.Body.List,
			sub.curTgts, depth, fv.recvType, fv.recvDecl, fv.ctx.file, fv.pkgPath, fv.ctx.rel)
		if len(node.Nodes) == 0 {
			node.Stop = &SeqStop{Code: SeqStopNoCalls}
		}
	case seqValueNamed:
		node.Evidence = &SeqEvidence{Source: SeqEvidenceClosure, CallFile: req.curFile,
			CallLine: node.Line, SymbolID: fv.symID, Reason: reason + "-named"}
		if depth <= 1 {
			req.acc.depth = true
			node.Stop = &SeqStop{Code: SeqStopDepthLimit,
				Detail: "depth 上限（--depth " + itoaSeq(req.Depth) + "），未展开该调用内部"}
			return
		}
		sub := req
		sub.paramValues = args
		// 具名函数值：入站回调上下文 = 该函数值身份（含绑定站点，区分不同
		// 实参/不同闭包实例）。
		sub.callbackCtx = seqCallbackVisitCtx(fv)
		if child, stop := codeSeqForSymbol(a, sub, fv.symID, depth-1); child != nil {
			node.Nodes, node.Stop = child.Nodes, child.Stop
		} else if stop != nil {
			node.Stop = stop
		}
	default:
		node.Evidence = &SeqEvidence{Source: SeqEvidenceClosure, CallFile: req.curFile,
			CallLine: node.Line, Reason: seqReasonUnknown, Ambiguous: true}
		node.Stop = &SeqStop{Code: SeqStopFuncValueUnbound, Detail: "函数值未解析"}
	}
}

// seqFuncArgEscape 判断调用是否让函数值参数逃逸（传给未解析/外部目标）：
// 逃逸后无法确定是否、何时被调用——在调用节点标注原因，不展开内部执行。
func seqFuncArgEscape(a *Actions, req CodeSequenceRequest, call *ast.CallExpr) string {
	if req.env == nil || call == nil {
		return ""
	}
	for _, arg := range call.Args {
		v := captureSeqClosureValue(a, req, req.curCtx, arg)
		switch v.kind {
		case seqValueLit, seqValueNamed, seqValueMulti:
			return "函数值参数在未解析调用中逃逸（无法确定是否/何时被调用）: " + v.label()
		}
		// 值未知但**声明为**函数类型：同样是函数值逃逸（不能因为不知道是
		// 哪个回调就当作普通参数）。
		if seqExprFuncTyped(arg) {
			name := seqExprText(req.curCtx.fset, req.curCtx.src, arg)
			return "函数值参数 " + name + " 在未解析调用中逃逸（无法确定是否/何时被调用）"
		}
	}
	return ""
}

// seqArgCallNodes 调用实参里的调用节点（Go 求值顺序：先求实参，再调用）。
// 不穿透函数字面量——字面量只建立函数值，其体内的调用不在此时执行。
func seqArgCallNodes(callNode func(*ast.CallExpr) *CodeSeqNode, call *ast.CallExpr) []*CodeSeqNode {
	var out []*CodeSeqNode
	var walkExpr func(ast.Expr)
	walkExpr = func(e ast.Expr) {
		if e == nil {
			return
		}
		switch x := e.(type) {
		case *ast.FuncLit:
			return // 函数字面量：只建值，不执行体内调用
		case *ast.CallExpr:
			for _, a := range x.Args {
				walkExpr(a)
			}
			walkExpr(x.Fun) // 链式调用 a().b()：receiver 调用先求值
			if n := callNode(x); n != nil {
				out = append(out, n)
			}
		default:
			ast.Inspect(e, func(n ast.Node) bool {
				switch nn := n.(type) {
				case *ast.FuncLit:
					return false
				case *ast.CallExpr:
					for _, a := range nn.Args {
						walkExpr(a)
					}
					walkExpr(nn.Fun)
					if nd := callNode(nn); nd != nil {
						out = append(out, nd)
					}
					return false
				}
				return true
			})
		}
	}
	for _, arg := range call.Args {
		walkExpr(arg)
	}
	return out
}

// seqValueActor 合成节点的参与者名（多候选/列表逐项执行时要能区分是哪个
// 函数值，不能渲染成空 actor "->"）。
func seqValueActor(v seqValue) string {
	switch v.kind {
	case seqValueNamed:
		return seqValueShortName(v.symID)
	case seqValueLit:
		return "callback"
	case seqValueList:
		return "list"
	}
	return "unknown"
}

// seqVisitKey 防环键：repo + 符号 + 入站回调上下文（todo23 §2.5）。
// 不能只用符号 ID（同一函数经不同回调路径应可继续展开），也不能用剩余
// 深度/每层新对象地址（会制造假身份、失去防环）。
func seqVisitKey(req CodeSequenceRequest, symID string) string {
	return req.RepoAbs + "|" + symID + "|" + req.callbackCtx
}

// seqCallbackVisitCtx 函数值的词法身份 + 绑定站点（回调上下文）。
func seqCallbackVisitCtx(fv seqValue) string {
	base := ""
	switch fv.kind {
	case seqValueLit:
		base = "lit:" + seqLitKey(fv)
	case seqValueNamed:
		base = "fn:" + fv.symID
	default:
		return ""
	}
	if fv.bindFile != "" {
		base += "@" + fv.bindFile + ":" + itoaSeq(fv.bindLine)
	}
	if fv.declObj != nil && fv.declObj.Name != "" {
		base += "#" + fv.declObj.Name
	}
	return base
}

package action

import (
	"go/ast"
	"go/token"
	"strings"
)

// walkStmts 遍历语句列表生成步骤（调用语句 / 分支 / 循环 / 块递归）。
// f = 当前函数所在文件（局部变量 DI 注入数据流查询用）；curPkg = 当前
// 函数包路径（P0-6）。tgts = 调用点 → 被调符号映射（R99-3：offset
// 优先——同行多调用区分）。
func walkStmts(a *Actions, req CodeSequenceRequest, fset *token.FileSet, src []byte, stmts []ast.Stmt, tgts *seqTargets, depth int, recvType string, recvDecl map[string]string, f *ast.File, curPkg, curFile string) []*CodeSeqNode {
	var out []*CodeSeqNode
	for _, st := range stmts {
		if req.acc != nil {
			req.acc.stmtSeq++
		}
		// todo10-9：逐语句检查取消——及时返回，不留"半条链"当成功
		if req.ctx().Err() != nil {
			req.acc.cancel = true
			return out
		}
		// U04c：先标记本语句的取址/闭包写者（持续逃逸），再展开语句。
		seqMarkScalarEscapes(req, st)
		seq := walkStmt(a, req, fset, src, st, tgts, depth, recvType, recvDecl, f, curPkg, curFile)
		out = append(out, seq...)
		// U04e：语句节点齐备后补齐 Scan 证据并绑定目标列（chained 调用的
		// 接收者节点此时已登记）。
		seqAnnotateScans(req, seq)
		// U04c：赋值/声明/incdec/range 的标量来源（RHS 读旧值，LHS 从下一
		// 条语句起生效）。
		seqRecordScalarStatement(req, st)
		// U04h：`_,err := 写入调用` 的写入错误结果绑定（覆盖/逃逸后失效）。
		seqRecordWriteResults(req, st)
		// U04x：`*dst = <expr>` 整值复制的构造祖先（先固定 RHS 再更新 LHS）。
		seqRecordDerefCopies(req, st)
		// U04k：`if err != nil { return err }` 之后当前路径 err==nil——后续
		// nil return 可用于错误槽等价；未受守卫的 nil 路径保持未知。
		if ifStmt, ok := st.(*ast.IfStmt); ok {
			seqRecordErrNilGuard(req, ifStmt)
		}
		// T12b：语句结束即同步续查本语句登记的已证明前沿，再回填调用结果；
		// 后续语句因此能看到已绑定值（不重放调用方语句）。
		seqContinueAtStatement(a, req)
		// todo10-2：return 终止当前路径——其后的语句不属于这条路径。
		// U03：已判定 taken 的分支里含 return 时，后续语句在同一路径上也不
		// 可达（`if len(m)==0 { Empty(); return }` 之后的业务分支不应再出现）。
		for _, n := range seq {
			if n.Terminates || seqTakenBranchReturns(n) {
				return out
			}
		}
	}
	return out
}

// walkStmt 单条语句 → 步骤（可能多条：赋值内多个调用）。
func walkStmt(a *Actions, req CodeSequenceRequest, fset *token.FileSet, src []byte, stmt ast.Stmt, tgts *seqTargets, depth int, recvType string, recvDecl map[string]string, f *ast.File, curPkg, curFile string) []*CodeSeqNode {
	callNode := newSeqCallNode(a, req, fset, src, tgts, depth, recvType, recvDecl, f, curPkg, curFile)
	switch s := stmt.(type) {
	case *ast.ExprStmt:
		if call, ok := s.X.(*ast.CallExpr); ok {
			// todo22 §2：Go 求值顺序——实参里的调用先执行；函数字面量只在
			// 实际调用处展开（不穿透未执行的 FuncLit）。
			nodes := seqArgCallNodes(callNode, call)
			if n := callNode(call); n != nil {
				nodes = append(nodes, n)
			}
			if len(nodes) > 0 {
				return nodes
			}
			return nil
		}
	case *ast.AssignStmt:
		return walkSeqAssign(a, req, fset, src, s, callNode, tgts, depth, recvType, recvDecl, f, curPkg, curFile)
	case *ast.IncDecStmt:
		// U04y：`r.ID++` 也是字段赋值观察（operator 保留）。
		if req.focusActive() {
			return nil
		}
		return seqFieldWriteNodes(req, fset, src, s)
	case *ast.IfStmt:
		var steps []*CodeSeqNode

		if s.Init != nil {
			initSteps := walkStmt(a, req, fset, src, s.Init, tgts, depth, recvType, recvDecl, f, curPkg, curFile)
			steps = append(steps, initSteps...)
			// U04e：`if err := row.Scan(&kind); err != nil`——Init 的 Scan 证据与
			// 标量来源必须在构建分支条件之前就绪。
			seqAnnotateScans(req, initSteps)
			seqRecordScalarStatement(req, s.Init)
		}
		node := &CodeSeqNode{Kind: "branch", Label: seqExprText(fset, src, s.Cond), Line: fset.Position(s.Cond.Pos()).Line}
		// U04h：条件引用某次写入调用的错误返回槽（只描述符号关系）。
		node.WriteCondition = seqWriteConditionOrRegister(req, node, s.Cond)
		// U04j：行数变量与零值的比较（只描述符号关系）。
		node.RowCountCondition = seqRowConditionOrRegister(req, node, s.Cond)
		// U04n：事务错误槽条件（begin/commit/rollback）。
		node.TransactionCondition = seqTransactionCondition(req, s.Cond)
		// U04c：条件结构的操作数来源（只记录来源，不改变 Scene 判定）。
		node.Condition = seqCondition(req, fset, src, s.Cond)
		// todo10-5：场景条件判定（taken/excluded/unknown）——排除的分支
		// 保留节点但剔除内容（用户能看到"此处被场景排除"及其依据）
		args, srcs := seqSceneArgsWithLocals(req, s.Cond)
		fit := req.Scene.matchCondSrc(s.Cond, args, srcs, req.sceneConsts)
		// todo25 §13：静态/--scene 无法判定时，用运行时快照的分支结论补足
		// （静态已确定的分支优先，不被快照覆盖）。
		if fit.Status == SceneUnknown && req.runtime != nil {
			if rf, ok := req.runtime.fit(s); ok {
				fit = *rf
			}
		}
		// todo13-3：unknown 的具体原因（值被未知调用改写/分析深度上限）——
		// 不能只给 unknown 而不说明哪一跳让它未知。
		if fit.Status == SceneUnknown && fit.Field != "" {
			fit.Cause = req.sceneWhy[fit.Field]
			if fit.Cause != "" {
				req.sceneCount.Partial = true
				req.sceneCount.Causes = appendUniqueStr(req.sceneCount.Causes,
					fit.Field+" ← "+fit.Cause)
			}
		}
		node.Scene = &fit
		switch fit.Status {
		case SceneTaken:
			req.sceneCount.Taken++
		case SceneExcluded:
			req.sceneCount.Excluded++
		case SceneUnknown:
			req.sceneCount.Unknown++
		}
		sawElse := fillSeqIfBranches(a, req, fset, src, s, &fit, node,
			tgts, depth, recvType, recvDecl, f, curPkg, curFile)
		_ = sawElse
		steps = append(steps, node)
		return steps
	case *ast.ForStmt:
		label := seqExprText(fset, src, s.Cond)
		if label == "" {
			label = "无限循环"
		}
		pre := req.env.clone()
		nodes := walkStmts(a, req, fset, src, s.Body.List, tgts, depth, recvType, recvDecl, f, curPkg, curFile)
		if req.env != nil {
			req.env.mergePossible(pre) // 循环体可能一次都不执行
		}
		return []*CodeSeqNode{{Kind: "loop", Label: label, Line: fset.Position(s.Pos()).Line, Nodes: nodes}}
	case *ast.RangeStmt:
		// todo22 §2.4：range 已知函数列表 → 按元素顺序展开（每项都可确定
		// 执行一次；循环体按迭代顺序折叠，如 process = wrapper(ctx, process)）
		if list, ok := seqRangeElems(a, req, s.X); ok {
			return []*CodeSeqNode{seqRangeOverFuncList(a, req, fset, src, s, list,
				tgts, depth, recvType, recvDecl, f, curPkg, curFile)}
		}
		// todo25 §2.4：条件分支下 append/覆盖得到多候选列表——公共前缀确定
		// 执行，差异尾部按互斥候选分支展开（不退回"整段不展开"）。
		if lists, ok := seqRangeMultiListsValue(a, req, s.X); ok {
			return []*CodeSeqNode{seqRangeOverMultiFuncList(a, req, fset, src, s, lists,
				tgts, depth, recvType, recvDecl, f, curPkg, curFile)}
		}
		// todo22 §2.4：range 对象是未消歧的多候选（条件覆盖下多个列表）——
		// 不按顺序展开（那会把互斥候选当顺序执行），给出明确诊断。
		if why := seqRangeAmbiguous(req, s.X); why != "" {
			return []*CodeSeqNode{{Kind: "loop", Label: "range " + seqExprText(fset, src, s.X),
				Line: fset.Position(s.Pos()).Line,
				Stop: &SeqStop{Code: SeqStopFuncValueUnbound, Detail: why}}}
		}
		// T02：声明的函数列表值未知——留明确诊断（不当作空列表、不猜元素）。
		if why := seqRangeListUnknown(a, req, s.X); why != "" {
			return []*CodeSeqNode{{Kind: "loop", Label: "range " + seqExprText(fset, src, s.X),
				Line: fset.Position(s.Pos()).Line,
				Stop: &SeqStop{Code: SeqStopFuncValueUnbound, Detail: why}}}
		}
		pre := req.env.clone()
		// todo24 §11.5：range 赋值目标若是已绑定函数值，循环内不得沿用循环前
		// 的来源（未知元素列表）。
		seqInvalidateRangeFuncSlots(req, s)
		nodes := walkStmts(a, req, fset, src, s.Body.List, tgts, depth, recvType, recvDecl, f, curPkg, curFile)
		if req.env != nil {
			req.env.mergePossible(pre)
		}
		return []*CodeSeqNode{{Kind: "loop", Label: "range " + seqExprText(fset, src, s.X), Line: fset.Position(s.Pos()).Line, Nodes: nodes}}
	case *ast.BlockStmt:
		return walkStmts(a, req, fset, src, s.List, tgts, depth, recvType, recvDecl, f, curPkg, curFile)
	case *ast.DeferStmt:
		// todo10-2：defer 位置保留在声明处，Exec 标注执行时机（不是立即执行）。
		// T01：defer 的实参在当前流程求值，延迟标记只加在被调度的调用上。
		// U04y：被调度函数体里的字段赋值标 scheduled。
		schedNode := newScheduledCallNode(a, req, fset, src, tgts, depth, recvType, recvDecl,
			f, curPkg, curFile)
		nodes := seqArgCallNodes(schedNode, s.Call)
		if n := schedNode(s.Call); n != nil {
			n.Exec = SeqExecDefer
			seqPropagateExec(n, SeqExecDefer)
			if n.Evidence != nil && n.Evidence.Transaction != nil {
				n.Evidence.Transaction.Exec = SeqExecDefer
			}
			nodes = append(nodes, n)
		}
		return nodes
	case *ast.GoStmt:
		// todo10-2：go 是并发启动（完成顺序未知）；T01：实参同样同步求值。
		schedNode := newScheduledCallNode(a, req, fset, src, tgts, depth, recvType, recvDecl,
			f, curPkg, curFile)
		nodes := seqArgCallNodes(schedNode, s.Call)
		if n := schedNode(s.Call); n != nil {
			n.Exec = SeqExecAsync
			seqPropagateExec(n, SeqExecAsync)
			if n.Evidence != nil && n.Evidence.Transaction != nil {
				n.Evidence.Transaction.Exec = SeqExecAsync
			}
			nodes = append(nodes, n)
		}
		return nodes
	case *ast.ReturnStmt:
		// todo10-2：返回语义分两步——① 返回表达式内的调用按书写顺序先
		// 求值（InReturn 标注）② 独立 return 节点携带返回表达式/结果槽/
		// 终止当前路径（此前有调用时 return 节点整体丢失）
		var out []*CodeSeqNode
		for _, r := range s.Results {
			for _, call := range collectCalls(r) {
				out = append(out, seqArgCallNodes(callNode, call)...)
				if n := callNode(call); n != nil {
					n.InReturn = true
					out = append(out, n)
				}
			}
		}
		ret := &CodeSeqNode{
			Kind: "return", Label: "return", Line: fset.Position(s.Pos()).Line,
			Terminates: true,
		}
		for _, r := range s.Results {
			ret.Values = append(ret.Values, seqExprText(fset, src, r))
		}
		// U04k：返回槽来源（按声明结果数展开 tuple 转发；嵌套调用的 return
		// 不属于本函数——由 seqNodeResultOrigins 的遍历边界保证）。
		tupleSlots := len(ret.Values)
		if len(s.Results) == 1 && len(req.resultNames) > 1 {
			if _, ok := s.Results[0].(*ast.CallExpr); ok {
				tupleSlots = len(req.resultNames)
			}
		}
		for i := 0; i < tupleSlots; i++ {
			ret.Slots = append(ret.Slots, seqResultSlot(i, tupleSlots, req.resultNames))
		}
		ret.ValueOrigins = seqReturnOrigins(req, s.Results, out)
		seqRecordReturnCtx(req, ret, s.Results)
		// todo12-12：裸 return（具名结果）——没有返回表达式，但槽身份仍按
		// 声明顺序展开（不能因为没有表达式就丢掉槽）。
		if len(s.Results) == 0 && len(req.resultNames) > 0 {
			for i := range req.resultNames {
				ret.Slots = append(ret.Slots, seqResultSlot(i, len(req.resultNames), req.resultNames))
			}
		}
		if len(ret.Values) > 0 {
			ret.Label = "return " + strings.Join(ret.Values, ", ")
		}
		out = append(out, ret)
		return out
	case *ast.SendStmt:
		// R100 续：ch <- foo()——原无 case 整条丢弃；提取发送值内调用
		//（纯值发送 ch <- 5 无调用可画——发送是通信动作，无消息线）
		var out []*CodeSeqNode
		for _, call := range collectCalls(s.Value) {
			out = append(out, seqArgCallNodes(callNode, call)...)
			if n := callNode(call); n != nil {
				out = append(out, n)
			}
		}
		return out
	case *ast.SelectStmt:
		return walkSeqSelectStmt(a, req, fset, src, s, tgts, depth, recvType, recvDecl, f, curPkg, curFile, callNode)
	case *ast.SwitchStmt, *ast.TypeSwitchStmt:
		return walkSwitchStmt(a, req, fset, src, s, tgts, depth, recvType, recvDecl, f, curPkg, curFile)
	case *ast.DeclStmt:
		return walkSeqDeclStmt(a, req, fset, src, s, callNode)
	case *ast.LabeledStmt:
		// R100 续：Loop: for——原无 case 整块丢弃（label 本身无时序
		// 语义，递归内部语句）
		return walkStmt(a, req, fset, src, s.Stmt, tgts, depth, recvType, recvDecl, f, curPkg, curFile)
	case *ast.BranchStmt:

		kind := s.Tok.String()
		return []*CodeSeqNode{{Kind: kind, Label: kind, Line: fset.Position(s.Pos()).Line}}
	}
	return nil
}

// newScheduledCallNode go/defer 调度体专用调用节点构造（scheduled=true）。
func newScheduledCallNode(a *Actions, req CodeSequenceRequest, fset *token.FileSet, src []byte,
	tgts *seqTargets, depth int, recvType string, recvDecl map[string]string, f *ast.File,
	curPkg, curFile string) func(call *ast.CallExpr) *CodeSeqNode {
	sched := req
	sched.scheduled = true
	return newSeqCallNode(a, sched, fset, src, tgts, depth, recvType, recvDecl, f, curPkg, curFile)
}

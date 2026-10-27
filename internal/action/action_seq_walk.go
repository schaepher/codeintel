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
		// todo10-9：逐语句检查取消——及时返回，不留"半条链"当成功
		if req.ctx().Err() != nil {
			req.acc.cancel = true
			return out
		}
		seq := walkStmt(a, req, fset, src, st, tgts, depth, recvType, recvDecl, f, curPkg, curFile)
		out = append(out, seq...)
		// todo10-2：return 终止当前路径——其后的语句不属于这条路径
		for _, n := range seq {
			if n.Terminates {
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
		// R100 续：表达式内最外层调用提取（x = foo() + bar()——原只认
		// 顶层 CallExpr，BinaryExpr/Index 等内的调用整条丢失）
		var out []*CodeSeqNode
		for _, rhs := range s.Rhs {
			for _, call := range collectCalls(rhs) {
				out = append(out, seqArgCallNodes(callNode, call)...)
				if n := callNode(call); n != nil {
					out = append(out, n)
				}
			}
		}
		// todo22 §2：赋值更新函数值环境——在调用节点生成之后（RHS 读旧值，
		// LHS 从下一条语句起生效：`f = wrap(ctx, f)` 的右侧是旧 f）。
		recordSeqAssign(a, req, req.curCtx, s)
		// todo10-2 part3：焦点模式下输出"纯赋值"步骤（状态字段赋值常常
		// 不含调用，纯调用树看不到）；非焦点模式保持原输出不变
		if req.focusActive() {
			line := fset.Position(s.Pos()).Line
			node := &CodeSeqNode{Kind: "assign", Label: assignLabel(fset, src, s), Line: line}
			focusHit := req.focusLines != nil && req.focusLines[line]
			if !focusHit && len(req.focus.fieldIDs) == 0 {
				// 局部变量焦点（无类型身份）：按变量名匹配
				focusHit = assignsFocusField(s, req.focus.fields)
			}
			if focusHit {
				node.Focus = true
			}
			out = append(out, node)
		}
		return out
	case *ast.IfStmt:
		var steps []*CodeSeqNode

		if s.Init != nil {
			steps = append(steps, walkStmt(a, req, fset, src, s.Init, tgts, depth, recvType, recvDecl, f, curPkg, curFile)...)
		}
		node := &CodeSeqNode{Kind: "branch", Label: seqExprText(fset, src, s.Cond), Line: fset.Position(s.Cond.Pos()).Line}
		// todo10-5：场景条件判定（taken/excluded/unknown）——排除的分支
		// 保留节点但剔除内容（用户能看到"此处被场景排除"及其依据）
		fit := req.Scene.matchCondSrc(s.Cond, req.sceneArgs, req.sceneSrc, req.sceneConsts)
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
		// todo22 §2.4：range 对象是未消歧的多候选（条件覆盖下多个列表）——
		// 不按顺序展开（那会把互斥候选当顺序执行），给出明确诊断。
		if why := seqRangeAmbiguous(req, s.X); why != "" {
			return []*CodeSeqNode{{Kind: "loop", Label: "range " + seqExprText(fset, src, s.X),
				Line: fset.Position(s.Pos()).Line,
				Stop: &SeqStop{Code: SeqStopFuncValueUnbound, Detail: why}}}
		}
		pre := req.env.clone()
		nodes := walkStmts(a, req, fset, src, s.Body.List, tgts, depth, recvType, recvDecl, f, curPkg, curFile)
		if req.env != nil {
			req.env.mergePossible(pre)
		}
		return []*CodeSeqNode{{Kind: "loop", Label: "range " + seqExprText(fset, src, s.X), Line: fset.Position(s.Pos()).Line, Nodes: nodes}}
	case *ast.BlockStmt:
		return walkStmts(a, req, fset, src, s.List, tgts, depth, recvType, recvDecl, f, curPkg, curFile)
	case *ast.DeferStmt:
		// todo10-2：defer 是"函数返回时执行"——位置保留在声明处（事实），
		// Exec 标注执行时机（不渲染成声明处立即执行）
		if n := callNode(s.Call); n != nil {
			n.Exec = SeqExecDefer
			return []*CodeSeqNode{n}
		}
		return nil
	case *ast.GoStmt:
		// todo10-2：go 是并发启动——完成顺序未知，渲染层不得画成同步完成
		if n := callNode(s.Call); n != nil {
			n.Exec = SeqExecAsync
			return []*CodeSeqNode{n}
		}
		return nil
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
		for i := range ret.Values {
			ret.Slots = append(ret.Slots, seqResultSlot(i, len(ret.Values), req.resultNames))
		}
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
		// R100 续：select 多路复用——原无 case 整块丢弃；仿 SwitchStmt
		// 分支结构（case 内调用 + send 分支提取）
		node := &CodeSeqNode{Kind: "branch", Label: "select", Line: fset.Position(s.Pos()).Line}
		for _, st := range s.Body.List {
			if cc, ok := st.(*ast.CommClause); ok {
				caseLabel := "case "
				if cc.Comm != nil {
					caseLabel += commExprText(fset, src, cc.Comm)
				} else {
					caseLabel += "default"
				}
				caseNode := &CodeSeqNode{Kind: "branch", Label: caseLabel, Line: fset.Position(cc.Pos()).Line}
				// Comm 通信表达式内的调用（case ch2 <- svc.Get() 的 Get
				// ——send 分支调用在 Comm 不在 Body）
				var commExprs []ast.Expr
				switch comm := cc.Comm.(type) {
				case *ast.SendStmt:
					commExprs = []ast.Expr{comm.Value}
				case *ast.AssignStmt:
					commExprs = comm.Rhs
				case *ast.ExprStmt:
					commExprs = []ast.Expr{comm.X}
				}
				for _, e := range commExprs {
					for _, call := range collectCalls(e) {
						caseNode.Nodes = append(caseNode.Nodes, seqArgCallNodes(callNode, call)...)
						if n := callNode(call); n != nil {
							caseNode.Nodes = append(caseNode.Nodes, n)
						}
					}
				}
				caseNode.Nodes = append(caseNode.Nodes, walkStmts(a, req, fset, src, cc.Body, tgts, depth, recvType, recvDecl, f, curPkg, curFile)...)
				node.Nodes = append(node.Nodes, caseNode)
			}
		}
		return []*CodeSeqNode{node}
	case *ast.SwitchStmt, *ast.TypeSwitchStmt:
		return walkSwitchStmt(a, req, fset, src, s, tgts, depth, recvType, recvDecl, f, curPkg, curFile)
	case *ast.DeclStmt:
		// todo22 §2：`var f = func(){…}` / `var f func()`——只建值，不执行
		if gd, ok := s.Decl.(*ast.GenDecl); ok {
			recordSeqDecl(a, req, req.curCtx, gd)
		}
		return nil
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

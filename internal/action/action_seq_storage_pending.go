package action

// U04k：depth=1 的延迟结果绑定（续查完成后回填变量/条件/实参来源）。

import (
	"go/ast"
	"go/token"
)

// seqPendingResolve 深/浅展开的补全（E-T12b 之后调用）：depth=1 时调用结果
// 在续查完成后才知道，此时回填变量绑定、条件与实参来源。
func seqResolvePendingResults(req CodeSequenceRequest) {
	acc := req.acc
	if acc == nil || len(acc.pendingVars) == 0 {
		return
	}
	resolved := map[*ast.Object]*SeqStorageOrigin{}
	for _, p := range acc.pendingVars {
		node := seqCallNodeOf(req, p.call)
		if node == nil {
			continue
		}
		origins := seqNodeResultOrigins(node)
		if p.slot < len(origins) && origins[p.slot] != nil {
			resolved[p.obj] = origins[p.slot]
		}
	}
	if len(resolved) == 0 {
		return
	}
	for _, pc := range acc.pendingConds {
		origin := resolved[pc.obj]
		if origin == nil {
			continue
		}
		switch pc.kind {
		case "write":
			if origin.Kind != SeqOriginWriteError || origin.WriteError == nil {
				continue
			}
			pc.branch.WriteCondition = &SeqWriteCondition{Result: cloneSeqWriteErrorResult(origin.WriteError),
				WhenTrue: pc.whenTrue, WhenFalse: pc.whenFalse}
		case "rows":
			if origin.Kind != SeqOriginWriteRowCount || origin.RowCount == nil {
				continue
			}
			pc.branch.RowCountCondition = &SeqRowCountCondition{Result: cloneSeqRowCount(origin.RowCount),
				WhenTrue: pc.whenTrue, WhenFalse: pc.whenFalse}
		}
	}
	// T12b：把调用结果绑定回登记时的函数环境；已绑定/被后续覆盖的不再倒灌。
	if acc.pendingBound == nil {
		acc.pendingBound = map[*ast.Object]bool{}
	}
	for obj, origin := range resolved {
		if acc.pendingBound[obj] {
			continue
		}
		p, ok := acc.pendingVarsByObj[obj]
		if !ok || p.env == nil {
			continue
		}
		acc.pendingBound[obj] = true
		if seqBindOriginToVar(p.env, obj, origin) {
			continue
		}
		switch origin.Kind {
		case SeqOriginParameter, SeqOriginLiteral, SeqOriginConcat,
			SeqOriginStructCopy, SeqOriginStructLiteral, SeqOriginFieldRead:
			p.env.setScalar(obj, cloneSeqScalarOrigin(origin))
		}
	}
	for _, po := range acc.pendingOrigins {
		origin := resolved[po.obj]
		if origin == nil {
			continue
		}
		if po.apply != nil {
			po.apply(cloneSeqScalarOrigin(origin))
		}
	}
}

// seqWriteConditionOrRegister 错误条件；变量尚在等待调用结果时登记延迟回填。
func seqWriteConditionOrRegister(req CodeSequenceRequest, branch *CodeSeqNode, cond ast.Expr) *SeqWriteCondition {
	if wc := seqWriteCondition(req, cond); wc != nil {
		return wc
	}
	if branch == nil || req.env == nil {
		return nil
	}
	obj, whenTrue, whenFalse, ok := seqWriteCondSpec(req, cond)
	if !ok || !seqPendingVarOf(req, obj) {
		return nil
	}
	seqRegisterPendingCond(req, branch, obj, "write", whenTrue, whenFalse)
	return nil
}

// seqRowConditionOrRegister 行数条件；变量尚在等待调用结果时登记延迟回填。
func seqRowConditionOrRegister(req CodeSequenceRequest, branch *CodeSeqNode, cond ast.Expr) *SeqRowCountCondition {
	if rc := seqRowCondition(req, cond); rc != nil {
		return rc
	}
	if branch == nil || req.env == nil {
		return nil
	}
	obj, whenTrue, whenFalse, ok := seqRowCondSpec(req, cond)
	if !ok || !seqPendingVarOf(req, obj) {
		return nil
	}
	seqRegisterPendingCond(req, branch, obj, "rows", whenTrue, whenFalse)
	return nil
}

// seqPendingVarOf 该变量是否在等待调用结果来源（depth=1 未展开）。
func seqPendingVarOf(req CodeSequenceRequest, obj *ast.Object) bool {
	if req.acc == nil || obj == nil {
		return false
	}
	_, ok := req.acc.pendingVarsByObj[obj]
	return ok
}

// seqPropagatePendingVar 形参继承调用点实参的延迟来源（depth=1）。
func seqPropagatePendingVar(req CodeSequenceRequest, from *ast.Object, to *ast.Object) {
	if req.acc == nil || from == nil || to == nil {
		return
	}
	p, ok := req.acc.pendingVarsByObj[from]
	if !ok {
		return
	}
	seqRegisterPendingVar(req, p.call, to, p.slot)
}

// seqRegisterPendingVar 登记"调用结果 → 变量槽"的延迟绑定。
func seqRegisterPendingVar(req CodeSequenceRequest, call *ast.CallExpr, obj *ast.Object, slot int) {
	if req.acc == nil || call == nil || obj == nil {
		return
	}
	if req.acc.pendingVarsByObj == nil {
		req.acc.pendingVarsByObj = map[*ast.Object]seqPendingVar{}
	}
	p := seqPendingVar{call: call, obj: obj, slot: slot, env: req.env}
	req.acc.pendingVars = append(req.acc.pendingVars, p)
	req.acc.pendingVarsByObj[obj] = p
}

// seqRegisterPendingCond 条件引用了待解析变量（depth=1）。
func seqRegisterPendingCond(req CodeSequenceRequest, branch *CodeSeqNode, obj *ast.Object,
	kind, whenTrue, whenFalse string) {
	if req.acc == nil || branch == nil || obj == nil {
		return
	}
	req.acc.pendingConds = append(req.acc.pendingConds, seqPendingCond{branch: branch, obj: obj,
		kind: kind, whenTrue: whenTrue, whenFalse: whenFalse})
}

// seqRegisterPendingOrigin 实参来源待解析（depth=1）。
func seqRegisterPendingOrigin(req CodeSequenceRequest, obj *ast.Object,
	apply func(*SeqStorageOrigin)) {
	if req.acc == nil || obj == nil || apply == nil {
		return
	}
	req.acc.pendingOrigins = append(req.acc.pendingOrigins, seqPendingOrigin{obj: obj, apply: apply})
}

// seqIdentObjOf 表达式的标识符对象（非标识符返回 nil）。
func seqIdentObjOf(e ast.Expr) *ast.Object {
	if id, ok := e.(*ast.Ident); ok {
		return id.Obj
	}
	return nil
}

var _ = token.NoPos

// seqRegisterPendingValueOrigins 实参来源尚待调用结果（depth=1）：登记续查后
// 回填的回调（storage/ORM/读取证据共用一个来源解析）。
func seqRegisterPendingValueOrigins(req CodeSequenceRequest, node *CodeSeqNode, call *ast.CallExpr) {
	if req.acc == nil || node == nil || node.Evidence == nil || call == nil {
		return
	}
	ev := node.Evidence
	patch := func(argIdx int, value *SeqStorageValue) {
		if value == nil || value.Origin != nil || value.Argument < 0 {
			return
		}
		pos := argIdx + value.Argument
		if pos < 0 || pos >= len(call.Args) {
			return
		}
		obj := seqIdentObjOf(call.Args[pos])
		if obj == nil || !seqPendingVarOf(req, obj) {
			return
		}
		v := value
		seqRegisterPendingOrigin(req, obj, func(o *SeqStorageOrigin) {
			v.Origin = o
		})
	}
	if st := ev.Storage; st != nil {
		sqlArg := seqPersistenceSQLArg(node)
		if sqlArg >= 0 {
			for i := range st.Writes {
				patch(sqlArg+1, &st.Writes[i].Value)
			}
			for i := range st.Predicates {
				patch(sqlArg+1, &st.Predicates[i].Value)
			}
		}
	}
	if orm := ev.ORM; orm != nil {
		for i := range orm.Arguments {
			patch(0, &orm.Arguments[i])
		}
		for i := range orm.Predicates {
			patch(0, &orm.Predicates[i].Value)
		}
	}
}

package action

// todo25 §13：运行时快照的调用求值（SQL 读取/Scan/本仓函数/内建）。

import (
	"go/ast"
	"strconv"
)

func (e *rtEval) evalCall(frame *rtFrame, call *ast.CallExpr, s *rtState, depth int) (rtValue, []*rtState) {
	if call == nil {
		return rtUnknownValue(), []*rtState{s}
	}
	// sql.Row.Scan：内层查询已算出 rows → 成功/失败两个状态（保留错误边界）。
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Scan" {
		if len(call.Args) == 0 {
			return rtUnknownValue(), []*rtState{s}
		}
		if inner, ok2 := sel.X.(*ast.CallExpr); ok2 {
			if out := e.evalScanOutcomes(frame, call, inner, s, depth); len(out) > 0 {
				return out[0].val, []*rtState{out[0].state}
			}
			return rtUnknownValue(), []*rtState{s}
		}
		if id, ok2 := sel.X.(*ast.Ident); ok2 {
			if out := e.evalRowsScanOutcomes(frame, call, id, s, depth); len(out) > 0 {
				return out[0].val, []*rtState{out[0].state}
			}
			return rtUnknownValue(), []*rtState{s}
		}
	}
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
		if id, ok2 := sel.X.(*ast.Ident); ok2 {
			p := seqImportPath(frame.ctx.file, id.Name)
			if p == "strings" {
				if v, out, ok3 := e.evalStringsBuiltin(frame, call, sel.Sel.Name, s, depth); ok3 {
					return v, out
				}
			}
			if p == "strconv" && sel.Sel.Name == "Itoa" && len(call.Args) == 1 {
				v, variants := e.evalExpr(frame, call.Args[0], s, depth)
				if v.kind == rtInt {
					return rtStringValue(strconv.Itoa(int(v.i))), variants
				}
			}
		}
	}
	name := runtimeCallName(call.Fun)
	switch name {
	case "len":
		if len(call.Args) == 1 {
			v, variants := e.evalExpr(frame, call.Args[0], s, depth)
			if n, ok := rtLen(v); ok {
				return rtIntValue(int64(n)), variants
			}
			return rtUnknownValue(), variants
		}
	case "append":
		if len(call.Args) >= 2 {
			base, _ := e.evalExpr(frame, call.Args[0], s, depth)
			elems := append([]rtValue(nil), base.elems...)
			for _, arg := range call.Args[1:] {
				v, _ := e.evalExpr(frame, arg, s, depth)
				elems = append(elems, v)
			}
			return rtSliceValue(elems), []*rtState{s}
		}
	case "make":
		if len(call.Args) >= 1 {
			switch call.Args[0].(type) {
			case *ast.MapType:
				return rtMapValue(map[string]rtValue{}), []*rtState{s}
			case *ast.ArrayType:
				return rtSliceValue(nil), []*rtState{s}
			}
		}
	case "int", "int32", "int64", "uint", "uint32", "uint64", "float32", "float64":
		if len(call.Args) == 1 {
			v, variants := e.evalExpr(frame, call.Args[0], s, depth)
			switch v.kind {
			case rtInt, rtFloat:
				return v, variants
			}
			return rtUnknownValue(), variants
		}
	}
	// U03：reflect 元信息链（TypeOf/Field/Tag.Get）——静态源码可证的类型/标签。
	if rv, out, handled := e.evalReflectCall(frame, call, s, depth); handled {
		return rv, out
	}
	if qv, out, handled := e.evalQueryCall(frame, call, s, depth); handled {
		return qv, out
	}
	if recvType, recvVal, method, ok := e.runtimeMethodCall(frame, call, s, depth); ok {
		target := "symbol:go:" + frame.pkgPath + ":(" + recvType + ")." + method
		if out := e.evalFuncCallOutcomesWithReceiver(frame, call, s, depth, target, &recvVal); len(out) > 0 {
			statesOut := make([]*rtState, 0, len(out))
			for _, oc := range out {
				statesOut = append(statesOut, oc.state)
			}
			return out[0].val, statesOut
		}
	}
	if target, ok := runtimeCalleeID(frame, call); ok {
		if out := e.evalFuncCallOutcomesFor(frame, call, s, depth, target); len(out) > 0 {
			statesOut := make([]*rtState, 0, len(out))
			for _, oc := range out {
				statesOut = append(statesOut, oc.state)
			}
			// T14：被调函数可能通过指针实参改写局部标量（rewrite(&enabled)）
			// ——除非该调用是已证明的读取（Scan/Query 已在上方返回），否则
			// 取址实参的旧值不能跨调用继续使用。
			for _, st := range statesOut {
				e.invalidateAddrTaken(call, st)
			}
			return out[0].val, statesOut
		}
	}
	// T14：未知调用同样可能改写取址的局部变量 → 降级为 unknown。
	e.invalidateAddrTaken(call, s)
	return rtUnknownValue(), []*rtState{s}
}

func (e *rtEval) runtimeMethodCall(frame *rtFrame, call *ast.CallExpr, s *rtState, depth int) (string, rtValue, string, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", rtValue{}, "", false
	}
	if id, ok2 := sel.X.(*ast.Ident); ok2 && id.Obj != nil && id.Obj.Kind == ast.Pkg {
		return "", rtValue{}, "", false
	}
	rt := e.runtimeTypeFromExpr(frame, sel.X, s, depth)
	recv, _ := e.evalExpr(frame, sel.X, s, depth)
	if recv.kind == rtRow && recv.typeName != "" {
		rt = &runtimeType{name: recv.typeName}
	}
	if rt == nil {
		return "", rtValue{}, "", false
	}
	id := "symbol:go:" + frame.pkgPath + ":(" + rt.name + ")." + sel.Sel.Name
	if _, _, ok := seqFuncSource(e.a, e.req, id); !ok {
		return "", rtValue{}, "", false
	}
	return rt.name, recv, sel.Sel.Name, true
}

func valWithStates(v rtValue, states []*rtState) (rtValue, []*rtState) { return v, states }

func (e *rtEval) evalArgs(frame *rtFrame, args []ast.Expr, s *rtState, depth int) ([]rtValue, []*rtState) {
	vals := make([]rtValue, 0, len(args))
	states := []*rtState{s}
	for _, arg := range args {
		v, variants := e.evalExpr(frame, arg, states[0], depth)
		vals = append(vals, v)
		states = variants
	}
	if len(states) == 0 {
		states = []*rtState{s}
	}
	return vals, states
}

// runtimeCalleeID 本仓包级函数调用（同包 Ident / 导入包 Selector）→ canonical ID。
func runtimeCalleeID(frame *rtFrame, call *ast.CallExpr) (string, bool) {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		if fun.Obj == nil || fun.Obj.Kind != ast.Fun {
			return "", false
		}
		if _, ok := fun.Obj.Decl.(*ast.FuncDecl); !ok {
			return "", false
		}
		return "symbol:go:" + frame.pkgPath + ":" + fun.Name, true
	case *ast.SelectorExpr:
		id, ok := fun.X.(*ast.Ident)
		if !ok || (id.Obj != nil && id.Obj.Kind != ast.Pkg) {
			return "", false
		}
		p := seqImportPath(frame.ctx.file, id.Name)
		if p == "" {
			return "", false
		}
		return "symbol:go:" + p + ":" + fun.Sel.Name, true
	}
	return "", false
}

// evalQueryCall database/sql 读取点 → rows 值（不展开内部实现）。
func (e *rtEval) assignScanTarget(arg ast.Expr, v rtValue, s *rtState) {
	target := arg
	if u, ok := arg.(*ast.UnaryExpr); ok {
		target = u.X
	}
	switch t := target.(type) {
	case *ast.Ident:
		e.assignIdent(t, v, s)
	case *ast.SelectorExpr:
		if id, ok := t.X.(*ast.Ident); ok && id.Obj != nil {
			if mv, ok2 := s.env[id.Obj]; ok2 && mv.kind == rtRow {
				if mv.cols == nil {
					mv.cols = map[string]rtValue{}
				}
				mv.cols[t.Sel.Name] = v
				if !containsRuntimeString(mv.order, t.Sel.Name) {
					mv.order = append(mv.order, t.Sel.Name)
				}
				s.env[id.Obj] = mv
			}
		}
	}
}

func runtimeScanTargetName(arg ast.Expr) string {
	if u, ok := arg.(*ast.UnaryExpr); ok {
		if id, ok2 := u.X.(*ast.Ident); ok2 {
			return id.Name
		}
	}
	if id, ok := arg.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

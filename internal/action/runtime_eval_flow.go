package action

// todo25 §13：分支/返回/循环的运行时语句执行（从 runtime_eval.go 拆出）。

import (
	"go/ast"
)

func (e *rtEval) execIf(frame *rtFrame, x *ast.IfStmt, s *rtState, depth int) []*rtState {
	states := []*rtState{s}
	if x.Init != nil {
		states = e.execStmt(frame, x.Init, s, depth)
	}
	var out []*rtState
	for _, st := range states {
		v, _ := e.evalExpr(frame, x.Cond, st, depth)
		truth, known := rtTruth(v)
		e.an.recordBranch(x, truth, known)
		switch {
		case known && truth:
			out = append(out, e.execBlock(frame, x.Body.List, []*rtState{st}, depth)...)
		case known && !truth:
			out = append(out, e.execElse(frame, x.Else, st, depth)...)
		default:
			out = append(out, e.execBlock(frame, x.Body.List, []*rtState{cloneRuntimeState(st)}, depth)...)
			out = append(out, e.execElse(frame, x.Else, st, depth)...)
		}
	}
	return e.capStates(out)
}

func (e *rtEval) execElse(frame *rtFrame, stmt ast.Stmt, s *rtState, depth int) []*rtState {
	if stmt == nil {
		return []*rtState{s}
	}
	return e.execStmt(frame, stmt, s, depth)
}

func (e *rtEval) execReturn(frame *rtFrame, x *ast.ReturnStmt, s *rtState, depth int) []*rtState {
	var states = []*rtState{s}
	var vals []rtValue
	for _, r := range x.Results {
		v, variants := e.evalExpr(frame, r, states[0], depth)
		if len(variants) > 0 {
			states[0] = variants[0]
		}
		if v.kind == rtTuple {
			vals = append(vals, v.elems...)
		} else {
			vals = append(vals, v)
		}
	}
	if len(x.Results) == 0 {
		vals = nil
	}
	for _, st := range states {
		st.ret = &rtReturn{vals: vals}
	}
	return states
}

func (e *rtEval) execRange(frame *rtFrame, x *ast.RangeStmt, s *rtState, depth int) []*rtState {
	v, variants := e.evalExpr(frame, x.X, s, depth)
	if len(variants) > 0 {
		s = variants[0]
	}
	if v.kind == rtMap {
		states := []*rtState{s}
		for _, k := range v.order {
			if x.Key != nil {
				if id, ok := x.Key.(*ast.Ident); ok && id.Obj != nil {
					states[0].env[id.Obj] = rtStringValue(k)
				}
			}
			if x.Value != nil {
				if id, ok := x.Value.(*ast.Ident); ok && id.Obj != nil {
					states[0].env[id.Obj] = v.cols[k]
				}
			}
			states = e.execBlock(frame, x.Body.List, states, depth)
			if len(states) == 0 {
				break
			}
		}
		return states
	}
	if v.kind != rtSlice && v.kind != rtRows {
		out := e.execBlock(frame, x.Body.List, []*rtState{s}, depth)
		// 未知集合上执行一次循环体只为保留调用证据；聚合结果（map）的
		// 基数/成员不可据此确定（len(map) 保持 unknown）。
		for _, st := range out {
			for obj, val := range st.env {
				if val.kind == rtMap {
					st.env[obj] = rtUnknownValue()
				}
			}
		}
		return out
	}
	states := []*rtState{s}
	for _, elem := range v.elems {
		if x.Value != nil {
			if id, ok := x.Value.(*ast.Ident); ok {
				if id.Obj != nil {
					states[0].env[id.Obj] = elem
				}
			}
		}
		states = e.execBlock(frame, x.Body.List, states, depth)
		if len(states) == 0 {
			break
		}
	}
	return states
}

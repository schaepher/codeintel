package action

// todo25 §13：赋值目标/多返回路径展开（从 runtime_eval.go 拆出）。
import "go/ast"

func (e *rtEval) execAssign(frame *rtFrame, x *ast.AssignStmt, s *rtState, depth int) []*rtState {
	// 单 RHS：函数返回（可能多值/多返回路径）与 Scan（成功/失败）按**变体**
	// 展开，保留返回值与错误的相关性。
	if len(x.Rhs) == 1 {
		var outcomes []rtOutcome
		if call, ok := x.Rhs[0].(*ast.CallExpr); ok {
			if sel, ok2 := call.Fun.(*ast.SelectorExpr); ok2 && sel.Sel.Name == "Scan" {
				if inner, ok3 := sel.X.(*ast.CallExpr); ok3 {
					outcomes = e.evalScanOutcomes(frame, call, inner, s, depth)
				}
			}
			if outcomes == nil {
				outcomes = e.evalFuncCallOutcomes(frame, call, s, depth)
			}
			if outcomes == nil && isRuntimeQueryCall(call) {
				outcomes = e.evalQueryOutcomes(frame, call, s, depth)
			}
		}
		if outcomes != nil {
			var out []*rtState
			for _, oc := range outcomes {
				assignRuntimeValues(e, frame, oc.state, x.Lhs, oc.val)
				out = append(out, oc.state)
			}
			if call, ok := x.Rhs[0].(*ast.CallExpr); ok && isRuntimeQueryCall(call) && len(x.Lhs) > 0 {
				if id, ok2 := x.Lhs[0].(*ast.Ident); ok2 && id.Obj != nil {
					if q := e.an.queries[call]; q != nil {
						e.an.rowQueries[id.Obj] = q
					}
				}
			}
			return e.capStates(out)
		}
	}
	// 通用路径：逐个 RHS 求值（不展开多返回；fixture 中的多值都在上面处理）。
	states := []*rtState{s}
	for i, rhs := range x.Rhs {
		v, variants := e.evalExpr(frame, rhs, states[0], depth)
		if len(variants) > 0 {
			states[0] = variants[0]
		}
		if i < len(x.Lhs) {
			e.assignExprValue(frame, x.Lhs[i], v, states[0])
		}
	}
	return states
}

type rtOutcome struct {
	state *rtState
	val   rtValue
}

func assignRuntimeValues(e *rtEval, frame *rtFrame, s *rtState, lhs []ast.Expr, val rtValue) {
	if val.kind == rtTuple {
		for i, v := range val.elems {
			if i < len(lhs) {
				e.assignExprValue(frame, lhs[i], v, s)
			}
		}
		return
	}
	if len(lhs) > 0 {
		e.assignExprValue(frame, lhs[0], val, s)
	}
}

// assignExprValue 赋值目标（变量/结构体字段/map 索引）。
func (e *rtEval) assignExprValue(frame *rtFrame, lhs ast.Expr, v rtValue, s *rtState) {
	switch l := lhs.(type) {
	case *ast.Ident:
		if l.Name == "_" {
			return
		}
		e.assignIdent(l, v, s)
	case *ast.SelectorExpr:
		if id, ok := l.X.(*ast.Ident); ok {
			if mv, ok2 := s.env[id.Obj]; ok2 && (mv.kind == rtRow || mv.kind == rtMap) {
				if mv.kind == rtRow {
					if mv.cols == nil {
						mv.cols = map[string]rtValue{}
					}
					mv.cols[l.Sel.Name] = v
					if !containsRuntimeString(mv.order, l.Sel.Name) {
						mv.order = append(mv.order, l.Sel.Name)
					}
					s.env[id.Obj] = mv
					return
				}
			}
			e.assignExpr(frame, lhs, v, s)
		}
	case *ast.IndexExpr:
		base, variants := e.evalExpr(frame, l.X, s, 0)
		if len(variants) > 0 {
			s = variants[0]
		}
		key, _ := e.evalExpr(frame, l.Index, s, 0)
		if base.kind == rtMap && key.known() {
			if base.cols == nil {
				base.cols = map[string]rtValue{}
			}
			k := key.rtDisplay()
			base.cols[k] = v
			if !containsRuntimeString(base.order, k) {
				base.order = append(base.order, k)
			}
			if id, ok := l.X.(*ast.Ident); ok && id.Obj != nil {
				s.env[id.Obj] = base
			}
		}
	default:
		e.assignExpr(frame, lhs, v, s)
	}
}

func containsRuntimeString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func flattenRuntimeValues(in [][]rtValue) []rtValue {
	var out []rtValue
	for _, v := range in {
		out = append(out, v...)
	}
	return out
}

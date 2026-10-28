package action

// T14：被调函数求值结果与取址失效（从 runtime_eval_call.go 拆出——行数治理）。

import (
	"go/ast"
	"go/token"

	"github.com/schaepher/codeintel/internal/domain"
)

// invalidateAddrTaken 取址实参（&x，x 为局部变量）在调用后值不再可信：
// 任何未证明为只读的调用都可能通过指针写入（T14 的 rewrite(&enabled)）。
func (e *rtEval) invalidateAddrTaken(call *ast.CallExpr, s *rtState) {
	if call == nil || s == nil {
		return
	}
	for _, arg := range call.Args {
		u, ok := arg.(*ast.UnaryExpr)
		if !ok || u.Op != token.AND {
			continue
		}
		id, ok := u.X.(*ast.Ident)
		if !ok || id.Obj == nil {
			continue
		}
		if _, isLocal := s.env[id.Obj]; isLocal {
			s.env[id.Obj] = rtUnknownValue()
		}
	}
}

// evalFuncCallOutcomes 函数调用 → 每个 return 路径一个变体（保留值与错误相关）。
func (e *rtEval) evalFuncCallOutcomes(frame *rtFrame, call *ast.CallExpr, s *rtState, depth int) []rtOutcome {
	target, ok := runtimeCalleeID(frame, call)
	if !ok {
		return nil
	}
	return e.evalFuncCallOutcomesFor(frame, call, s, depth, target)
}

func (e *rtEval) evalMethodCallOutcomes(frame *rtFrame, call *ast.CallExpr, s *rtState, depth int) []rtOutcome {
	recvType, recvVal, method, ok := e.runtimeMethodCall(frame, call, s, depth)
	if !ok {
		return nil
	}
	target := "symbol:go:" + frame.pkgPath + ":(" + recvType + ")." + method
	return e.evalFuncCallOutcomesWithReceiver(frame, call, s, depth, target, &recvVal)
}

func (e *rtEval) evalFuncCallOutcomesFor(frame *rtFrame, call *ast.CallExpr, s *rtState, depth int, target string) []rtOutcome {
	vals, states := e.evalArgs(frame, call.Args, s, depth)
	vals = flattenRuntimeVariadic(vals, call)
	if len(states) == 0 {
		states = []*rtState{s}
	}
	rets, err := e.evalFunc(domain.CanonicalID(target), vals, depth+1)
	if err != nil || len(rets) == 0 {
		return nil
	}
	out := make([]rtOutcome, 0, len(rets))
	for _, r := range rets {
		st := cloneRuntimeState(states[0])
		val := rtUnknownValue()
		switch len(r.vals) {
		case 0:
			val = rtUnknownValue()
		case 1:
			val = r.vals[0]
		default:
			val = rtValue{kind: rtTuple, elems: append([]rtValue(nil), r.vals...)}
		}
		out = append(out, rtOutcome{state: st, val: val})
	}
	return out
}

// valWithStates 单值 + 状态变体（把返回值与变体一起返回的语法糖）。
func (e *rtEval) evalFuncCallOutcomesWithReceiver(frame *rtFrame, call *ast.CallExpr,
	s *rtState, depth int, target string, recv *rtValue) []rtOutcome {
	vals, states := e.evalArgs(frame, call.Args, s, depth)
	vals = flattenRuntimeVariadic(vals, call)
	if len(states) == 0 {
		states = []*rtState{s}
	}
	rets, err := e.evalFuncWithReceiver(domain.CanonicalID(target), recv, vals, depth+1)
	if err != nil || len(rets) == 0 {
		return nil
	}
	out := make([]rtOutcome, 0, len(rets))
	for _, r := range rets {
		st := cloneRuntimeState(states[0])
		val := rtUnknownValue()
		switch len(r.vals) {
		case 0:
			val = rtUnknownValue()
		case 1:
			val = r.vals[0]
		default:
			val = rtValue{kind: rtTuple, elems: append([]rtValue(nil), r.vals...)}
		}
		out = append(out, rtOutcome{state: st, val: val})
	}
	return out
}

// runtimeMethodCall 本仓方法调用：receiver 类型来自轻量类型表。

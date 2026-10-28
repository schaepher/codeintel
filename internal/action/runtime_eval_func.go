package action

// T14：解释器函数调用与预算检查（从 runtime_eval.go 拆出——行数治理）。

import (
	"fmt"
	"go/ast"

	"github.com/schaepher/codeintel/internal/domain"
)

// step 记一步解释步数；超出预算 → 记错误（T14）。
func (e *rtEval) step() bool {
	e.steps++
	if e.steps > runtimeEvalMaxSteps {
		if e.err == nil {
			e.err = fmt.Errorf("runtime-data: analysis step budget exhausted (%d)", runtimeEvalMaxSteps)
		}
		return false
	}
	return true
}

// canceled 检查 ctx 取消（每个语句块检查一次；取消记 ErrSeqCanceled）。
func (e *rtEval) canceled() bool {
	if e.err != nil {
		return true
	}
	if e.ctx != nil && e.ctx.Err() != nil {
		e.err = ErrSeqCanceled
		return true
	}
	return false
}

func (e *rtEval) evalFunc(symID domain.CanonicalID, args []rtValue, depth int) ([]rtReturn, error) {
	return e.evalFuncWithReceiver(symID, nil, args, depth)
}

func (e *rtEval) evalFuncWithReceiver(symID domain.CanonicalID, recv *rtValue, args []rtValue, depth int) ([]rtReturn, error) {
	if e.canceled() {
		return nil, e.err
	}
	if depth >= e.maxDepth || e.stack[string(symID)] {
		return []rtReturn{{vals: []rtValue{rtUnknownValue()}}}, nil
	}
	fn, ctx, ok := seqFuncSource(e.a, e.req, string(symID))
	if !ok {
		return nil, nil
	}
	if e.stack == nil {
		e.stack = map[string]bool{}
	}
	e.stack[string(symID)] = true
	defer delete(e.stack, string(symID))
	frame := &rtFrame{fn: fn, ctx: ctx, symID: string(symID), pkgPath: pkgPathOf(string(symID))}
	st := &rtState{env: map[*ast.Object]rtValue{}, types: map[*ast.Object]*runtimeType{}}
	bindRuntimeParams(st.env, fn.Type.Params, args)
	if fn.Recv != nil && len(fn.Recv.List) > 0 && recv != nil {
		for _, name := range fn.Recv.List[0].Names {
			if name.Obj != nil {
				st.env[name.Obj] = *recv
				if rt := runtimeTypeOfTypeExpr(name.Obj, fn.Recv.List[0].Type); rt != nil {
					st.types[name.Obj] = rt
				}
			}
		}
	}
	bindRuntimeParamTypes(st.types, fn.Type.Params)
	states := e.execBlock(frame, fn.Body.List, []*rtState{st}, depth)
	var out []rtReturn
	for _, s := range states {
		if s.ret != nil {
			out = append(out, *s.ret)
		}
	}
	if len(out) == 0 {
		out = append(out, rtReturn{})
	}
	if e.err != nil {
		return nil, e.err
	}
	return out, nil
}

func bindRuntimeParams(env map[*ast.Object]rtValue, params *ast.FieldList, args []rtValue) {
	if params == nil {
		return
	}
	i := 0
	for _, fl := range params.List {
		for _, nm := range fl.Names {
			if nm.Obj == nil {
				i++
				continue
			}
			if i < len(args) {
				env[nm.Obj] = args[i]
			} else {
				env[nm.Obj] = rtUnknownValue()
			}
			i++
		}
	}
}

package action

// todo25 §13：运行时快照的符号执行（语句层）。

import (
	"go/ast"
	"go/token"

	"github.com/schaepher/codeintel/internal/domain"
)

type rtState struct {
	env map[*ast.Object]rtValue
	ret *rtReturn
}

type rtReturn struct {
	vals []rtValue
}

type rtFrame struct {
	fn      *ast.FuncDecl
	ctx     *seqFileCtx
	symID   string
	pkgPath string
}

type rtEval struct {
	a         *Actions
	an        *runtimeAnalysis
	req       CodeSequenceRequest
	depth     int
	maxDepth  int
	maxStates int
	stack     map[string]bool
}

func (e *rtEval) evalFunc(symID domain.CanonicalID, args []rtValue, depth int) ([]rtReturn, error) {
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
	st := &rtState{env: map[*ast.Object]rtValue{}}
	bindRuntimeParams(st.env, fn.Type.Params, args)
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

func (e *rtEval) execBlock(frame *rtFrame, stmts []ast.Stmt, states []*rtState, depth int) []*rtState {
	for _, st := range stmts {
		var next []*rtState
		for _, s := range states {
			if s.ret != nil {
				next = append(next, s)
				continue
			}
			next = append(next, e.execStmt(frame, st, s, depth)...)
		}
		states = e.capStates(next)
		if len(states) == 0 {
			return nil
		}
	}
	return states
}

func (e *rtEval) execStmt(frame *rtFrame, st ast.Stmt, s *rtState, depth int) []*rtState {
	switch x := st.(type) {
	case *ast.ExprStmt:
		_, out := e.evalExpr(frame, x.X, s, depth)
		return out
	case *ast.AssignStmt:
		return e.execAssign(frame, x, s, depth)
	case *ast.DeclStmt:
		return e.execDecl(frame, x, s, depth)
	case *ast.IfStmt:
		return e.execIf(frame, x, s, depth)
	case *ast.ReturnStmt:
		return e.execReturn(frame, x, s, depth)
	case *ast.BlockStmt:
		return e.execBlock(frame, x.List, []*rtState{s}, depth)
	case *ast.ForStmt:
		// rows.Next() 循环：按查询结果逐行执行（cursor 供 rows.Scan 取当前行）。
		if id, rows, ok := e.rowsNextLoop(frame, x.Cond, s); ok {
			states := []*rtState{s}
			for _, row := range rows.elems {
				if id.Obj != nil {
					e.an.cursor[id.Obj] = row
				}
				states = e.execBlock(frame, x.Body.List, states, depth)
				if len(states) == 0 {
					break
				}
			}
			if id.Obj != nil {
				delete(e.an.cursor, id.Obj)
			}
			return states
		}
		return e.execBlock(frame, x.Body.List, []*rtState{s}, depth)
	case *ast.RangeStmt:
		return e.execRange(frame, x, s, depth)
	case *ast.IncDecStmt:
		if id, ok := x.X.(*ast.Ident); ok {
			v := e.evalIdent(frame, id, s)
			if v.kind == rtInt {
				nv := rtIntValue(v.i + 1)
				if x.Tok == token.DEC {
					nv = rtIntValue(v.i - 1)
				}
				e.assignIdent(id, nv, s)
			}
		}
		return []*rtState{s}
	case *ast.LabeledStmt:
		return e.execStmt(frame, x.Stmt, s, depth)
	case *ast.SwitchStmt:
		// 首期：switch 不裁剪（保留所有 case；内部调用仍执行一次）
		var merged []*rtState
		for _, c := range x.Body.List {
			if cc, ok := c.(*ast.CaseClause); ok {
				merged = append(merged, e.execBlock(frame, cc.Body, []*rtState{cloneRuntimeState(s)}, depth)...)
			}
		}
		if len(merged) == 0 {
			merged = []*rtState{s}
		}
		return merged
	case *ast.DeferStmt:
		if x.Call != nil {
			_, out := e.evalExpr(frame, x.Call, s, depth)
			return out
		}
	case *ast.GoStmt:
		if x.Call != nil {
			_, out := e.evalExpr(frame, x.Call, s, depth)
			return out
		}
	}
	return []*rtState{s}
}

func (e *rtEval) execDecl(frame *rtFrame, x *ast.DeclStmt, s *rtState, depth int) []*rtState {
	gd, ok := x.Decl.(*ast.GenDecl)
	if !ok {
		return []*rtState{s}
	}
	for _, spec := range gd.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for i, name := range vs.Names {
			if i < len(vs.Values) {
				v, variants := e.evalExpr(frame, vs.Values[i], s, depth)
				if len(variants) > 0 {
					s = variants[0]
				}
				if name.Obj != nil {
					s.env[name.Obj] = v
					if gd.Tok == token.VAR && e.an.pkgVars[name.Obj] {
						g := e.an.globalByNameOf(name)
						g.value = v
					}
				}
			} else if name.Obj != nil {
				switch vs.Type.(type) {
				case *ast.ArrayType:
					s.env[name.Obj] = rtSliceValue(nil)
				case *ast.MapType:
					s.env[name.Obj] = rtMapValue(map[string]rtValue{})
				default:
					s.env[name.Obj] = rtUnknownValue()
				}
			}
		}
	}
	return []*rtState{s}
}

func (e *rtEval) assignExpr(frame *rtFrame, lhs ast.Expr, v rtValue, s *rtState) {
	switch l := lhs.(type) {
	case *ast.Ident:
		e.assignIdent(l, v, s)
	case *ast.SelectorExpr:
		if id, ok := l.X.(*ast.Ident); ok {
			g := e.an.globalByNameOf(id)
			g.mutated = true
		}
	case *ast.StarExpr:
		if id, ok := l.X.(*ast.Ident); ok {
			g := e.an.globalByNameOf(id)
			g.mutated = true
		}
	}
}

func (e *rtEval) assignIdent(id *ast.Ident, v rtValue, s *rtState) {
	if id == nil {
		return
	}
	if id.Obj != nil {
		if e.an.pkgVars[id.Obj] {
			g := e.an.globalByNameOf(id)
			g.value = v
			g.mutated = true
			return
		}
		s.env[id.Obj] = v
		return
	}
	g := e.an.globalByNameOf(id)
	g.value = v
	g.mutated = true
}

func cloneRuntimeState(s *rtState) *rtState {
	out := &rtState{ret: s.ret, env: make(map[*ast.Object]rtValue, len(s.env))}
	for k, v := range s.env {
		out.env[k] = v
	}
	return out
}

func (e *rtEval) capStates(in []*rtState) []*rtState {
	if e.maxStates <= 0 || len(in) <= e.maxStates {
		return in
	}
	out := in[:e.maxStates]
	for i := range out {
		out[i] = cloneRuntimeState(out[i])
	}
	return out
}

// rowsNextLoop 识别 `for rows.Next() { ... }` 并返回查询结果（rtRows）。
func (e *rtEval) rowsNextLoop(frame *rtFrame, cond ast.Expr, s *rtState) (*ast.Ident, rtValue, bool) {
	call, ok := cond.(*ast.CallExpr)
	if !ok {
		return nil, rtValue{}, false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Next" {
		return nil, rtValue{}, false
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return nil, rtValue{}, false
	}
	v := e.evalIdent(frame, id, s)
	if v.kind != rtRows {
		return nil, rtValue{}, false
	}
	return id, v, true
}

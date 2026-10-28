package action

// todo25 §13：运行时快照的符号执行（语句层）。

import (
	"context"
	"go/ast"
	"go/token"
)

type rtState struct {
	env   map[*ast.Object]rtValue
	types map[*ast.Object]*runtimeType
	ret   *rtReturn
}

type runtimeType struct {
	pkgPath string
	name    string
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
	// err U02：状态预算耗尽等不可继续的符号执行错误（首个错误即失败，
	// 不得静默丢尾部状态后继续判定分支）。
	err error
	// ctx 取消信号（U02：已开始执行后的取消必须停下，不产出半棵确定树）。
	ctx context.Context
	// steps T14：解释器步数预算（函数/语句/表达式统一计数；耗尽即失败，
	// 不返回截断后的"成功"结果）。
	steps int
	stack map[string]bool
}

const (
	// runtimeEvalMaxDepth 解释器最大调用深度（T14）。
	runtimeEvalMaxDepth = 16
	// runtimeEvalMaxSteps 解释器步数预算（T14）。
	runtimeEvalMaxSteps = 10000
)

func (e *rtEval) execBlock(frame *rtFrame, stmts []ast.Stmt, states []*rtState, depth int) []*rtState {
	if e.err != nil {
		return nil
	}
	if e.canceled() {
		return nil
	}
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
	if e.canceled() || !e.step() {
		return nil
	}
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
					if rt := e.runtimeTypeFromExpr(frame, vs.Values[i], s, depth); rt != nil {
						s.types[name.Obj] = rt
					}
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
					if isRuntimeStructType(vs.Type) || e.runtimeStructTypeName(frame, runtimeTypeNameOfExpr(vs.Type)) {
						s.env[name.Obj] = rtRowValue(map[string]rtValue{}, nil)
					} else {
						s.env[name.Obj] = rtUnknownValue()
					}
				}
			}
		}
	}
	return []*rtState{s}
}

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

// recordAssignTypes 记录赋值/短声明变量的轻量类型（方法调用解析用）。

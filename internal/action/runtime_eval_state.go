package action

// todo25 §13：运行时状态赋值/克隆/类型记录（从 runtime_eval.go 拆出）。

import (
	"fmt"
	"go/ast"
)

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
	out := &rtState{ret: s.ret, env: make(map[*ast.Object]rtValue, len(s.env)), types: make(map[*ast.Object]*runtimeType, len(s.types))}
	for k, v := range s.env {
		out.env[k] = cloneRuntimeValue(v)
	}
	for k, v := range s.types {
		out.types[k] = v
	}
	return out
}

func (e *rtEval) capStates(in []*rtState) []*rtState {
	if e.maxStates <= 0 || len(in) <= e.maxStates {
		return in
	}
	// U02：超过预算不能静默截断——丢掉尾部状态会把本可达的分支判成
	// excluded（7 个未知 bool → 128 状态，被截到 64 后 n==0 永不可达）。
	// 明确拒绝整次查询（stdout 为空、非零退出）。
	if e.err == nil {
		e.err = fmt.Errorf("runtime-data: analysis state budget exhausted (%d)", e.maxStates)
	}
	return in
}

// rowsNextLoop 识别 `for rows.Next() { ... }` 并返回查询结果（rtRows）。
func (e *rtEval) recordAssignTypes(frame *rtFrame, s *rtState, x *ast.AssignStmt, rhsIdx, depth int) {
	if rhsIdx >= len(x.Rhs) {
		return
	}
	for i, lhs := range x.Lhs {
		id, ok := lhs.(*ast.Ident)
		if !ok || id.Obj == nil {
			continue
		}
		var src ast.Expr
		if len(x.Rhs) == 1 {
			src = x.Rhs[0]
		} else if i < len(x.Rhs) {
			src = x.Rhs[i]
		}
		if src == nil {
			continue
		}
		if rt := e.runtimeTypeFromExpr(frame, src, s, depth); rt != nil {
			s.types[id.Obj] = rt
		}
	}
}

func (e *rtEval) runtimeStructTypeName(frame *rtFrame, name string) bool {
	if name == "" || frame == nil {
		return false
	}
	_, _, ok := seqFindStructInPackage(e.req, frame.ctx.rel, name)
	return ok
}

// cloneRuntimeValue 深拷贝可变容器（rtRow/rtMap/rtSlice），避免成功/失败
// 状态共享 map 后相互覆盖（Scan 多目标曾因此把成功值改成 unknown）。
func cloneRuntimeValue(v rtValue) rtValue {
	switch v.kind {
	case rtRow:
		cols := make(map[string]rtValue, len(v.cols))
		for k, cv := range v.cols {
			cols[k] = cloneRuntimeValue(cv)
		}
		return rtValue{kind: rtRow, cols: cols, order: append([]string(nil), v.order...), typeName: v.typeName}
	case rtMap:
		cols := make(map[string]rtValue, len(v.cols))
		for k, cv := range v.cols {
			cols[k] = cloneRuntimeValue(cv)
		}
		return rtValue{kind: rtMap, cols: cols, order: append([]string(nil), v.order...)}
	case rtSlice, rtRows, rtTuple:
		elems := make([]rtValue, len(v.elems))
		for i, ev := range v.elems {
			elems[i] = cloneRuntimeValue(ev)
		}
		return rtValue{kind: v.kind, elems: elems}
	}
	return v
}

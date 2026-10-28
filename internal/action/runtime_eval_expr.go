package action

// todo25 §13：运行时快照的表达式求值（字面量/常量/比较/逻辑/内建转换）。

import (
	"go/ast"
	"go/token"
	"strconv"
)

func (e *rtEval) evalExpr(frame *rtFrame, expr ast.Expr, s *rtState, depth int) (rtValue, []*rtState) {
	if expr == nil {
		return rtUnknownValue(), []*rtState{s}
	}
	switch x := expr.(type) {
	case *ast.BasicLit:
		return evalRuntimeLiteral(x), []*rtState{s}
	case *ast.Ident:
		return e.evalIdent(frame, x, s), []*rtState{s}
	case *ast.ParenExpr:
		return e.evalExpr(frame, x.X, s, depth)
	case *ast.UnaryExpr:
		v, variants := e.evalExpr(frame, x.X, s, depth)
		if len(variants) > 0 {
			s = variants[0]
		}
		switch x.Op {
		case token.NOT:
			truth, known := rtTruth(v)
			if !known {
				return rtUnknownValue(), variants
			}
			return rtBoolValue(!truth), variants
		case token.SUB:
			if v.kind == rtInt {
				return rtIntValue(-v.i), variants
			}
			if v.kind == rtFloat {
				return rtFloatValue(-v.f), variants
			}
		case token.AND:
			// &T{...} / &struct{...}{...}：结构体实例（字段值来自复合字面量）。
			if cl, ok := runtimeUnparen(x.X).(*ast.CompositeLit); ok {
				if _, isMap := cl.Type.(*ast.MapType); !isMap {
					return e.evalRuntimeStructLit(frame, cl, s, depth)
				}
			}
		}
		return rtUnknownValue(), variants
	case *ast.BinaryExpr:
		return e.evalBinary(frame, x, s, depth)
	case *ast.CallExpr:
		return e.evalCall(frame, x, s, depth)
	case *ast.SelectorExpr:
		if id, ok := x.X.(*ast.Ident); ok && id.Obj != nil && id.Obj.Kind == ast.Pkg {
			return rtUnknownValue(), []*rtState{s}
		}
		v, variants := e.evalExpr(frame, x.X, s, depth)
		if v.kind == rtRow {
			if fv, ok := v.cols[x.Sel.Name]; ok {
				return fv, variants
			}
		}
		// U03：reflect 字段元信息 —— field.Tag（struct tag 文本）。
		if v.kind == rtFieldInfo && x.Sel.Name == "Tag" {
			if tv, ok := v.cols["tag"]; ok {
				return rtValue{kind: rtTagInfo, s: tv.s}, variants
			}
			return rtValue{kind: rtTagInfo}, variants
		}
		return rtUnknownValue(), variants
	case *ast.IndexExpr:
		base, variants := e.evalExpr(frame, x.X, s, depth)
		if len(variants) > 0 {
			s = variants[0]
		}
		key, kvar := e.evalExpr(frame, x.Index, s, depth)
		if len(kvar) > 0 {
			s = kvar[0]
		}
		if base.kind == rtMap && key.known() {
			if v, ok := base.cols[key.rtDisplay()]; ok {
				return v, []*rtState{s}
			}
			return rtUnknownValue(), []*rtState{s}
		}
		if len(variants) > 0 {
			s = variants[0]
		}
		idx, _ := e.evalExpr(frame, x.Index, s, depth)
		if (base.kind == rtSlice || base.kind == rtRows) && idx.kind == rtInt &&
			idx.i >= 0 && int(idx.i) < len(base.elems) {
			return base.elems[idx.i], variants
		}
		return rtUnknownValue(), variants
	case *ast.CompositeLit:
		if _, ok := x.Type.(*ast.MapType); ok {
			return rtMapValue(map[string]rtValue{}), []*rtState{s}
		}
		if isRuntimeStructType(x.Type) {
			v, variants := e.evalRuntimeStructLit(frame, x, s, depth)
			return v, variants
		}
		if len(x.Elts) == 0 {
			return rtSliceValue(nil), []*rtState{s}
		}
		var elems []rtValue
		for _, el := range x.Elts {
			v, _ := e.evalExpr(frame, el, s, depth)
			elems = append(elems, v)
		}
		return rtSliceValue(elems), []*rtState{s}
	}
	return rtUnknownValue(), []*rtState{s}
}

func evalRuntimeLiteral(lit *ast.BasicLit) rtValue {
	switch lit.Kind {
	case token.INT:
		i, err := strconv.ParseInt(lit.Value, 0, 64)
		if err != nil {
			return rtUnknownValue()
		}
		return rtIntValue(i)
	case token.FLOAT:
		f, err := strconv.ParseFloat(lit.Value, 64)
		if err != nil {
			return rtUnknownValue()
		}
		return rtFloatValue(f)
	case token.STRING:
		s, err := strconv.Unquote(lit.Value)
		if err != nil {
			return rtUnknownValue()
		}
		return rtStringValue(s)
	case token.CHAR:
		s, err := strconv.Unquote(lit.Value)
		if err != nil || len(s) == 0 {
			return rtUnknownValue()
		}
		return rtIntValue(int64([]rune(s)[0]))
	}
	return rtUnknownValue()
}

func (e *rtEval) evalIdent(frame *rtFrame, id *ast.Ident, s *rtState) rtValue {
	if id == nil {
		return rtUnknownValue()
	}
	if id.Obj != nil {
		if v, ok := s.env[id.Obj]; ok {
			return v
		}
		switch id.Obj.Kind {
		case ast.Con:
			if v, ok := e.evalConst(frame, id, s, 0); ok {
				return v
			}
		case ast.Var:
			sym := "symbol:go:" + frame.pkgPath + ":" + id.Name
			if _, bound := e.an.snap.Bindings[sym]; bound {
				return rtDBValue(sym)
			}
			return e.an.globalByNameOf(id).value
		}
		return rtUnknownValue()
	}
	switch id.Name {
	case "true":
		return rtBoolValue(true)
	case "false":
		return rtBoolValue(false)
	case "nil":
		return rtNullValue()
	}
	return rtUnknownValue()
}

func (e *rtEval) evalConst(frame *rtFrame, id *ast.Ident, s *rtState, depth int) (rtValue, bool) {
	if depth > 8 || id.Obj == nil {
		return rtValue{}, false
	}
	vs, ok := id.Obj.Decl.(*ast.ValueSpec)
	if !ok {
		return rtValue{}, false
	}
	for i, name := range vs.Names {
		if name.Name != id.Name || i >= len(vs.Values) {
			continue
		}
		v, _ := e.evalExpr(frame, vs.Values[i], s, depth+1)
		return v, v.known()
	}
	return rtValue{}, false
}

func (e *rtEval) evalBinary(frame *rtFrame, x *ast.BinaryExpr, s *rtState, depth int) (rtValue, []*rtState) {
	lv, lvars := e.evalExpr(frame, x.X, s, depth)
	if len(lvars) == 0 {
		lvars = []*rtState{s}
	}
	if x.Op == token.LAND || x.Op == token.LOR {
		lt, lk := rtTruth(lv)
		if lk && ((x.Op == token.LAND && !lt) || (x.Op == token.LOR && lt)) {
			return rtBoolValue(lt), lvars
		}
		var out []*rtState
		var val rtValue = rtUnknownValue()
		for _, st := range lvars {
			rv, rvars := e.evalExpr(frame, x.Y, st, depth)
			if len(rvars) > 0 {
				st = rvars[0]
			}
			rt, rk := rtTruth(rv)
			if lk && rk {
				val = rtBoolValue(rt)
			}
			out = append(out, st)
		}
		return val, out
	}
	var out []*rtState
	var val rtValue = rtUnknownValue()
	for _, st := range lvars {
		rv, rvars := e.evalExpr(frame, x.Y, st, depth)
		if len(rvars) > 0 {
			st = rvars[0]
		}
		switch x.Op {
		case token.EQL:
			if eq, known := rtEqual(lv, rv); known {
				val = rtBoolValue(eq)
			}
		case token.NEQ:
			if eq, known := rtEqual(lv, rv); known {
				val = rtBoolValue(!eq)
			}
		case token.LSS, token.LEQ, token.GTR, token.GEQ:
			if cmp, known := rtCompare(lv, rv); known {
				switch x.Op {
				case token.LSS:
					val = rtBoolValue(cmp < 0)
				case token.LEQ:
					val = rtBoolValue(cmp <= 0)
				case token.GTR:
					val = rtBoolValue(cmp > 0)
				case token.GEQ:
					val = rtBoolValue(cmp >= 0)
				}
			}
		case token.ADD:
			if lv.kind == rtString && rv.kind == rtString {
				val = rtStringValue(lv.s + rv.s)
				break
			}
			if lv.kind == rtInt && rv.kind == rtInt {
				val = rtIntValue(lv.i + rv.i)
			}
		case token.SUB, token.MUL:
			if lv.kind == rtInt && rv.kind == rtInt {
				switch x.Op {
				case token.ADD:
					val = rtIntValue(lv.i + rv.i)
				case token.SUB:
					val = rtIntValue(lv.i - rv.i)
				case token.MUL:
					val = rtIntValue(lv.i * rv.i)
				}
			}
		}
		out = append(out, st)
	}
	return val, out
}

// evalRuntimeStructLit 结构体复合字面量：keyed 字段（生成 DAO/工厂常见形态）。
func (e *rtEval) evalRuntimeStructLit(frame *rtFrame, cl *ast.CompositeLit, s *rtState, depth int) (rtValue, []*rtState) {
	row := rtRowValue(map[string]rtValue{}, nil)
	row.typeName = runtimeTypeNameOfExpr(cl.Type)
	for _, el := range cl.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		v, variants := e.evalExpr(frame, kv.Value, s, depth)
		if len(variants) > 0 {
			s = variants[0]
		}
		row.cols[key.Name] = v
		if !containsRuntimeString(row.order, key.Name) {
			row.order = append(row.order, key.Name)
		}
	}
	return row, []*rtState{s}
}

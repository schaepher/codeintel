package action

// U04c：标量来源的语句记录与逃逸标记（取址/闭包写者/赋值/声明/形参绑定）。

import (
	"go/ast"
	"go/token"
)

// seqMarkScalarEscapes 语句内取址/闭包写者 → 对应声明槽持续逃逸。
// 在语句展开**之前**调用（同一语句内也立即生效）。
func seqMarkScalarEscapes(req CodeSequenceRequest, st ast.Stmt) {
	if req.env == nil || st == nil {
		return
	}
	inspectWrites := func(body *ast.BlockStmt) {
		ast.Inspect(body, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.AssignStmt:
				for _, lhs := range v.Lhs {
					seqMarkEscapeIdent(req.env, lhs)
				}
			case *ast.IncDecStmt:
				seqMarkEscapeIdent(req.env, v.X)
			case *ast.UnaryExpr:
				if v.Op == token.AND {
					seqMarkEscapeIdentAt(req.env, v.X, v.Pos())
				}
			}
			return true
		})
	}
	ast.Inspect(st, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.UnaryExpr:
			if v.Op == token.AND {
				seqMarkEscapeIdentAt(req.env, v.X, v.Pos())
			}
		case *ast.FuncLit:
			inspectWrites(v.Body)
			// 闭包写者的失效从字面量出现处起（同一语句里更早求值的实参仍可用）。
			seqMarkClosureEscapePositions(req.env, v)
			return false // 字面量体单独遍历（已含嵌套写者）
		}
		return true
	})
}

// seqMarkEscapeIdent 目标为标识符时标记逃逸（字段/下标取址忽略——标量
// 槽只按标识符对象跟踪）。
func seqMarkEscapeIdent(env *seqEnv, e ast.Expr) {
	seqMarkEscapeIdentAt(env, e, token.NoPos)
}

// seqMarkEscapeIdentAt 从指定位置起标记逃逸。
func seqMarkEscapeIdentAt(env *seqEnv, e ast.Expr, pos token.Pos) {
	if id, ok := e.(*ast.Ident); ok && id.Obj != nil {
		env.markScalarEscapedAt(id.Obj, pos)
	}
}

// seqMarkClosureEscapePositions 闭包里的写者按**字面量出现位置**失效。
func seqMarkClosureEscapePositions(env *seqEnv, lit *ast.FuncLit) {
	if env == nil || lit == nil || lit.Body == nil {
		return
	}
	ast.Inspect(lit.Body, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.AssignStmt:
			for _, lhs := range v.Lhs {
				seqMarkEscapeIdentAt(env, lhs, lit.Pos())
			}
		case *ast.IncDecStmt:
			seqMarkEscapeIdentAt(env, v.X, lit.Pos())
		case *ast.UnaryExpr:
			if v.Op == token.AND {
				seqMarkEscapeIdentAt(env, v.X, lit.Pos())
			}
		}
		return true
	})
}

// seqRecordScalarStatement 赋值/声明/incdec/range 的标量来源记录。
// 在语句的调用节点生成**之后**调用（RHS 读旧值，LHS 从下一条语句起生效）。
func seqRecordScalarStatement(req CodeSequenceRequest, st ast.Stmt) {
	if req.env == nil || st == nil {
		return
	}
	switch s := st.(type) {
	case *ast.AssignStmt:
		switch s.Tok {
		case token.ASSIGN, token.DEFINE:
			seqRecordScalarAssign(req, s)
		default:
			// compound（+= 等）不能保留循环前/赋值前来源。
			for _, lhs := range s.Lhs {
				if id, ok := lhs.(*ast.Ident); ok && id.Obj != nil {
					req.env.setScalar(id.Obj, nil)
				}
			}
		}
	case *ast.IncDecStmt:
		if id, ok := s.X.(*ast.Ident); ok && id.Obj != nil {
			req.env.setScalar(id.Obj, nil)
		}
	case *ast.DeclStmt:
		gd, ok := s.Decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			return
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			seqRecordScalarValueSpec(req, vs)
		}
	case *ast.RangeStmt:
		// range 元素来源未证明；键/值（含 := 与 = 两种形态）都置未知。
		for _, e := range []ast.Expr{s.Key, s.Value} {
			if id, ok := e.(*ast.Ident); ok && id.Obj != nil {
				req.env.setScalar(id.Obj, nil)
			}
		}
	}
}

// seqRecordScalarAssign 先算全部 RHS（旧环境）再写 LHS（多重赋值语义）。
func seqRecordScalarAssign(req CodeSequenceRequest, s *ast.AssignStmt) {
	origins := make([]*SeqStorageOrigin, len(s.Rhs))
	if len(s.Rhs) == len(s.Lhs) {
		for i := range s.Rhs {
			origins[i] = seqScalarOriginOf(req, s.Rhs[i])
		}
	}
	for i, lhs := range s.Lhs {
		id, ok := lhs.(*ast.Ident)
		if !ok || id.Obj == nil {
			continue
		}
		if len(s.Rhs) != len(s.Lhs) {
			// 多返回值/多重赋值未能逐个对齐 → 未知（但保留逃逸状态）。
			req.env.setScalar(id.Obj, nil)
			continue
		}
		req.env.setScalar(id.Obj, origins[i])
	}
}

// seqRecordScalarValueSpec `var x = expr` / `var x T`。
func seqRecordScalarValueSpec(req CodeSequenceRequest, vs *ast.ValueSpec) {
	for i, name := range vs.Names {
		if name.Obj == nil {
			continue
		}
		if i < len(vs.Values) {
			req.env.setScalar(name.Obj, seqScalarOriginOf(req, vs.Values[i]))
			continue
		}
		req.env.setScalar(name.Obj, nil)
	}
}

// seqBindEntryParamOrigins 入口参数 → parameter 来源（symbol 必须是**本次
// 查询入口**，其它入口的同名参数不能借用）。
func seqBindEntryParamOrigins(req CodeSequenceRequest, env *seqEnv, params *ast.FieldList,
	fset *token.FileSet, src []byte, symbol string) {
	if env == nil || params == nil || symbol == "" || req.entryID != symbol {
		return
	}
	idx := 0
	for _, field := range params.List {
		typeName := seqExprText(fset, src, field.Type)
		names := field.Names
		if len(names) == 0 {
			idx++
			continue
		}
		for _, name := range names {
			if name.Obj != nil {
				env.setScalar(name.Obj, seqOriginParameter(symbol, req.RepoAbs, typeName, idx))
			}
			idx++
		}
	}
}

// seqBindParamOrigins 被调函数形参 ← 调用点实参来源（按位置；缺失即未知）。
func seqBindParamOrigins(env *seqEnv, params *ast.FieldList, origins []*SeqStorageOrigin) {
	if env == nil || params == nil {
		return
	}
	idx := 0
	for _, field := range params.List {
		names := field.Names
		if len(names) == 0 {
			idx++
			continue
		}
		_, variadic := field.Type.(*ast.Ellipsis)
		for _, name := range names {
			if name.Obj != nil {
				if variadic {
					env.setScalar(name.Obj, nil)
					continue
				}
				if idx < len(origins) {
					env.setScalar(name.Obj, origins[idx])
				} else {
					env.setScalar(name.Obj, nil)
				}
			}
			idx++
		}
	}
}

// seqPropagatePendingParams 被调形参继承调用点实参的待解析来源（depth=1）。
func seqPropagatePendingParams(req CodeSequenceRequest, params *ast.FieldList) {
	if req.acc == nil || params == nil || len(req.callArgObjs) == 0 {
		return
	}
	idx := 0
	for _, field := range params.List {
		names := field.Names
		if len(names) == 0 {
			idx++
			continue
		}
		for _, name := range names {
			if name.Obj != nil && idx < len(req.callArgObjs) {
				seqPropagatePendingVar(req, req.callArgObjs[idx], name.Obj)
			}
			idx++
		}
	}
}

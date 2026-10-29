package action

// todo27 T05：局部实例字段的**顺序敏感**到达定义。
//
// 形态：`box := &Box{}` → `box.Set(NewOne())` → `box.Get().Work()`。
// 与"整个函数里所有 setter 候选"（工厂路径）不同，这里只看调用点之前的
// 写入顺序：最后一次写入决定字段值；调用前覆写用新来源；调用后的写入不倒推
// 污染调用前；重复 setter 取最后一次。
//
// 保守失效（返回 unknown）：实例取址逃逸（`&box`/`*p = …`）、未知方法调用
// （可能经 receiver 改写字段）、条件分支内的写入（执行与否未证明）、
// 实例被重新赋值为另一对象。

import (
	"go/ast"
)

// seqLocalInstanceField 局部实例字段的到达定义结果。
type seqLocalInstanceField struct {
	types   []writeSource // 最后一次写入的具体类型来源（可能多个 = 未消歧）
	unknown bool          // 写入来源未证明（逃逸/未知方法/条件写入/未知实参）
	ok      bool          // 是否是"局部实例 + 声明类型具体"的形态
}

// seqLocalInstanceFieldValue 计算调用点之前 `obj.field` 的到达定义。
// atPos 为读取点位置（同函数内，按源码顺序；位置在读取点之后的写入
// 不倒推污染读取）。
func (a *Actions) seqLocalInstanceFieldValue(req CodeSequenceRequest, f *ast.File,
	curPkg string, obj *ast.Object, field string, atOff int) seqLocalInstanceField {
	var res seqLocalInstanceField
	if f == nil || obj == nil || req.curBody == nil || req.curCtx == nil || req.curCtx.fset == nil {
		return res
	}
	_, typeName, ok := seqLocalInstanceDecl(f, obj)
	if !ok || typeName == "" {
		return res
	}
	res.ok = true
	imports := importAliases(f)

	// 地址逃逸：本次函数里出现 `&obj` → 指针写入可能改写实例。
	if seqFuncValueAddrTaken(req, &ast.Ident{Obj: obj, Name: obj.Name}) {
		res.unknown = true
		return res
	}

	writerParams := a.seqFieldWriterParamsForType(req, typeName, field)
	var last []writeSource
	writes := 0

	var walk func(stmts []ast.Stmt, conditional bool)
	walk = func(stmts []ast.Stmt, conditional bool) {
		for _, st := range stmts {
			if res.unknown {
				return
			}
			// 语句结束位置超过读取点 → 该语句包含读取（或在其后）：用
			// 语句起点比较会漏掉"读取所在的语句"（单行多语句夹具）。
			if atOff >= 0 && req.curCtx.fset.Position(st.End()).Offset > atOff {
				return
			}
			dbgBefore := res.unknown
			_ = dbgBefore
			switch s := st.(type) {
			case *ast.AssignStmt:
				for i, lhs := range s.Lhs {
					if i >= len(s.Rhs) {
						continue
					}
					if seqAssignTargetsObj(lhs, obj) {
						// 整个实例重绑定（`box = &Box{}`）：旧字段值失效。
						if !conditional {
							last, writes = nil, 0
							continue
						}
						res.unknown = true
						return
					}
					if sel, ok := lhs.(*ast.SelectorExpr); ok && sel.Sel.Name == field {
						if id, ok2 := sel.X.(*ast.Ident); !ok2 || id.Obj != obj {
							continue
						}
						srcs := rhsTypes(a, req, f, imports, s.Rhs[i], 3, curPkg)
						if conditional {
							res.unknown = true // 写入是否发生未证明
							return
						}
						writes++
						if len(srcs) == 0 {
							res.unknown = true
							return
						}
						last = srcs
					}
				}
			case *ast.ExprStmt:
				call, ok := s.X.(*ast.CallExpr)
				if !ok {
					continue
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					continue
				}
				id, ok := sel.X.(*ast.Ident)
				if !ok || id.Obj != obj {
					// 实例作为实参逃逸（可能被写）→ 未证明。
					if seqCallPassesObj(call, obj) {
						res.unknown = true
					}
					continue
				}
				if conditional {
					res.unknown = true
					return
				}
				idx, isWriter := writerParams[sel.Sel.Name]
				if !isWriter {
					// 未知方法（可能经 receiver 改写字段）——除非该方法有源码
					// 证明只读本字段（返回该字段的 getter）。
					if !a.seqMethodReadOnlyFor(req, typeName, sel.Sel.Name, field) {
						res.unknown = true
						return
					}
					continue
				}
				if idx < 0 || idx >= len(call.Args) {
					res.unknown = true
					return
				}
				srcs := rhsTypes(a, req, f, imports, call.Args[idx], 3, curPkg)
				writes++
				if len(srcs) == 0 {
					res.unknown = true
					return
				}
				last = srcs
			case *ast.IfStmt:
				if s.Init != nil {
					walk([]ast.Stmt{s.Init}, conditional)
				}
				walk(s.Body.List, true)
				if s.Else != nil {
					switch e := s.Else.(type) {
					case *ast.BlockStmt:
						walk(e.List, true)
					case *ast.IfStmt:
						walk([]ast.Stmt{e}, conditional)
					}
				}
			case *ast.ForStmt:
				walk(s.Body.List, true)
			case *ast.RangeStmt:
				walk(s.Body.List, true)
			case *ast.SwitchStmt:
				walk(s.Body.List, true)
			case *ast.BlockStmt:
				walk(s.List, conditional)
			}
		}
	}
	walk(req.curBody.List, false)
	if res.unknown {
		return res
	}
	if writes == 0 {
		// 没有任何写入：不是本路径的证明（交给字段写者/接口边界路径）。
		res.ok = false
		return res
	}
	res.types = last
	return res
}

// seqLocalInstanceDecl 定位局部实例声明 → (声明行, 具体类型名)。

// seqCompositeTypeName `&T{}` / `T{}` / `new(T)` → 类型名。

// seqTypeNameOfExpr 类型表达式 → 类型名（Ident / pkg.T / *T）。

// seqAssignTargetsObj 赋值左值是否是实例本身（`box = ...` / `*p` 不做处理）。

// seqCallPassesObj 调用是否把实例作为实参传入（可能逃逸被改写）。

// seqFieldWriterParamsForType 类型上写该字段的方法 → 被写实参下标。

// seqMethodReadOnlyFor 方法是否可证明只读该字段（方法体 return <recv>.<field>，
// 且没有对字段的赋值）——用于把 getter 视为不改变字段值的调用。

// seqCallReceiverFields 调用点 receiver 实例的字段到达定义（仅局部实例；
// 其它形态返回 nil，交给工厂/字段写者路径）。
func seqCallReceiverFields(a *Actions, req CodeSequenceRequest, fun ast.Expr,
	targetID string, f *ast.File, curPkg string, atOff int) map[string]seqLocalInstanceField {
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok || id.Obj == nil || f == nil {
		return nil
	}
	// 当前 receiver 已经携带字段状态 → 沿调用链继续传递。
	if id.Obj == req.curRecvObj && len(req.curRecvFields) > 0 {
		return req.curRecvFields
	}
	_, typeName, ok := seqLocalInstanceDecl(f, id.Obj)
	if !ok || typeName == "" {
		return nil
	}
	fields := seqStructFieldNames(f, typeName)
	if len(fields) == 0 {
		return nil
	}
	out := map[string]seqLocalInstanceField{}
	for _, field := range fields {
		st := a.seqLocalInstanceFieldValue(req, f, curPkg, id.Obj, field, atOff)
		if st.ok {
			out[field] = st
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// seqStructFieldNames 同文件内类型声明的字段名（结构体/嵌入字段）。

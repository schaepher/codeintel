package action

// U04j/U04k/U04l：字段槽失效规则（普通写入/整对象覆写/方法调用/捕获写者/
// 调度体）。
//
// 只清"当前 Scan 得到的字段列值"；已经复制到普通标量变量的历史来源不受影响
// （由标量槽独立保存）。

import (
	"go/ast"
	"go/token"
)

// seqRecordFieldUpdates 语句后处理：字段写入/方法调用/捕获写者使字段槽失效。
// 与输出格式/焦点无关（分析事实始终记录）。
func seqRecordFieldUpdates(req CodeSequenceRequest, st ast.Stmt) {
	if req.env == nil || st == nil {
		return
	}
	switch s := st.(type) {
	case *ast.AssignStmt:
		for _, lhs := range s.Lhs {
			seqInvalidateFieldLHS(req, lhs, s.Tok)
		}
		seqEscapeStoredAllocations(req, s)
	case *ast.IncDecStmt:
		seqInvalidateFieldLHS(req, s.X, s.Tok)
	case *ast.SendStmt:
		// 通道发送：对象交给另一个并发方。
		seqEscapeOriginAlloc(req, s.Value)
	}
	ast.Inspect(st, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.CallExpr:
			seqInvalidateByCall(req, v)
		case *ast.FuncLit:
			// 捕获写者：闭包体里对字段的写入在字面量出现处起使旧列值失效。
			seqInvalidateCapturedWrites(req, v)
			return false
		}
		return true
	})
}

// seqInvalidateFieldLHS 赋值目标对字段槽的影响。
func seqInvalidateFieldLHS(req CodeSequenceRequest, lhs ast.Expr, tok token.Token) {
	if req.env == nil || lhs == nil {
		return
	}
	switch x := runtimeUnparen(lhs).(type) {
	case *ast.Ident:
		if x.Obj == nil {
			return
		}
		if tok == token.ASSIGN || tok == token.DEFINE {
			if alloc := seqAllocOriginOf(req, x); alloc != nil {
				// 指针变量重绑：只切换分配，不清旧别名引用的对象状态；新分配
				// 从下一条语句起才有来源（fresh unknown）。
				return
			}
			req.env.clearObjFieldSlotsDeep(x.Obj)
			return
		}
		// 复合赋值（+= 等）对结构体同样清除。
		req.env.clearObjFieldSlotsDeep(x.Obj)
	case *ast.SelectorExpr:
		id, ok := runtimeUnparen(x.X).(*ast.Ident)
		if !ok || id.Obj == nil {
			return
		}
		if alloc := seqAllocOriginOf(req, id); alloc != nil {
			req.env.clearAllocFieldDeep(alloc.ID, x.Sel.Name)
			return
		}
		req.env.clearObjFieldDeep(id.Obj, x.Sel.Name)
	case *ast.StarExpr:
		id, ok := runtimeUnparen(x.X).(*ast.Ident)
		if !ok || id.Obj == nil {
			return
		}
		// `*alias = Record{}`：整值覆写同一分配。
		if alloc := seqAllocOriginOf(req, id); alloc != nil {
			req.env.clearAllocFieldSlotsDeep(alloc.ID)
			return
		}
		req.env.clearObjFieldSlotsDeep(id.Obj)
	}
}

// seqEscapeStoredAllocations 直接把分配存入全局/字段/容器/接口等不透明位置
// 时标为传出（`Global = record` / `s.rec = record` / `x = any`）。
func seqEscapeStoredAllocations(req CodeSequenceRequest, s *ast.AssignStmt) {
	if req.env == nil || s == nil || len(s.Lhs) != len(s.Rhs) {
		return
	}
	for i, lhs := range s.Lhs {
		origin := seqScalarOriginOf(req, s.Rhs[i])
		if origin == nil || origin.Kind != SeqOriginAllocation || origin.Allocation == nil {
			continue
		}
		l := runtimeUnparen(lhs)
		if s.Tok == token.DEFINE {
			// 局部短声明是别名（同一分配）。
			continue
		}
		if id, ok := l.(*ast.Ident); ok {
			if id.Obj == nil {
				continue
			}
			if seqAllocOriginOf(req, id) != nil {
				continue // 指针变量重绑/别名
			}
			if seqIdentLocalDecl(req, id) && seqIdentDeclaredPointer(id) {
				continue // 新声明/既有局部指针别名
			}
			seqMarkAllocEscaped(req, origin.Allocation)
			continue
		}
		// 字段/下标/解引用目标：不透明存储。
		seqMarkAllocEscaped(req, origin.Allocation)
	}
}

// seqEscapeOriginAlloc 单个表达式位置把分配交给外部（通道发送等）。
func seqEscapeOriginAlloc(req CodeSequenceRequest, e ast.Expr) {
	origin := seqScalarOriginOf(req, e)
	if origin != nil && origin.Kind == SeqOriginAllocation && origin.Allocation != nil {
		seqMarkAllocEscaped(req, origin.Allocation)
	}
}

// seqIdentLocalDecl 标识符声明是否在当前函数体内（非包级全局）。
func seqIdentLocalDecl(req CodeSequenceRequest, id *ast.Ident) bool {
	if id == nil || id.Obj == nil || req.curBody == nil {
		return false
	}
	pos := seqDeclPosOf(id.Obj.Decl)
	return pos.IsValid() && pos >= req.curBody.Pos() && pos <= req.curBody.End()
}

// seqIdentDeclaredPointer 标识符声明的类型是否为指针。
func seqIdentDeclaredPointer(id *ast.Ident) bool {
	if id == nil || id.Obj == nil {
		return false
	}
	var typ ast.Expr
	switch d := id.Obj.Decl.(type) {
	case *ast.Field:
		typ = d.Type
	case *ast.ValueSpec:
		typ = d.Type
	}
	if typ == nil {
		return false
	}
	_, ok := runtimeUnparen(typ).(*ast.StarExpr)
	return ok
}

// seqInvalidateByCall 调用对字段槽的影响（未证明只读即失效）。
func seqInvalidateByCall(req CodeSequenceRequest, call *ast.CallExpr) {
	if req.env == nil || call == nil {
		return
	}
	// 标准 Scan 的地址传递不保留指针（本次目标可以绑定）；其接收者是读取
	// 结果，不需要按未知调用失效。其它自定义 Scanner 仍走普通规则。
	if seqIsProvenScanCall(req, call) {
		return
	}
	readOnly := seqReadOnlyGetterCall(req, call)
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok && !readOnly {
		if id, ok2 := runtimeUnparen(sel.X).(*ast.Ident); ok2 && id.Obj != nil {
			seqInvalidateReceiver(req, id)
		}
	}
	if readOnly {
		return
	}
	for _, arg := range call.Args {
		seqInvalidateEscapedArg(req, arg)
	}
}

// seqInvalidateReceiver 方法接收者（隐式地址）使旧列值失效并标分配传出。
func seqInvalidateReceiver(req CodeSequenceRequest, id *ast.Ident) {
	if alloc := seqAllocOriginOf(req, id); alloc != nil {
		seqMarkAllocEscaped(req, alloc)
		return
	}
	req.env.clearObjFieldSlotsDeep(id.Obj)
}

// seqInvalidateEscapedArg 实参把对象/分配交给未知调用 → 传出。
func seqInvalidateEscapedArg(req CodeSequenceRequest, arg ast.Expr) {
	a := runtimeUnparen(arg)
	if u, ok := a.(*ast.UnaryExpr); ok && u.Op == token.AND {
		a = runtimeUnparen(u.X)
	}
	switch x := a.(type) {
	case *ast.Ident:
		if x.Obj == nil {
			return
		}
		if alloc := seqAllocOriginOf(req, x); alloc != nil {
			seqMarkAllocEscaped(req, alloc)
			return
		}
		// 值类型对象取址交给未知调用：保守清除字段槽（对象可能被改写）。
		req.env.clearObjFieldSlotsDeep(x.Obj)
	case *ast.SelectorExpr:
		// `&record.Kind` 交给未知调用：该字段值失效。
		id, ok := runtimeUnparen(x.X).(*ast.Ident)
		if !ok || id.Obj == nil {
			return
		}
		if alloc := seqAllocOriginOf(req, id); alloc != nil {
			req.env.clearAllocFieldDeep(alloc.ID, x.Sel.Name)
			return
		}
		req.env.clearObjFieldDeep(id.Obj, x.Sel.Name)
	}
}

// seqInvalidateCapturedWrites 闭包体内的字段写入/方法调用在创建处失效。
func seqInvalidateCapturedWrites(req CodeSequenceRequest, lit *ast.FuncLit) {
	if req.env == nil || lit == nil || lit.Body == nil {
		return
	}
	ast.Inspect(lit.Body, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.AssignStmt:
			for _, lhs := range v.Lhs {
				seqInvalidateFieldLHS(req, lhs, v.Tok)
			}
		case *ast.IncDecStmt:
			seqInvalidateFieldLHS(req, v.X, v.Tok)
		case *ast.CallExpr:
			// 闭包内的方法调用同样可能改写捕获对象。
			if sel, ok := v.Fun.(*ast.SelectorExpr); ok {
				if id, ok2 := runtimeUnparen(sel.X).(*ast.Ident); ok2 && id.Obj != nil {
					seqInvalidateReceiver(req, id)
				}
			}
		}
		return true
	})
}

// seqIsProvenScanCall 该调用是否已证明为标准 Row.Scan/Rows.Scan。
func seqIsProvenScanCall(req CodeSequenceRequest, call *ast.CallExpr) bool {
	node := seqCallNodeOf(req, call)
	return node != nil && node.Evidence != nil && node.Evidence.Scan != nil
}

// seqReadOnlyGetterCall 只读字段 getter 证明（零参、单 return receiver.Field、
// 内建标量且与字段声明类型一致）。
func seqReadOnlyGetterCall(req CodeSequenceRequest, call *ast.CallExpr) bool {
	if call == nil || len(call.Args) > 0 || req.curCtx == nil {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	base := seqFieldBaseOrigin(req, sel.X)
	if base == nil {
		return false
	}
	fn, _, ok := seqMethodDeclOf(req, base.ref, sel.Sel.Name)
	if !ok || fn == nil || fn.Body == nil || len(fn.Body.List) != 1 {
		return false
	}
	ret, ok := fn.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return false
	}
	fs, ok := runtimeUnparen(ret.Results[0]).(*ast.SelectorExpr)
	if !ok {
		return false
	}
	if fn.Recv == nil || len(fn.Recv.List) == 0 || len(fn.Recv.List[0].Names) == 0 {
		return false
	}
	recvID, ok := runtimeUnparen(fs.X).(*ast.Ident)
	if !ok || recvID.Name != fn.Recv.List[0].Names[0].Name {
		return false
	}
	field, ok := seqResolveFieldDecl(req, base.ref, fs.Sel.Name)
	if !ok || field.iface || !seqBuiltinTypeName(field.goType) {
		return false
	}
	return true
}

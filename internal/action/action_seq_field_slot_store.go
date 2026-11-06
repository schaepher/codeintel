package action

// 不透明存储/全局存储的分配传出（从 action_seq_field_slot_invalidate.go 拆出）。

import (
	"go/ast"
	"go/token"
)

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

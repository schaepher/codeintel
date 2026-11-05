package action

// U04e：Scan 字段目标解析、列来源与错误槽（从 action_seq_scan.go 拆出）。

import (
	"go/ast"
	"go/token"
)

// seqScanFieldDeclType 字段目标的准确声明类型（值模型或分配根）。
func seqScanFieldDeclType(req CodeSequenceRequest, id *ast.Ident, field string,
	callPos token.Pos) (string, bool) {
	if id == nil || id.Obj == nil {
		return "", false
	}
	alloc := seqAllocOriginForScan(req, id, callPos)
	if alloc != nil {
		if !seqAllocMatchesRequest(req, alloc) || req.env.allocEscaped(alloc.ID) {
			return "", false
		}
	}
	_, typ, ok := seqFieldTypeFromDecl(req, id, field, callPos)
	return typ, ok
}

// seqClearScanFieldTarget 目标不支持/列数不符时清掉旧列值（不能沿用）。
func seqClearScanFieldTarget(req CodeSequenceRequest, id *ast.Ident, field string) {
	if req.env == nil || id == nil || id.Obj == nil {
		return
	}
	if alloc := seqAllocOriginOf(req, id); alloc != nil {
		req.env.clearAllocField(alloc.ID, field)
		return
	}
	req.env.clearObjField(id.Obj, field)
}

// seqScanArgIdent `&x` → x（其它形态不支持）。
func seqScanArgIdent(arg ast.Expr) *ast.Ident {
	u, ok := runtimeUnparen(arg).(*ast.UnaryExpr)
	if !ok || u.Op != token.AND {
		return nil
	}
	id, ok := runtimeUnparen(u.X).(*ast.Ident)
	if !ok {
		return nil
	}
	return id
}

// seqScanTargetType 目标的声明类型（只接受内建标量；无显式类型/指针/复合
// 类型一律不支持）。
func seqScanTargetType(id *ast.Ident) (string, bool) {
	if id == nil || id.Obj == nil {
		return "", false
	}
	var typ ast.Expr
	switch d := id.Obj.Decl.(type) {
	case *ast.Field:
		typ = d.Type
	case *ast.ValueSpec:
		typ = d.Type
	}
	if typ == nil {
		return "", false
	}
	t := runtimeUnparen(typ)
	if _, ok := t.(*ast.StarExpr); ok {
		return "", false
	}
	name, ok := t.(*ast.Ident)
	if !ok || !seqBuiltinTypeName(name.Name) {
		return "", false
	}
	switch name.Name {
	case "error", "any", "complex64", "complex128":
		return "", false
	}
	return name.Name, true
}

// seqReadColumnOrigin 目标 → read_column 来源（同一 Scan 身份）。
func seqReadColumnOrigin(scan *SeqScan, t SeqScanTarget, scalarType string) (*SeqStorageOrigin, bool) {
	if scan == nil || t.Column == "" {
		return nil, false
	}
	col := &SeqReadColumn{Result: scan, Column: t.Column, Index: t.Index, When: SeqScanWhenSuccess}
	return &SeqStorageOrigin{Kind: SeqOriginReadColumn, ScalarType: scalarType, ReadColumn: col}, true
}

// seqScanErrorOriginAt 调用是已证明的 Scan → 错误返回槽来源（槽 0 = error）。
func seqScanErrorOriginAt(req CodeSequenceRequest, call *ast.CallExpr) *SeqStorageOrigin {
	node := seqCallNodeOf(req, call)
	if node == nil || node.Evidence == nil || node.Evidence.Scan == nil {
		return nil
	}
	cp := *node.Evidence.Scan
	return &SeqStorageOrigin{Kind: SeqOriginScanError, ScalarType: "error", ScanResult: &cp}
}

// seqBindReadResultOrigin 多槽赋值里槽 0 = 读取结果身份（`rows, err :=
// DB.Query(...)`；错误槽保持未知）。
func seqBindReadResultOrigin(req CodeSequenceRequest, call *ast.CallExpr, lhs []ast.Expr) {
	if req.env == nil || call == nil || len(lhs) == 0 {
		return
	}
	id, ok := lhs[0].(*ast.Ident)
	if !ok || id.Obj == nil || id.Name == "_" {
		return
	}
	if o := seqReadResultOriginAt(req, call); o != nil {
		req.env.setScalar(id.Obj, o)
	}
}

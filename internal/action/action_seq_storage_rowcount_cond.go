package action

// U04j：影响行数变量的零值比较条件。

import (
	"go/ast"
	"go/token"
)

// seqRowCondition 行数变量与零值的比较条件（右侧/左侧零值均可、支持取反）。
func seqRowCondition(req CodeSequenceRequest, cond ast.Expr) *SeqRowCountCondition {
	expr, invert := seqUnwrapNegation(cond)
	cmp, ok := expr.(*ast.BinaryExpr)
	if !ok {
		return nil
	}
	ref, op, ok := seqRowZeroComparison(req, cmp)
	if !ok {
		return nil
	}
	trueRes, falseRes := seqRowsCompareOutcome(op)
	if invert {
		trueRes, falseRes = falseRes, trueRes
	}
	return &SeqRowCountCondition{Result: ref, WhenTrue: trueRes, WhenFalse: falseRes}
}

// seqRowZeroComparison 比较式是否为 row <op> 0（或 0 <op> row）。
func seqRowZeroComparison(req CodeSequenceRequest, cmp *ast.BinaryExpr) (*SeqORMRowCountResult, token.Token, bool) {
	if isZeroLiteral(cmp.Y) {
		if ref := seqRowsRefOf(req, cmp.X); ref != nil {
			return ref, cmp.Op, true
		}
	}
	if isZeroLiteral(cmp.X) {
		if ref := seqRowsRefOf(req, cmp.Y); ref != nil {
			return ref, seqFlipCompareOp(cmp.Op), true
		}
	}
	return nil, 0, false
}

// seqRowsRefOf 表达式的行数来源（变量槽优先，其次已证明的 write_row_count 标量来源）。
func seqRowsRefOf(req CodeSequenceRequest, e ast.Expr) *SeqORMRowCountResult {
	id, ok := e.(*ast.Ident)
	if !ok || id.Obj == nil || req.env == nil {
		return nil
	}
	if ref := req.env.rowsResultOf(id.Obj, id.Pos()); ref != nil {
		return ref
	}
	origin, found := req.env.scalarOriginAt(id.Obj, id.Pos())
	if !found || origin == nil || origin.RowCount == nil {
		return nil
	}
	return cloneSeqRowCount(origin.RowCount)
}

func isZeroLiteral(e ast.Expr) bool {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.INT {
		return false
	}
	return lit.Value == "0"
}

// seqFlipCompareOp 左右交换后的运算符。
func seqFlipCompareOp(op token.Token) token.Token {
	switch op {
	case token.LSS:
		return token.GTR
	case token.GTR:
		return token.LSS
	case token.LEQ:
		return token.GEQ
	case token.GEQ:
		return token.LEQ
	}
	return op
}

// seqRowsCompareOutcome 运算符 → 真假结果名。
func seqRowsCompareOutcome(op token.Token) (string, string) {
	switch op {
	case token.GTR:
		return SeqRowsGTZero, SeqRowsLEZero
	case token.LEQ:
		return SeqRowsLEZero, SeqRowsGTZero
	case token.GEQ:
		return SeqRowsGEZero, SeqRowsLTZero
	case token.LSS:
		return SeqRowsLTZero, SeqRowsGEZero
	case token.EQL:
		return SeqRowsEQZero, SeqRowsNEZero
	case token.NEQ:
		return SeqRowsNEZero, SeqRowsEQZero
	}
	return "", ""
}

// seqRowCondSpec 条件里的行数变量与真假结果（不看是否已有 ref）。
func seqRowCondSpec(req CodeSequenceRequest, cond ast.Expr) (*ast.Object, string, string, bool) {
	expr, invert := seqUnwrapNegation(cond)
	cmp, ok := expr.(*ast.BinaryExpr)
	if !ok {
		return nil, "", "", false
	}
	var obj *ast.Object
	op := cmp.Op
	switch {
	case isZeroLiteral(cmp.Y):
		if id, ok := cmp.X.(*ast.Ident); ok && id.Obj != nil {
			obj = id.Obj
		}
	case isZeroLiteral(cmp.X):
		if id, ok := cmp.Y.(*ast.Ident); ok && id.Obj != nil {
			obj = id.Obj
			op = seqFlipCompareOp(op)
		}
	}
	if obj == nil {
		return nil, "", "", false
	}
	trueRes, falseRes := seqRowsCompareOutcome(op)
	if trueRes == "" {
		return nil, "", "", false
	}
	if invert {
		trueRes, falseRes = falseRes, trueRes
	}
	return obj, trueRes, falseRes, true
}

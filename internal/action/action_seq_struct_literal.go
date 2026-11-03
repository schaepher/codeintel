package action

// U04w：结构体字面量的初始化来源（struct_literal）。
//
// semantics=initialization_only：这是构造时的来源，不是当前字段值、对象相等、
// ORM 列映射、生成 ID、执行成功或跨入口因果。

import (
	"go/ast"
)

// 构造来源常量与预算。
const (
	SeqOriginStructLiteral      = "struct_literal"
	SeqStructLiteralSemantics   = "initialization_only"
	seqStructLiteralMaxFields   = 64
	seqStructLiteralMaxDepth    = 8
	seqStructLiteralMaxObserved = 256
)

// SeqStructLiteralField 字面量的一个显式字段（name + 值来源）。
type SeqStructLiteralField struct {
	Name   string            `json:"name"`
	Origin *SeqStorageOrigin `json:"origin,omitempty"`
}

// SeqStorageStructLiteral 构造观察（请求内独立 ID）。
type SeqStorageStructLiteral struct {
	ID        string                  `json:"id"`
	TypeID    string                  `json:"type_id"`
	Semantics string                  `json:"semantics"`
	File      string                  `json:"file,omitempty"`
	Offset    int                     `json:"offset,omitempty"`
	Fields    []SeqStructLiteralField `json:"fields,omitempty"`
}

// seqNextStructLiteralID 构造观察 ID（超出预算 → 空，调用方省略来源）。
func seqNextStructLiteralID(req CodeSequenceRequest) string {
	if req.acc == nil || req.acc.structLitSeq >= seqStructLiteralMaxObserved {
		return ""
	}
	req.acc.structLitSeq++
	return "struct-literal-" + itoaSeq(req.acc.structLitSeq)
}

// seqStructLiteralOrigin 字面量 → 初始化来源（含字段来源）。
func seqStructLiteralOrigin(req CodeSequenceRequest, lit *ast.CompositeLit, depth int) *SeqStorageOrigin {
	if lit == nil || req.curCtx == nil || req.curCtx.file == nil || depth >= seqStructLiteralMaxDepth {
		return nil
	}
	// 位置式字段/无显式字段：不扩展语义（省略来源）。
	if len(lit.Elts) == 0 {
		return nil
	}
	if len(lit.Elts) > seqStructLiteralMaxFields {
		return nil
	}
	for _, el := range lit.Elts {
		if _, ok := el.(*ast.KeyValueExpr); !ok {
			return nil
		}
	}
	// 具名结构体（真正的别名跟随；泛型/局部同名类型拒绝）。
	c := &seqPersistCtx{file: req.curCtx.file, pkgPath: pkgPathOf(req.curSym),
		src: req.curCtx.src, fset: req.curCtx.fset, repoAbs: req.RepoAbs,
		fileRel: req.curCtx.rel, req: &req}
	ref, ok := seqORMTypeExprRef(c, lit.Type, req.curCtx.file, c.pkgPath, 0)
	if !ok {
		return nil
	}
	owner, st, tparams, ok := seqResolveStructType(req, ref, req.curCtx.rel)
	if !ok || tparams || st == nil {
		return nil
	}
	id := seqNextStructLiteralID(req)
	if id == "" {
		return nil
	}
	out := &SeqStorageStructLiteral{ID: id,
		TypeID:    "symbol:go:" + owner.pkgPath + ":" + owner.name,
		Semantics: SeqStructLiteralSemantics, File: req.curCtx.rel,
		Offset: req.curCtx.fset.Position(lit.Lbrace).Offset}
	for _, el := range lit.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name == "" {
			return nil
		}
		field := SeqStructLiteralField{Name: key.Name}
		if valueOrigin := seqStructLiteralFieldOrigin(req, kv.Value, depth+1); valueOrigin != nil {
			field.Origin = valueOrigin
		}
		out.Fields = append(out.Fields, field)
	}
	return &SeqStorageOrigin{Kind: SeqOriginStructLiteral, StructLiteral: out}
}

// seqStructLiteralFieldOrigin 字段值来源（不重跑构造函数/getter）。
func seqStructLiteralFieldOrigin(req CodeSequenceRequest, e ast.Expr, depth int) *SeqStorageOrigin {
	if e == nil || depth >= seqStructLiteralMaxDepth {
		return nil
	}
	if lit, ok := runtimeUnparen(e).(*ast.CompositeLit); ok {
		return seqStructLiteralOrigin(req, lit, depth+1)
	}
	return seqScalarOriginOf(req, e)
}

package action

// U04x：整值复制的构造祖先（struct_copy, construction_ancestry_only）。
//
// 只证明"这份值来自那次构造"：不是当前字段值、对象相等、列映射、生成键、
// 事务提交或跨入口因果。

import (
	"go/ast"
	"go/token"

	"github.com/schaepher/codeintel/internal/domain"
)

// 常量与预算。
const (
	SeqOriginStructCopy      = "struct_copy"
	SeqStructCopySemantics   = "construction_ancestry_only"
	seqStructCopyMaxDepth    = 8
	seqStructCopyMaxObserved = 256
)

// SeqStorageStructCopy 一次整值复制观察（请求内独立 ID）。
type SeqStorageStructCopy struct {
	ID        string            `json:"id"`
	TypeID    string            `json:"type_id"`
	Semantics string            `json:"semantics"`
	File      string            `json:"file,omitempty"`
	Offset    int               `json:"offset,omitempty"`
	Source    *SeqStorageOrigin `json:"source,omitempty"`
	Guard     string            `json:"guard,omitempty"`
	// Conditional 写入位于条件分支（可能未执行）。
	Conditional bool `json:"conditional,omitempty"`
}

// seqNextStructCopyID 复制观察 ID（超出预算省略来源）。
func seqNextStructCopyID(req CodeSequenceRequest) string {
	if req.acc == nil || req.acc.structCopySeq >= seqStructCopyMaxObserved {
		return ""
	}
	req.acc.structCopySeq++
	return "struct-copy-" + itoaSeq(req.acc.structCopySeq)
}

// seqRecordDerefCopies 语句里的 `*dst = <expr>` 整值复制（U04x）。
func seqRecordDerefCopies(req CodeSequenceRequest, st ast.Stmt) {
	if req.env == nil || st == nil {
		return
	}
	as, ok := st.(*ast.AssignStmt)
	if !ok {
		return
	}
	// 多重赋值先固定全部 RHS（旧环境），再更新 LHS。
	origins := make([]*SeqStorageOrigin, len(as.Rhs))
	for i := range as.Rhs {
		origins[i] = seqDerefValueOrigin(req, as.Rhs[i])
	}
	for i, lhs := range as.Lhs {
		star, ok := lhs.(*ast.StarExpr)
		if !ok {
			continue
		}
		dst, ok := star.X.(*ast.Ident)
		if !ok || dst.Obj == nil || i >= len(origins) || origins[i] == nil {
			continue
		}
		seqRecordDerefCopy(req, dst, origins[i], as.Rhs[i], as)
	}
}

// seqRecordDerefCopy 校验目标/来源类型一致后记录复制祖先。
func seqRecordDerefCopy(req CodeSequenceRequest, dst *ast.Ident, src *SeqStorageOrigin,
	rhs ast.Expr, at *ast.AssignStmt) {
	if src.Kind == SeqOriginWriteError || src.Kind == SeqOriginWriteRowCount ||
		src.Kind == SeqOriginTxError || src.Kind == SeqOriginTransaction ||
		src.Kind == SeqOriginSQLResult {
		return
	}
	dstRef, dstOK := seqIdentPointerTargetType(req, dst)
	srcRef, srcOK := seqDerefSourceType(req, rhs, src)
	if dstOK && srcOK {
		if dstRef.pkgPath != srcRef.pkgPath || dstRef.name != srcRef.name {
			return // 指针层级/类型不符
		}
	}
	if !dstOK || !srcOK {
		return // 任一侧未证明 → 不建立祖先
	}
	if req.branchCond != "" {
		// U04x：条件分支内的复制可能未执行——不升级为无条件祖先。
		return
	}
	id := seqNextStructCopyID(req)
	if id == "" {
		return
	}
	out := &SeqStorageStructCopy{ID: id, TypeID: "symbol:go:" + dstRef.pkgPath + ":" + dstRef.name,
		Semantics: SeqStructCopySemantics, Source: cloneSeqScalarOrigin(src)}
	if req.curCtx != nil {
		out.File = req.curCtx.rel
		out.Offset = req.curCtx.fset.Position(at.Pos()).Offset
	}
	req.env.setScalar(dst.Obj, &SeqStorageOrigin{Kind: SeqOriginStructCopy,
		ScalarType: src.ScalarType, StructCopy: out})
}

// seqIdentPointerTargetType `new(T)`/`&T{}` 指针变量的目标类型 T。
func seqIdentPointerTargetType(req CodeSequenceRequest, id *ast.Ident) (seqORMTypeRef, bool) {
	if id == nil || id.Obj == nil || req.curCtx == nil {
		return seqORMTypeRef{}, false
	}
	if origin, found := req.env.scalarOriginAt(id.Obj, id.Pos()); found && origin != nil &&
		origin.Kind == SeqOriginStructCopy {
		return seqTypeRefOfCopyTypeID(origin.StructCopy), true
	}
	// `clone := new(T)`：new 必须未被遮蔽，且来源类型由初始化表达式证明。
	vs, ok := id.Obj.Decl.(*ast.AssignStmt)
	if !ok {
		return seqORMTypeRef{}, false
	}
	for i, lhs := range vs.Lhs {
		lid, ok := lhs.(*ast.Ident)
		if !ok || lid.Obj != id.Obj || i >= len(vs.Rhs) {
			continue
		}
		call, ok := runtimeUnparen(vs.Rhs[i]).(*ast.CallExpr)
		if !ok {
			return seqORMTypeRef{}, false
		}
		fid, ok := call.Fun.(*ast.Ident)
		if !ok || fid.Name != "new" || fid.Obj != nil || len(call.Args) != 1 {
			return seqORMTypeRef{}, false
		}
		c := &seqPersistCtx{file: req.curCtx.file, pkgPath: pkgPathOf(req.curSym),
			src: req.curCtx.src, fset: req.curCtx.fset, repoAbs: req.RepoAbs,
			fileRel: req.curCtx.rel, req: &req}
		return seqORMTypeExprRef(c, call.Args[0], req.curCtx.file, c.pkgPath, 0)
	}
	return seqORMTypeRef{}, false
}

// seqDerefValueOrigin `*p` / 值变量 → 被复制值的来源。
func seqDerefValueOrigin(req CodeSequenceRequest, rhs ast.Expr) *SeqStorageOrigin {
	switch v := runtimeUnparen(rhs).(type) {
	case *ast.StarExpr:
		if id, ok := v.X.(*ast.Ident); ok {
			return seqScalarOriginOf(req, id)
		}
		return nil
	case *ast.Ident:
		return seqScalarOriginOf(req, v)
	}
	return seqScalarOriginOf(req, rhs)
}

// seqDerefSourceType RHS 的类型（`*p` 取 pointee；值拷贝取值类型）。
func seqDerefSourceType(req CodeSequenceRequest, rhs ast.Expr, src *SeqStorageOrigin) (seqORMTypeRef, bool) {
	if src.Kind == SeqOriginStructCopy && src.StructCopy != nil {
		return seqTypeRefOfCopyTypeID(src.StructCopy), true
	}
	if src.Kind == SeqOriginStructLiteral && src.StructLiteral != nil {
		return seqTypeRefOfSymbolID(src.StructLiteral.TypeID), true
	}
	if originRef, ok := seqOriginTypeRef(req, src); ok {
		return originRef, true
	}
	// `*record`：形参/变量指针的 pointee 类型。
	if star, ok := runtimeUnparen(rhs).(*ast.StarExpr); ok {
		if id, ok := star.X.(*ast.Ident); ok && id.Obj != nil {
			if ref, ok := seqIdentStructType(req, id); ok {
				return ref, true
			}
		}
	}
	// RHS 为标识符（值）：取声明结构体类型。
	if id, ok := runtimeUnparen(rhs).(*ast.Ident); ok {
		return seqIdentStructType(req, id)
	}
	return seqORMTypeRef{}, false
}

// seqOriginTypeRef 来源里的类型引用（parameter/scalar 用声明类型）。
func seqOriginTypeRef(req CodeSequenceRequest, src *SeqStorageOrigin) (seqORMTypeRef, bool) {
	switch src.Kind {
	case SeqOriginParameter:
		return seqORMTypeRef{}, false // 形参类型需按使用点声明解析
	}
	return seqORMTypeRef{}, false
}

// seqTypeRefOfCopyTypeID 复制观察的 type_id → 类型引用。
func seqTypeRefOfCopyTypeID(c *SeqStorageStructCopy) seqORMTypeRef {
	if c == nil {
		return seqORMTypeRef{}
	}
	return seqTypeRefOfSymbolID(c.TypeID)
}

// seqTypeRefOfSymbolID canonical ID → 类型引用。
func seqTypeRefOfSymbolID(id string) seqORMTypeRef {
	return seqORMTypeRef{pkgPath: pkgOfID(domain.CanonicalID(id)), name: pkgNameOfID(id)}
}

// seqDeclPosOf 声明节点的起始位置（不同 Decl 形态）。
func seqDeclPosOf(decl interface{}) token.Pos {
	switch d := decl.(type) {
	case *ast.Field:
		return d.Pos()
	case *ast.ValueSpec:
		return d.Pos()
	case *ast.AssignStmt:
		return d.Pos()
	case *ast.TypeSpec:
		return d.Pos()
	case *ast.FuncDecl:
		return d.Pos()
	}
	return token.NoPos
}

var _ = token.NoPos

// seqMergeImmediateClosureEnv 立即执行的函数字面量：把捕获变量上的写入合并
// 回外层环境（参数/字面量内局部变量不回写）。go/defer 调度体不经过这里。
func seqMergeImmediateClosureEnv(parent, child *seqEnv, lit *ast.FuncLit) {
	if parent == nil || child == nil || lit == nil || child.scalars == nil {
		return
	}
	inLit := func(obj *ast.Object) bool {
		if obj == nil || obj.Decl == nil {
			return false
		}
		pos := seqDeclPosOf(obj.Decl)
		return pos.IsValid() && pos >= lit.Pos() && pos <= lit.End()
	}
	for obj, slot := range child.scalars {
		if inLit(obj) {
			continue
		}
		if parent.scalars == nil {
			parent.scalars = map[*ast.Object]seqScalarSlot{}
		}
		parent.scalars[obj] = slot
	}
}

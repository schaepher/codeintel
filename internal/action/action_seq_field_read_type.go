package action

// U04v：字段读取的类型声明解析（别名跟随、泛型/接口拒绝）。

import (
	"go/ast"
	"go/token"

	"github.com/schaepher/codeintel/internal/domain"
)

// seqResolveStructType 类型引用 → 结构体声明（别名跟随；泛型拒绝）。
func seqResolveStructType(req CodeSequenceRequest, ref seqORMTypeRef,
	relFile string) (seqORMTypeRef, *seqStructTypeWrap, bool, bool) {
	seen := map[string]bool{}
	cur := ref
	for i := 0; i < seqORMModelMaxDepth; i++ {
		key := cur.pkgPath + ":" + cur.name
		if seen[key] {
			return seqORMTypeRef{}, nil, false, false
		}
		seen[key] = true
		files := seqPkgFilesForReq(req, cur.pkgPath, relFile)
		var found *seqStructTypeWrap
		for _, ctx := range files {
			if ctx == nil || ctx.file == nil || seqFileHasBuildConstraint(ctx) {
				continue
			}
			ts := fileTypeSpec(ctx.file, cur.name)
			if ts == nil {
				continue
			}
			if ts.TypeParams != nil {
				return seqORMTypeRef{}, nil, true, false
			}
			if ts.Assign.IsValid() {
				c := &seqPersistCtx{file: ctx.file, pkgPath: cur.pkgPath, src: ctx.src,
					fset: ctx.fset, repoAbs: req.RepoAbs, fileRel: ctx.rel, req: &req}
				next, ok := seqORMTypeExprRef(c, ts.Type, ctx.file, cur.pkgPath, 0)
				if !ok {
					return seqORMTypeRef{}, nil, false, false
				}
				cur = next
				found = nil
				break
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				return seqORMTypeRef{}, nil, false, false
			}
			found = &seqStructTypeWrap{fields: st.Fields, file: ctx}
			break
		}
		if found != nil {
			return cur, found, false, true
		}
	}
	return seqORMTypeRef{}, nil, false, false
}

// seqFieldReadValueRef 字段读取后下一级表达式的类型引用（U04i：优先用
// 缓存的值类型；缺失时只能从准确 owner + 最后一级字段声明恢复）。
func seqFieldReadValueRef(req CodeSequenceRequest, fr *SeqStorageFieldRead) (seqORMTypeRef, bool) {
	if fr == nil {
		return seqORMTypeRef{}, false
	}
	if fr.valueRef.pkgPath != "" && fr.valueRef.name != "" {
		return fr.valueRef, true
	}
	ownerID := domain.CanonicalID(fr.TypeID)
	owner := seqORMTypeRef{pkgPath: pkgOfID(ownerID), name: pkgNameOfID(string(ownerID))}
	if owner.pkgPath == "" || owner.name == "" {
		return seqORMTypeRef{}, false
	}
	if len(fr.Path) == 0 {
		return owner, true
	}
	field, ok := seqResolveFieldDecl(req, owner, fr.Path[len(fr.Path)-1])
	if !ok {
		// 字段声明不可解析：保留 owner（保守，不借用外层同名字段）。
		return owner, true
	}
	return field.ref, true
}

// seqNextFieldReadID 请求内读取 ID（每次求值一个）。
func seqNextFieldReadID(req CodeSequenceRequest) string {
	if req.acc == nil {
		return ""
	}
	req.acc.fieldReadSeq++
	return "field-read-" + itoaSeq(req.acc.fieldReadSeq)
}

// seqFieldReadOrigin `x.Field`（或其链式嵌套）→ field_read 来源。
func seqFieldReadOrigin(req CodeSequenceRequest, sel *ast.SelectorExpr) *SeqStorageOrigin {
	if sel == nil || req.curCtx == nil || req.curCtx.file == nil {
		return nil
	}
	base := seqFieldBaseOrigin(req, sel.X)
	if base == nil {
		return nil
	}
	return seqFieldStepFrom(req, base, sel)
}

// seqCanonicalTypeRef 沿**类型别名**（`type X = Y`）链解析到准确声明类型
// （U04i）。新定义类型（`type X Y`，无 Assign）保持自身——不能借用原类型
// 的方法集合。别名环/超深度时保留当前引用（保守，不猜更深一层）。
func seqCanonicalTypeRef(req CodeSequenceRequest, ref seqORMTypeRef) seqORMTypeRef {
	seen := map[string]bool{}
	cur := ref
	for i := 0; i < seqORMModelMaxDepth; i++ {
		if cur.pkgPath == "" || cur.name == "" {
			return cur
		}
		key := cur.pkgPath + ":" + cur.name
		if seen[key] {
			return cur
		}
		seen[key] = true
		var alias ast.Expr
		var aliasCtx *seqFileCtx
		for _, ctx := range seqPkgFilesForReq(req, cur.pkgPath, "") {
			if ctx == nil || ctx.file == nil || seqFileHasBuildConstraint(ctx) {
				continue
			}
			ts := fileTypeSpec(ctx.file, cur.name)
			if ts == nil {
				continue
			}
			if ts.Assign.IsValid() {
				alias, aliasCtx = ts.Type, ctx
			}
			break
		}
		if alias == nil || aliasCtx == nil {
			return cur
		}
		c := &seqPersistCtx{file: aliasCtx.file, pkgPath: cur.pkgPath, src: aliasCtx.src,
			fset: aliasCtx.fset, repoAbs: req.RepoAbs, fileRel: aliasCtx.rel, req: &req}
		next, ok := seqORMTypeExprRef(c, alias, aliasCtx.file, cur.pkgPath, 0)
		if !ok {
			return cur
		}
		cur = next
	}
	return cur
}

// seqRefIsIfaceDecl 类型引用是否是接口声明（本包/外部）。
func seqRefIsIfaceDecl(req CodeSequenceRequest, ref seqORMTypeRef) bool {
	for _, ctx := range seqPkgFilesForReq(req, ref.pkgPath, "") {
		if ctx == nil || ctx.file == nil || seqFileHasBuildConstraint(ctx) {
			continue
		}
		if ts := fileTypeSpec(ctx.file, ref.name); ts != nil {
			if _, ok := ts.Type.(*ast.InterfaceType); ok {
				return true
			}
		}
	}
	return false
}

// cloneSeqFieldRoot 字段根深拷贝。
func cloneSeqFieldRoot(r *SeqFieldRoot) *SeqFieldRoot {
	if r == nil {
		return nil
	}
	cp := *r
	return &cp
}

// pkgNameOfID `symbol:go:pkg:T` → T。
func pkgNameOfID(id string) string {
	for i := len(id) - 1; i >= 0; i-- {
		if id[i] == ':' {
			return id[i+1:]
		}
	}
	return ""
}

var _ = token.NoPos

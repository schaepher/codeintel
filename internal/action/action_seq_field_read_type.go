package action

// U04v：字段读取的类型声明解析（别名跟随、泛型/接口拒绝）。

import (
	"go/ast"
	"go/token"
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

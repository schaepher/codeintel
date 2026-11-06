package action

// 字段声明解析与提升字段（从 action_seq_field_read.go 拆出）。

import "go/ast"

// seqResolveFieldDecl 解析类型上的字段（声明的 owner/类型/位置；支持提升字段）。
func seqResolveFieldDecl(req CodeSequenceRequest, ref seqORMTypeRef, field string) (seqResolvedField, bool) {
	return seqResolveFieldDeclDepth(req, ref, field, 0)
}

func seqResolveFieldDeclDepth(req CodeSequenceRequest, ref seqORMTypeRef, field string,
	depth int) (seqResolvedField, bool) {
	if depth > 4 {
		return seqResolvedField{}, false
	}
	ownerRef, st, tparams, ok := seqResolveStructType(req, ref, "")
	if !ok || tparams || st == nil || st.fields == nil {
		return seqResolvedField{}, false
	}
	for _, fl := range st.fields.List {
		if len(fl.Names) == 0 {
			t := fl.Type
			if star, ok := t.(*ast.StarExpr); ok {
				t = star.X
			}
			if exprTypeName(t) != field {
				continue
			}
			return seqFieldFromExpr(req, ownerRef, st, t, "")
		}
		for _, n := range fl.Names {
			if n.Name != field {
				continue
			}
			return seqFieldFromExpr(req, ownerRef, st, fl.Type, n.Name)
		}
	}
	// 提升字段：在嵌入的值类型结构体里唯一命中才接受（歧义 → 未知）。
	var found seqResolvedField
	count := 0
	for _, fl := range st.fields.List {
		if len(fl.Names) != 0 {
			continue
		}
		t := runtimeUnparen(fl.Type)
		if _, isStar := t.(*ast.StarExpr); isStar {
			continue
		}
		embRef, ok := seqORMTypeExprRef(&seqPersistCtx{file: st.file.file, pkgPath: ownerRef.pkgPath,
			src: st.file.src, fset: st.file.fset, req: &req, fileRel: st.file.rel},
			t, st.file.file, ownerRef.pkgPath, 0)
		if !ok {
			continue
		}
		if fd, ok := seqResolveFieldDeclDepth(req, seqCanonicalTypeRef(req, embRef), field, depth+1); ok {
			found = fd
			count++
		}
	}
	if count != 1 {
		return seqResolvedField{}, false
	}
	return found, true
}

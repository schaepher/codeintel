package action

// U04v：getter 调用的字段读取/字面量来源。

import (
	"go/ast"
)

// seqGetterOrigin 方法 getter 调用 → 字段读取/字面量来源（U04v）。
func seqGetterOrigin(req CodeSequenceRequest, call *ast.CallExpr) *SeqStorageOrigin {
	if call == nil || len(call.Args) > 0 || req.curCtx == nil {
		return nil
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	base := seqFieldBaseOrigin(req, sel.X)
	if base == nil {
		return nil
	}
	fn, declCtx, ok := seqMethodDeclOf(req, base.ref, sel.Sel.Name)
	if !ok || fn == nil || fn.Body == nil {
		return nil
	}
	// 只接受单一 return 的 getter（其余形态保守放弃）。
	if len(fn.Body.List) != 1 {
		return nil
	}
	ret, ok := fn.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return nil
	}
	expr := ret.Results[0]
	if lit, ok := expr.(*ast.BasicLit); ok {
		return seqBasicLitOrigin(lit)
	}
	// `return <recv>.<field>`：字段读取（读取位置 = getter 内的选择器）。
	if fs, ok := runtimeUnparen(expr).(*ast.SelectorExpr); ok {
		if recvBase, ok2 := seqGetterReceiverBase(req, base, fn, fs.X); ok2 {
			if out := seqFieldStepAt(req, recvBase, fs, declCtx); out != nil {
				return out
			}
		}
	}
	return nil
}

// seqGetterReceiverBase getter 返回的字段接收者就是方法 receiver。
func seqGetterReceiverBase(req CodeSequenceRequest, base *seqFieldBase, fn *ast.FuncDecl,
	e ast.Expr) (*seqFieldBase, bool) {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return nil, false
	}
	id, ok := e.(*ast.Ident)
	if !ok || len(fn.Recv.List[0].Names) == 0 || fn.Recv.List[0].Names[0].Name != id.Name {
		return nil, false
	}
	return base, true
}

// seqMethodDeclOf 类型上的方法声明（包内/外部包文件）。
func seqMethodDeclOf(req CodeSequenceRequest, ref seqORMTypeRef, method string) (*ast.FuncDecl, *seqFileCtx, bool) {
	for _, ctx := range seqPkgFilesForReq(req, ref.pkgPath, "") {
		if ctx == nil || ctx.file == nil || seqFileHasBuildConstraint(ctx) {
			continue
		}
		for _, d := range ctx.file.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv == nil || len(fd.Recv.List) == 0 || fd.Name == nil {
				continue
			}
			if fd.Name.Name == method && recvTypeName(fd.Recv.List[0].Type) == ref.name {
				return fd, ctx, true
			}
		}
	}
	return nil, nil, false
}

// seqFieldStepAt 按指定**读取位置**生成字段读取来源（getter 内读取）。
func seqFieldStepAt(req CodeSequenceRequest, base *seqFieldBase, read *ast.SelectorExpr,
	declCtx *seqFileCtx) *SeqStorageOrigin {
	if base == nil || read == nil || declCtx == nil {
		return nil
	}
	field, ok := seqResolveFieldDecl(req, base.ref, read.Sel.Name)
	if !ok || field.iface {
		return nil
	}
	id := seqNextFieldReadID(req)
	if id == "" || base.root == nil {
		return nil
	}
	return &SeqStorageOrigin{Kind: SeqOriginFieldRead, ScalarType: field.goType,
		FieldRead: &SeqStorageFieldRead{ID: id, Root: cloneSeqFieldRoot(base.root),
			Path:       append(append([]string{}, base.path...), read.Sel.Name),
			TypeID:     "symbol:go:" + field.ownerpkg + ":" + field.owner,
			SourceFile: field.sourceFile, SourceLine: field.sourceLine,
			ReadFile: declCtx.rel, ReadOffset: declCtx.fset.Position(read.Sel.Pos()).Offset,
			valueRef: seqCanonicalTypeRef(req, field.ref)}}
}

// seqPkgDeclaresType 包内是否有该文件级类型声明（跨文件引用无 Obj 时核对）。
func seqPkgDeclaresType(req CodeSequenceRequest, pkgPath, name string) bool {
	for _, ctx := range seqPkgFilesForReq(req, pkgPath, "") {
		if ctx == nil || ctx.file == nil || seqFileHasBuildConstraint(ctx) {
			continue
		}
		if fileTypeSpec(ctx.file, name) != nil {
			return true
		}
	}
	return false
}

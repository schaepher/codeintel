package action

// U04k/U04j：分配来源、值类型模型与 Scan 字段目标解析（从
// action_seq_field_slot.go 拆出；槽存储/合并在主文件）。

import (
	"go/ast"
	"go/token"
	"strings"
)

// seqNewAllocOrigin 已证明的 `new(T)` 分配来源（只接受预声明 new）。
func seqNewAllocOrigin(req CodeSequenceRequest, call *ast.CallExpr, depth int) *SeqStorageOrigin {
	if call == nil || len(call.Args) != 1 || call.Ellipsis.IsValid() {
		return nil
	}
	id, ok := call.Fun.(*ast.Ident)
	if !ok || id.Obj != nil || id.Name != "new" {
		return nil
	}
	tref, ok := seqTypeExprRefAtCursor(req, call.Args[0])
	if !ok {
		return nil
	}
	if req.acc == nil {
		return nil
	}
	req.acc.allocChecks++
	if req.acc.allocChecks > seqAllocCheckMax {
		seqFieldSlotBudgetExceeded(req)
		return nil
	}
	req.acc.allocSeq++
	alloc := &SeqAllocation{ID: "alloc-" + itoaSeq(req.acc.allocSeq),
		TypeID: "symbol:go:" + tref.pkgPath + ":" + tref.name,
		Repo:   req.RepoAbs, Entry: req.entryID, Symbol: req.curSym, File: req.curFile}
	if req.curCtx != nil && call.Lparen.IsValid() {
		alloc.Offset = req.curCtx.fset.Position(call.Lparen).Offset
	}
	return &SeqStorageOrigin{Kind: SeqOriginAllocation, ScalarType: "pointer", Allocation: alloc}
}

// seqTypeExprRefAtCursor 表达式（类型语法）→ 类型引用（当前文件上下文）。
func seqTypeExprRefAtCursor(req CodeSequenceRequest, typ ast.Expr) (seqORMTypeRef, bool) {
	if req.curCtx == nil || req.curCtx.file == nil {
		return seqORMTypeRef{}, false
	}
	c := &seqPersistCtx{file: req.curCtx.file, pkgPath: pkgPathOf(req.curSym),
		src: req.curCtx.src, fset: req.curCtx.fset, repoAbs: req.RepoAbs,
		fileRel: req.curCtx.rel, req: &req}
	return seqORMTypeExprRef(c, typ, req.curCtx.file, c.pkgPath, 0)
}

// seqValueStructTypeRef 值类型结构体变量/值参数/值字面量的准确类型引用。
// 指针根（*T / new(T)）返回 false（U04j 只支持值模型；U04k 另行证明分配）。
func seqValueStructTypeRef(req CodeSequenceRequest, id *ast.Ident, depth int) (seqORMTypeRef, bool) {
	if id == nil || id.Obj == nil || depth > seqFieldSlotDepth {
		return seqORMTypeRef{}, false
	}
	var typ ast.Expr
	var init ast.Expr
	switch d := id.Obj.Decl.(type) {
	case *ast.Field:
		typ = d.Type
	case *ast.ValueSpec:
		typ = d.Type
		for i, name := range d.Names {
			if name.Obj == id.Obj && i < len(d.Values) {
				init = d.Values[i]
			}
		}
	case *ast.AssignStmt:
		if len(d.Lhs) == len(d.Rhs) {
			for i, lhs := range d.Lhs {
				if lid, ok := lhs.(*ast.Ident); ok && lid.Obj == id.Obj {
					init = d.Rhs[i]
				}
			}
		}
	}
	if typ != nil {
		t := runtimeUnparen(typ)
		if _, isStar := t.(*ast.StarExpr); isStar {
			return seqORMTypeRef{}, false
		}
		return seqTypeExprRefAtCursor(req, t)
	}
	if init == nil {
		return seqORMTypeRef{}, false
	}
	switch x := runtimeUnparen(init).(type) {
	case *ast.CompositeLit:
		if x.Type == nil {
			return seqORMTypeRef{}, false
		}
		return seqTypeExprRefAtCursor(req, x.Type)
	case *ast.Ident:
		return seqValueStructTypeRef(req, x, depth+1)
	}
	return seqORMTypeRef{}, false
}

// seqScanFieldTarget `&record.Kind` / `&record.Detail.Kind` → (根变量, 字段路径)。
func seqScanFieldTarget(arg ast.Expr) (*ast.Ident, string, bool) {
	u, ok := runtimeUnparen(arg).(*ast.UnaryExpr)
	if !ok || u.Op != token.AND {
		return nil, "", false
	}
	id, path, ok := seqSelectorFieldPath(runtimeUnparen(u.X))
	if !ok || len(path) == 0 {
		return nil, "", false
	}
	return id, strings.Join(path, "."), true
}

// seqSelectorFieldPath 值字段选择器链 → (根标识符, 路径)。
func seqSelectorFieldPath(e ast.Expr) (*ast.Ident, []string, bool) {
	var path []string
	cur := runtimeUnparen(e)
	for {
		sel, ok := cur.(*ast.SelectorExpr)
		if !ok {
			break
		}
		path = append([]string{sel.Sel.Name}, path...)
		cur = runtimeUnparen(sel.X)
	}
	id, ok := cur.(*ast.Ident)
	if !ok {
		return nil, nil, false
	}
	return id, path, true
}

// seqBindScanFieldTarget Scan 目标为字段时建立字段槽来源；返回是否成功。
func seqBindScanFieldTarget(req CodeSequenceRequest, id *ast.Ident, field string,
	origin *SeqStorageOrigin, callPos token.Pos) bool {
	if req.env == nil || id == nil || id.Obj == nil || origin == nil {
		return false
	}
	alloc := seqAllocOriginForScan(req, id, callPos)
	if alloc != nil {
		if !seqAllocMatchesRequest(req, alloc) || req.env.allocEscaped(alloc.ID) {
			return false
		}
		// 标准 Scan 不保留指针：清除本次地址传递造成的逃逸标记（更早的仍有效）。
		req.env.clearScanPassEscape(id.Obj, callPos)
		return seqFieldSlotSet(req, seqFieldSlotKey{alloc: alloc.ID, field: field}, origin)
	}
	// 值类型模型：只接受值类型结构体根（指针根没有分配证明；类型别名、
	// 提升字段/嵌套路径不支持）。
	ref, ok := seqValueStructTypeRef(req, id, 0)
	if !ok || seqTypeRefIsAlias(req, ref) {
		return false
	}
	return seqFieldSlotSet(req, seqFieldSlotKey{obj: id.Obj, field: field}, origin)
}

// seqFieldTypeFromDecl 字段声明的内建标量类型（U04j：只支持直接标量字段）。
func seqFieldTypeFromDecl(req CodeSequenceRequest, id *ast.Ident, field string,
	callPos token.Pos) (seqORMTypeRef, string, bool) {
	ref, ok := seqValueStructTypeRef(req, id, 0)
	if ok && seqTypeRefIsAlias(req, ref) {
		return seqORMTypeRef{}, "", false
	}
	if !ok {
		// 分配根：按分配类型解析。
		alloc := seqAllocOriginForScan(req, id, callPos)
		if alloc == nil || alloc.TypeID == "" {
			return seqORMTypeRef{}, "", false
		}
		ref = seqTypeRefOfSymbolID(alloc.TypeID)
	}
	return seqFieldPathType(req, ref, field)
}

// seqFieldPathType 逐级解析字段路径（B03：中间字段必须是值类型结构体；最后
// 一级必须是直接内建标量）。
func seqFieldPathType(req CodeSequenceRequest, ref seqORMTypeRef, path string) (seqORMTypeRef, string, bool) {
	parts := strings.Split(path, ".")
	cur := ref
	for i, name := range parts {
		fd, ok := seqResolveFieldDecl(req, cur, name)
		if !ok || fd.iface {
			return seqORMTypeRef{}, "", false
		}
		if i == len(parts)-1 {
			if !seqBuiltinTypeName(fd.goType) {
				return fd.ref, fd.goType, false
			}
			return fd.ref, fd.goType, true
		}
		if fd.pointer {
			return seqORMTypeRef{}, "", false
		}
		cur = seqCanonicalTypeRef(req, fd.ref)
	}
	return seqORMTypeRef{}, "", false
}

// seqTypeRefIsAlias 该类型引用是否由类型别名声明（U04j 不支持别名根）。
func seqTypeRefIsAlias(req CodeSequenceRequest, ref seqORMTypeRef) bool {
	if ref.pkgPath == "" || ref.name == "" {
		return false
	}
	for _, ctx := range seqPkgFilesForReq(req, ref.pkgPath, "") {
		if ctx == nil || ctx.file == nil || seqFileHasBuildConstraint(ctx) {
			continue
		}
		if ts := fileTypeSpec(ctx.file, ref.name); ts != nil {
			return ts.Assign.IsValid()
		}
	}
	return false
}

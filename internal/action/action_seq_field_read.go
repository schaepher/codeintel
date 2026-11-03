package action

// U04v：请求结构体字段的声明与本次读取来源（field_read）。
//
// 只描述**读取位置与对象来源**：不是字段初始值/运行时值，也不证明跨入口
// 相等；循环中的表示读取不是逐迭代观测。

import (
	"go/ast"

	"github.com/schaepher/codeintel/internal/domain"
)

// SeqFieldRoot 字段读取的对象根（入口形参对象）。
type SeqFieldRoot struct {
	Symbol    string `json:"symbol"`
	Repo      string `json:"repo,omitempty"`
	Parameter int    `json:"parameter"`
}

// SeqStorageFieldRead 一次字段读取（请求内独立 ID）。
type SeqStorageFieldRead struct {
	ID         string        `json:"id"`
	Root       *SeqFieldRoot `json:"root"`
	Path       []string      `json:"path"`
	TypeID     string        `json:"type_id"`
	SourceFile string        `json:"source_file,omitempty"`
	SourceLine int           `json:"source_line,omitempty"`
	ReadFile   string        `json:"read_file,omitempty"`
	ReadOffset int           `json:"read_offset,omitempty"`
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

// seqFieldBaseOrigin 基表达式的来源：参数对象（直接或别名）或上一层字段读取。
func seqFieldBaseOrigin(req CodeSequenceRequest, e ast.Expr) *seqFieldBase {
	switch x := runtimeUnparen(e).(type) {
	case *ast.Ident:
		if x.Obj == nil || req.env == nil {
			return nil
		}
		origin, found := req.env.scalarOriginAt(x.Obj, x.Pos())
		if !found || origin == nil {
			return nil
		}
		switch origin.Kind {
		case SeqOriginParameter:
			ref, ok := seqIdentStructType(req, x)
			if !ok {
				return nil
			}
			root := &SeqFieldRoot{Repo: origin.Repo, Parameter: 0}
			if origin.Parameter != nil {
				root.Parameter = *origin.Parameter
			}
			root.Symbol = origin.Symbol
			return &seqFieldBase{root: root, ref: ref}
		case SeqOriginFieldRead:
			if origin.FieldRead == nil {
				return nil
			}
			return &seqFieldBase{root: origin.FieldRead.Root, ref: seqORMTypeRef{
				pkgPath: pkgOfID(domain.CanonicalID(origin.FieldRead.TypeID)), name: pkgNameOfID(origin.FieldRead.TypeID)},
				path: append([]string{}, origin.FieldRead.Path...)}
		}
	case *ast.SelectorExpr:
		if origin := seqFieldReadOrigin(req, x); origin != nil && origin.FieldRead != nil {
			return &seqFieldBase{root: origin.FieldRead.Root, ref: seqORMTypeRef{
				pkgPath: pkgOfID(domain.CanonicalID(origin.FieldRead.TypeID)), name: pkgNameOfID(origin.FieldRead.TypeID)},
				path: append([]string{}, origin.FieldRead.Path...)}
		}
	}
	return nil
}

// seqFieldBase 字段读取的基（对象根 + 当前类型 + 已有路径）。
type seqFieldBase struct {
	root *SeqFieldRoot
	ref  seqORMTypeRef
	path []string
}

// seqFieldStepFrom 在当前类型上解析字段并生成 field_read 来源。
func seqFieldStepFrom(req CodeSequenceRequest, base *seqFieldBase, sel *ast.SelectorExpr) *SeqStorageOrigin {
	if base == nil || base.ref.pkgPath == "" || sel == nil {
		return nil
	}
	field, ok := seqResolveFieldDecl(req, base.ref, sel.Sel.Name)
	if !ok {
		return nil
	}
	if field.iface {
		return nil
	}
	id := seqNextFieldReadID(req)
	if id == "" {
		return nil
	}
	root := base.root
	if root == nil {
		return nil
	}
	fr := &SeqStorageFieldRead{ID: id, Root: cloneSeqFieldRoot(root),
		Path:       append(append([]string{}, base.path...), sel.Sel.Name),
		TypeID:     "symbol:go:" + field.ownerpkg + ":" + field.owner,
		SourceFile: field.sourceFile, SourceLine: field.sourceLine,
		ReadFile: req.curCtx.rel, ReadOffset: req.curCtx.fset.Position(sel.Sel.Pos()).Offset}
	return &SeqStorageOrigin{Kind: SeqOriginFieldRead, ScalarType: field.goType, FieldRead: fr}
}

// seqResolvedField 字段声明解析结果。
type seqResolvedField struct {
	ref        seqORMTypeRef
	iface      bool
	owner      string
	ownerpkg   string
	sourceFile string
	sourceLine int
	goType     string
}

// seqResolveFieldDecl 解析类型上的字段（声明的 owner/类型/位置）。
func seqResolveFieldDecl(req CodeSequenceRequest, ref seqORMTypeRef, field string) (seqResolvedField, bool) {
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
	return seqResolvedField{}, false
}

// seqFieldFromExpr 字段声明的类型引用与位置。
func seqFieldFromExpr(req CodeSequenceRequest, owner seqORMTypeRef, st *seqStructTypeWrap,
	typ ast.Expr, name string) (seqResolvedField, bool) {
	c := &seqPersistCtx{file: st.file.file, pkgPath: owner.pkgPath, src: st.file.src,
		fset: st.file.fset, repoAbs: req.RepoAbs, fileRel: st.file.rel, req: &req}
	ref, ok := seqORMTypeExprRef(c, typ, st.file.file, owner.pkgPath, 0)
	if !ok {
		// 跨文件类型引用在 parser 里没有 Obj（`*Node` 声明在 node.go）——
		// 按包内实际声明核对后接受（局部同名类型仍有 Obj 守卫）。
		t := typ
		if star, isStar := t.(*ast.StarExpr); isStar {
			t = star.X
		}
		if id, isID := t.(*ast.Ident); isID && seqPkgDeclaresType(req, owner.pkgPath, id.Name) {
			ref, ok = seqORMTypeRef{pkgPath: owner.pkgPath, name: id.Name}, true
		}
	}
	if !ok {
		return seqResolvedField{}, false
	}
	out := seqResolvedField{ref: ref, owner: owner.name, ownerpkg: owner.pkgPath,
		iface: seqRefIsIfaceDecl(req, ref)}
	if st.file != nil && st.file.fset != nil {
		out.sourceFile = st.file.rel
		if name != "" {
			out.sourceLine = st.file.fset.Position(typ.Pos()).Line
		}
		if st.file.src != nil && typ != nil {
			start := st.file.fset.Position(typ.Pos()).Offset
			end := st.file.fset.Position(typ.End()).Offset
			if start >= 0 && end <= len(st.file.src) && start < end {
				out.goType = string(st.file.src[start:end])
			}
		}
	}
	return out, true
}

// seqIdentStructType 标识符声明类型 → 结构体类型引用（票根为参数/局部变量）。
func seqIdentStructType(req CodeSequenceRequest, id *ast.Ident) (seqORMTypeRef, bool) {
	if id == nil || id.Obj == nil || req.curCtx == nil || req.curCtx.file == nil {
		return seqORMTypeRef{}, false
	}
	var typ ast.Expr
	switch d := id.Obj.Decl.(type) {
	case *ast.Field:
		typ = d.Type
	case *ast.ValueSpec:
		typ = d.Type
	}
	if typ == nil {
		return seqORMTypeRef{}, false
	}
	c := &seqPersistCtx{file: req.curCtx.file, pkgPath: pkgPathOf(req.curSym),
		src: req.curCtx.src, fset: req.curCtx.fset, repoAbs: req.RepoAbs,
		fileRel: req.curCtx.rel, req: &req}
	ref, ok := seqORMTypeExprRef(c, typ, req.curCtx.file, c.pkgPath, 0)
	if !ok {
		return seqORMTypeRef{}, false
	}
	return ref, true
}

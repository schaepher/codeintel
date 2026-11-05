package action

// U04v：请求结构体字段的声明与本次读取来源（field_read）。
//
// 只描述**读取位置与对象来源**：不是字段初始值/运行时值，也不证明跨入口
// 相等；循环中的表示读取不是逐迭代观测。

import (
	"go/ast"
)

// SeqFieldRoot 字段读取/写入的对象根（入口形参、构造实例或整值复制历史）。
type SeqFieldRoot struct {
	// Kind parameter_object | struct_literal | struct_copy。
	Kind      string `json:"kind,omitempty"`
	Symbol    string `json:"symbol,omitempty"`
	Repo      string `json:"repo,omitempty"`
	Parameter int    `json:"parameter"`
	// TypeID/ID 构造/复制历史根（U04y：保留同次求值的历史对象身份）。
	TypeID string `json:"type_id,omitempty"`
	ID     string `json:"id,omitempty"`
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
	// valueRef U04i：该字段的**值类型**（下一级选择器/方法归属按它解析，
	// 不能退回外层同名 owner）。内部缓存——不进入 JSON 契约；来源复制时
	// 随 struct copy 保留，缺失时由 owner+最后一级字段声明恢复。
	valueRef seqORMTypeRef
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
			root := &SeqFieldRoot{Kind: "parameter_object", Repo: origin.Repo, Parameter: 0}
			if origin.Parameter != nil {
				root.Parameter = *origin.Parameter
			}
			root.Symbol = origin.Symbol
			return &seqFieldBase{root: root, ref: ref, obj: x.Obj}
		case SeqOriginFieldRead:
			if origin.FieldRead == nil {
				return nil
			}
			// U04i：下一级选择器按**字段值类型**解析（req.Child.ID 的 ID 属于
			// Leaf，不是外层 Root 的 ID）。
			ref, ok := seqFieldReadValueRef(req, origin.FieldRead)
			if !ok {
				return nil
			}
			return &seqFieldBase{root: origin.FieldRead.Root, ref: ref,
				path: append([]string{}, origin.FieldRead.Path...)}
		case SeqOriginStructLiteral:
			// U04y：构造实例作为字段读写根（initialization_only，不是当前值）。
			if origin.StructLiteral == nil {
				return nil
			}
			ref, ok := seqIdentStructType(req, x)
			if !ok {
				ref = seqTypeRefOfSymbolID(origin.StructLiteral.TypeID)
			}
			return &seqFieldBase{root: &SeqFieldRoot{Kind: "struct_literal",
				TypeID: origin.StructLiteral.TypeID, ID: origin.StructLiteral.ID}, ref: ref, obj: x.Obj}
		case SeqOriginStructCopy:
			// U04y：整值复制后的历史对象根（构造祖先，不是当前字段值）。
			if origin.StructCopy == nil {
				return nil
			}
			ref, ok := seqIdentStructType(req, x)
			if !ok {
				ref = seqTypeRefOfCopyTypeID(origin.StructCopy)
			}
			return &seqFieldBase{root: &SeqFieldRoot{Kind: "struct_copy",
				TypeID: origin.StructCopy.TypeID, ID: origin.StructCopy.ID}, ref: ref, obj: x.Obj}
		case SeqOriginAllocation:
			// U04k：new(T) 分配根（别名共享同一分配 ID）。
			if origin.Allocation == nil {
				return nil
			}
			ref, ok := seqValueStructTypeRef(req, x, 0)
			if !ok {
				ref = seqTypeRefOfSymbolID(origin.Allocation.TypeID)
			}
			return &seqFieldBase{root: &SeqFieldRoot{Kind: "allocation",
				TypeID: origin.Allocation.TypeID, ID: origin.Allocation.ID}, ref: ref,
				alloc: origin.Allocation.ID}
		}
	case *ast.SelectorExpr:
		if origin := seqFieldReadOrigin(req, x); origin != nil && origin.FieldRead != nil {
			// U04i：继续下一级选择器时用字段值类型（不是 FieldRead.type_id 的
			// 外层 owner）。
			ref, ok := seqFieldReadValueRef(req, origin.FieldRead)
			if !ok {
				return nil
			}
			return &seqFieldBase{root: origin.FieldRead.Root, ref: ref,
				path: append([]string{}, origin.FieldRead.Path...)}
		}
	case *ast.CallExpr:
		// `Get().ID = 3`：接收者是构造调用的返回值 → struct_literal 根。
		// 先按已展开的返回槽来源，再按源码单一 return 构造（续查/部署前）。
		if origin := seqScalarOriginOf(req, x); origin != nil {
			switch origin.Kind {
			case SeqOriginFieldRead:
				// U04i：getter 返回字段（`GetChild().ID`）——下一级按字段值
				// 类型解析，读取位置保留 getter 内路径 + 外层选择器。
				if origin.FieldRead == nil {
					return nil
				}
				ref, ok := seqFieldReadValueRef(req, origin.FieldRead)
				if !ok {
					return nil
				}
				return &seqFieldBase{root: origin.FieldRead.Root, ref: ref,
					path: append([]string{}, origin.FieldRead.Path...)}
			case SeqOriginStructLiteral:
				if origin.StructLiteral != nil {
					return &seqFieldBase{root: &SeqFieldRoot{Kind: "struct_literal",
						TypeID: origin.StructLiteral.TypeID, ID: origin.StructLiteral.ID},
						ref: seqTypeRefOfSymbolID(origin.StructLiteral.TypeID)}
				}
			case SeqOriginStructCopy:
				if origin.StructCopy != nil {
					return &seqFieldBase{root: &SeqFieldRoot{Kind: "struct_copy",
						TypeID: origin.StructCopy.TypeID, ID: origin.StructCopy.ID},
						ref: seqTypeRefOfCopyTypeID(origin.StructCopy)}
				}
			}
		}
		return seqCallCtorFieldBase(req, x)
	}
	return nil
}

// seqFieldBase 字段读取的基（对象根 + 当前类型 + 已有路径）。
type seqFieldBase struct {
	root *SeqFieldRoot
	ref  seqORMTypeRef
	path []string
	// U04j/U04k：字段槽根身份（值类型对象声明 / new(T) 分配）。
	obj   *ast.Object
	alloc string
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
		ReadFile: req.curCtx.rel, ReadOffset: req.curCtx.fset.Position(sel.Sel.Pos()).Offset,
		valueRef: seqCanonicalTypeRef(req, field.ref)}
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
	// pointer B03：字段声明是否为指针（嵌套路径必须区分值/指针）。
	pointer bool
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
	_, pointer := runtimeUnparen(typ).(*ast.StarExpr)
	out := seqResolvedField{ref: ref, owner: owner.name, ownerpkg: owner.pkgPath,
		iface: seqRefIsIfaceDecl(req, ref), pointer: pointer}
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

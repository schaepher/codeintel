package action

// U03：运行时求值里的 reflect 元信息链（源码可证明的部分）。
//
// 只支持会直接影响 SQL/模型映射的调用：
//   reflect.TypeOf(v)            → 类型（来自变量声明/复合字面量，静态可证）
//   t.Field(i) / t.FieldByName() → 字段（名称 + struct tag，来自源码声明）
//   t.NumField()                 → 字段数
//   field.Tag / tag.Get("db")    → struct tag 值
// 任何一步不可证明（未知类型/索引越界/动态字段名）→ unknown（不猜列名）。

import (
	"go/ast"
	"reflect"
	"strings"
)

// isReflectPkgCall 调用是否为 reflect 包级函数（支持导入别名）。
func isReflectPkgCall(frame *rtFrame, call *ast.CallExpr, name string) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok || (id.Obj != nil && id.Obj.Kind != ast.Pkg) {
		return false
	}
	if frame == nil || frame.ctx == nil || frame.ctx.file == nil {
		return false
	}
	return seqImportPath(frame.ctx.file, id.Name) == "reflect"
}

// evalReflectCall reflect 类型链（命中返回 handled=true）。
func (e *rtEval) evalReflectCall(frame *rtFrame, call *ast.CallExpr, s *rtState,
	depth int) (rtValue, []*rtState, bool) {
	if isReflectPkgCall(frame, call, "TypeOf") && len(call.Args) == 1 {
		if name, ok := runtimeExprStaticTypeName(frame, call.Args[0], s); ok {
			return rtValue{kind: rtTypeInfo, s: name}, []*rtState{s}, true
		}
		return rtUnknownValue(), []*rtState{s}, true
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return rtUnknownValue(), nil, false
	}
	recv, variants := e.evalExpr(frame, sel.X, s, depth)
	if len(variants) > 0 {
		s = variants[0]
	}
	switch recv.kind {
	case rtTypeInfo:
		switch sel.Sel.Name {
		case "Field":
			if len(call.Args) != 1 {
				return rtUnknownValue(), []*rtState{s}, true
			}
			idx, _ := e.evalExpr(frame, call.Args[0], s, depth)
			if idx.kind != rtInt {
				return rtUnknownValue(), []*rtState{s}, true
			}
			if fv, ok := runtimeStructFieldByIndex(frame, e.req, recv.s, int(idx.i)); ok {
				return fv, []*rtState{s}, true
			}
			return rtUnknownValue(), []*rtState{s}, true
		case "FieldByName":
			if len(call.Args) != 1 {
				return rtUnknownValue(), []*rtState{s}, true
			}
			name, _ := e.evalExpr(frame, call.Args[0], s, depth)
			if name.kind != rtString {
				return rtUnknownValue(), []*rtState{s}, true
			}
			if fv, ok := runtimeStructFieldByName(frame, e.req, recv.s, name.s); ok {
				return fv, []*rtState{s}, true
			}
			return rtUnknownValue(), []*rtState{s}, true
		case "NumField":
			if n, ok := runtimeStructFieldCount(frame, e.req, recv.s); ok {
				return rtIntValue(int64(n)), []*rtState{s}, true
			}
			return rtUnknownValue(), []*rtState{s}, true
		}
	case rtTagInfo:
		if sel.Sel.Name == "Get" && len(call.Args) == 1 {
			key, _ := e.evalExpr(frame, call.Args[0], s, depth)
			if key.kind != rtString {
				return rtUnknownValue(), []*rtState{s}, true
			}
			return rtStringValue(reflect.StructTag(recv.s).Get(key.s)), []*rtState{s}, true
		}
	}
	return rtUnknownValue(), nil, false
}

// runtimeExprStaticTypeName 表达式在源码里可证明的**具名类型**（变量声明 /
// 复合字面量类型 / 已记录的类型身份）。
func runtimeExprStaticTypeName(frame *rtFrame, expr ast.Expr, s *rtState) (string, bool) {
	switch e := expr.(type) {
	case *ast.UnaryExpr:
		if e.Op.String() == "&" {
			return runtimeExprStaticTypeName(frame, e.X, s)
		}
	case *ast.ParenExpr:
		return runtimeExprStaticTypeName(frame, e.X, s)
	case *ast.CompositeLit:
		return recvTypeName(e.Type), recvTypeName(e.Type) != ""
	case *ast.Ident:
		if e.Obj == nil {
			return "", false
		}
		if s != nil {
			if rt, ok := s.types[e.Obj]; ok && rt != nil && rt.name != "" {
				return rt.name, true
			}
		}
		switch d := e.Obj.Decl.(type) {
		case *ast.ValueSpec:
			if n := recvTypeName(d.Type); n != "" {
				return n, true
			}
		case *ast.Field:
			if n := recvTypeName(d.Type); n != "" {
				return n, true
			}
		}
	}
	return "", false
}

// runtimeStructFields 源码里的结构体字段（名称 + tag 文本，按声明顺序）。
func runtimeStructFields(frame *rtFrame, req CodeSequenceRequest, typeName string) ([]*ast.Field, bool) {
	if frame == nil || frame.ctx == nil || typeName == "" {
		return nil, false
	}
	_, st, ok := seqFindStructInPackage(req, frame.ctx.rel, typeName)
	if !ok || st == nil || st.Fields == nil {
		return nil, false
	}
	return st.Fields.List, true
}

func runtimeStructFieldByIndex(frame *rtFrame, req CodeSequenceRequest, typeName string, idx int) (rtValue, bool) {
	fields, ok := runtimeStructFields(frame, req, typeName)
	if !ok || idx < 0 || idx >= len(fields) {
		return rtUnknownValue(), false
	}
	return runtimeFieldValue(fields[idx]), true
}

func runtimeStructFieldByName(frame *rtFrame, req CodeSequenceRequest, typeName, name string) (rtValue, bool) {
	fields, ok := runtimeStructFields(frame, req, typeName)
	if !ok {
		return rtUnknownValue(), false
	}
	for _, fl := range fields {
		for _, nm := range fl.Names {
			if nm.Name == name {
				return runtimeFieldValue(fl), true
			}
		}
	}
	return rtUnknownValue(), false
}

func runtimeStructFieldCount(frame *rtFrame, req CodeSequenceRequest, typeName string) (int, bool) {
	fields, ok := runtimeStructFields(frame, req, typeName)
	if !ok {
		return 0, false
	}
	return len(fields), true
}

// runtimeFieldValue 字段元信息值（name + tag 文本）。
func runtimeFieldValue(fl *ast.Field) rtValue {
	name := ""
	if len(fl.Names) > 0 {
		name = fl.Names[0].Name
	}
	tag := ""
	if fl.Tag != nil {
		tag = strings.Trim(fl.Tag.Value, "`")
	}
	return rtValue{kind: rtFieldInfo, cols: map[string]rtValue{
		"name": rtStringValue(name),
		"tag":  rtStringValue(tag),
	}, order: []string{"name", "tag"}}
}

// runtimeColumnFromTag 由字段的 db 标签得到列名（无标签 → 字段名 snake_case）。
func runtimeColumnFromTag(tag, fieldName string) string {
	if v := reflect.StructTag(tag).Get("db"); v != "" {
		parts := strings.Split(v, ",")
		if parts[0] != "" && parts[0] != "-" {
			return parts[0]
		}
	}
	return snakeCaseName(fieldName)
}

// snakeCaseName 字段名 → snake_case（仅用于无 tag 的映射提示）。
func snakeCaseName(name string) string {
	var b strings.Builder
	for i, r := range name {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r - 'A' + 'a')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

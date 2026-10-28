package action

// todo24 §11.6：调用点身份核对——实例 selector 与包函数的类别区分、按可
// 证明的 receiver 类型消歧（从 action_seq_targets.go 拆出，行数治理）。
//
// 规则：非包 selector（局部/参数/嵌套 receiver）即使类型未知也不算包调用；
// 导入别名判断前先核对 AST 局部对象（遮蔽）；多候选时用可证明的 receiver
// 类型消歧，不可证明则保持歧义（不选第一个）。

import (
	"go/ast"
	"go/token"
	"strings"
)

// seqFilterByRecvType 多候选时按可证明 receiver 类型过滤；类型不可证明则
// 原样返回（保持歧义）。
func seqFilterByRecvType(matched []seqTarget, fun ast.Expr,
	recvType, recvName string, recvDecl map[string]string) []seqTarget {
	want, known := seqCallSiteReceiverType(fun, recvType, recvName, recvDecl)
	if !known {
		return matched
	}
	out := matched[:0]
	for _, tg := range matched {
		if got := seqTargetRecvType(tg.tid); got != "" && got == want {
			out = append(out, tg)
		}
	}
	return out
}

// seqTargetRecvType 方法 canonical ID 的 receiver 类型基名（`(T).M` → T）。
func seqTargetRecvType(id string) string {
	s := strings.TrimPrefix(id, "symbol:go:")
	i := strings.Index(s, ":(")
	if i < 0 {
		return ""
	}
	rest := s[i+2:]
	if j := strings.Index(rest, ")."); j >= 0 {
		return rest[:j]
	}
	return ""
}

// seqCallSiteReceiverType 调用点 receiver 的**可证明**类型名：当前 receiver
// 对象、已知字段类型、同函数局部变量（:= 的 RHS 身份/复合字面量/取址）、
// 参数声明类型。嵌套 receiver（box.worker）不做猜测。
func seqCallSiteReceiverType(fun ast.Expr, recvType, recvName string, recvDecl map[string]string) (string, bool) {
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	switch base := sel.X.(type) {
	case *ast.Ident:
		if recvType != "" && base.Name == recvName {
			return recvType, true
		}
		if t, ok := recvDecl[base.Name]; ok && t != "" {
			return t, true
		}
		if base.Obj == nil {
			return "", false
		}
		return seqLocalObjType(base.Obj, recvType, recvName, 0)
	case *ast.SelectorExpr:
		return seqNestedFieldProofType(base, recvType, recvName, recvDecl)
	}
	return "", false
}

// seqNestedFieldProofType `x.field.Method()` 的可证明类型：x 是当前 receiver
// （字段类型来自结构体声明）或局部匿名结构体变量（`box := struct{...}{}`）。
func seqNestedFieldProofType(sel *ast.SelectorExpr, recvType, recvName string, recvDecl map[string]string) (string, bool) {
	base, ok := sel.X.(*ast.Ident)
	if !ok {
		return "", false
	}
	field := sel.Sel.Name
	if recvType != "" && base.Name == recvName {
		if t, ok := recvDecl[field]; ok && t != "" {
			return t, true
		}
		return "", false
	}
	if base.Obj == nil {
		return "", false
	}
	as, ok := base.Obj.Decl.(*ast.AssignStmt)
	if !ok {
		return "", false
	}
	for i, lhs := range as.Lhs {
		lid, ok := lhs.(*ast.Ident)
		if !ok || lid.Obj != base.Obj || i >= len(as.Rhs) {
			continue
		}
		cl, ok := as.Rhs[i].(*ast.CompositeLit)
		if !ok {
			return "", false
		}
		st, ok := cl.Type.(*ast.StructType)
		if !ok {
			return "", false
		}
		for _, fl := range st.Fields.List {
			if len(fl.Names) == 1 && fl.Names[0].Name == field {
				if t := recvTypeName(fl.Type); t != "" {
					return t, true
				}
			}
		}
		return "", false
	}
	return "", false
}

// seqLocalObjType 局部对象的可证明类型（深度受限，避免别名链过长）。
func seqLocalObjType(obj *ast.Object, recvType, recvName string, depth int) (string, bool) {
	if obj == nil || depth > 3 {
		return "", false
	}
	switch decl := obj.Decl.(type) {
	case *ast.AssignStmt:
		for i, lhs := range decl.Lhs {
			lid, ok := lhs.(*ast.Ident)
			if !ok || lid.Obj != obj || i >= len(decl.Rhs) {
				continue
			}
			return seqExprProofType(decl.Rhs[i], recvType, recvName, depth+1)
		}
	case *ast.ValueSpec:
		if decl.Type != nil {
			if t := recvTypeName(decl.Type); t != "" {
				return t, true
			}
			return "", false
		}
		for i, nm := range decl.Names {
			if nm.Obj != obj || i >= len(decl.Values) {
				continue
			}
			return seqExprProofType(decl.Values[i], recvType, recvName, depth+1)
		}
	case *ast.Field: // 参数/结果声明
		if t := recvTypeName(decl.Type); t != "" {
			return t, true
		}
	}
	return "", false
}

// seqExprProofType RHS 表达式的可证明类型：`&T{}`/`T{}`/`new(T)`/已知局部变量。
// 函数调用（工厂）不在这里猜（由工厂/字段数据流路径处理）。
func seqExprProofType(e ast.Expr, recvType, recvName string, depth int) (string, bool) {
	if depth > 3 {
		return "", false
	}
	switch x := e.(type) {
	case *ast.Ident:
		if recvType != "" && x.Name == recvName {
			return recvType, true
		}
		if x.Obj == nil {
			return "", false
		}
		return seqLocalObjType(x.Obj, recvType, recvName, depth+1)
	case *ast.UnaryExpr:
		if x.Op == token.AND {
			return seqExprProofType(x.X, recvType, recvName, depth+1)
		}
	case *ast.CompositeLit:
		if t := recvTypeName(x.Type); t != "" {
			return t, true
		}
	case *ast.CallExpr:
		if id, ok := x.Fun.(*ast.Ident); ok && id.Name == "new" && len(x.Args) == 1 {
			if t := recvTypeName(x.Args[0]); t != "" {
				return t, true
			}
		}
	}
	return "", false
}

// seqTargetName 被调符号短名（函数=名字；方法=方法名）。
func seqTargetName(id string) string {
	_, name, _ := seqSplitTarget(id)
	return name
}

// seqSplitTarget canonical ID → (包路径, 短名, 是否方法)。
func seqSplitTarget(id string) (pkg, name string, method bool) {
	s := strings.TrimPrefix(id, "symbol:go:")
	if i := strings.Index(s, ":("); i >= 0 {
		pkg = s[:i]
		rest := s[i+2:]
		if j := strings.Index(rest, ")."); j >= 0 {
			name = rest[j+2:]
		}
		return pkg, name, true
	}
	if i := strings.LastIndex(s, ":"); i >= 0 {
		return s[:i], s[i+1:], false
	}
	return "", "", false
}

// seqTargetMatchesCall 旧行号目标与调用表达式的静态身份是否一致
// （调用名/包提示/函数-vs-方法；未知字段不参与核对）。
func seqTargetMatchesCall(tg seqTarget, fun ast.Expr, f *ast.File,
	curPkg, recvType, recvName string, recvDecl map[string]string) bool {
	name, pkgHint, wantMethod, kindKnown := seqCallSiteIdentity(fun, f, curPkg, recvType, recvDecl)
	if name == "" {
		return true // 无法核对：保留旧行为（由后续展开/诊断处理）
	}
	pkg, tname, tmethod := seqSplitTarget(tg.tid)
	if tname != name {
		return false
	}
	if pkgHint != "" && pkg != pkgHint {
		return false
	}
	if kindKnown && tmethod != wantMethod {
		return false
	}
	return true
}

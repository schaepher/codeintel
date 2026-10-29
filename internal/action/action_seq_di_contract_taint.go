package action

import "go/ast"

// seqDIContractInFunc 在单个函数内做**变量级**契约证明：
//
//	annotation →（key）→ GetBean 实参 → reflect.ValueOf(结果) → 同一字段
//	的 FieldByName 结果 .Set(...)。
//
// 只要求"同函数 + 变量依赖链"，不按调用顺序/无关语句拼接（不能因为函数里
// 分别出现过 4 个调用就当契约成立）。
func seqDIContractInFunc(req CodeSequenceRequest, ctx *seqFileCtx, rel string,
	fd *ast.FuncDecl) (seqDIContract, bool) {
	taint := map[string]map[string]bool{}
	getContainer := map[string]bool{}
	line := 0
	hasAnnotation, hasGet, hasField := false, false, false
	var setOK bool

	var label func(e ast.Expr) map[string]bool
	label = func(e ast.Expr) map[string]bool {
		out := map[string]bool{}
		switch x := e.(type) {
		case *ast.ParenExpr:
			return label(x.X)
		case *ast.Ident:
			for k := range taint[x.Name] {
				out[k] = true
			}
			return out
		case *ast.IndexExpr:
			for k := range label(x.X) {
				out[k] = true
			}
			return out
		case *ast.SelectorExpr:

			if call, ok := x.X.(*ast.CallExpr); ok {
				if sel, ok2 := call.Fun.(*ast.SelectorExpr); ok2 {
					if m := sel.Sel.Name; m == "Value" || m == "GetStructByAnno" || m == "GetAnnotation" {
						out[diTaintAnnotation] = true
						return out
					}
				}
			}
			for k := range label(x.X) {
				out[k] = true
			}
			return out
		case *ast.CallExpr:
			name, recvName := "", ""
			switch fn := x.Fun.(type) {
			case *ast.SelectorExpr:
				name = fn.Sel.Name
				if id, ok := fn.X.(*ast.Ident); ok {
					recvName = id.Name
				} else if sel2, ok := fn.X.(*ast.SelectorExpr); ok {
					recvName = sel2.Sel.Name
				}
			case *ast.Ident:
				name = fn.Name
			}
			switch name {
			case "Value", "GetStructByAnno", "GetAnnotation":
				out[diTaintAnnotation] = true
				return out
			case "GetBean", "Get", "Resolve", "Lookup":
				hasGet = true
				if recvName != "" {
					getContainer[recvName] = true
				}
				if len(x.Args) > 0 {
					for k := range label(x.Args[0]) {
						if k == diTaintAnnotation {
							out[diTaintBean] = true
						}
					}
				}
				if !out[diTaintBean] {
					out[diTaintBean] = true
				}
				return out
			case "TypeOf", "ValueOf":
				for k := range label(x.Args[0]) {
					switch k {
					case diTaintBean:
						out[diTaintBeanValue] = true
					case diTaintField:
						out[diTaintFieldValue] = true
					}
				}
				return out
			case "FieldByName":
				hasField = true
				out[diTaintField] = true
				return out
			case "Elem", "Addr", "Interface":
				return out
			}
			return out
		}
		return out
	}

	record := func(lhs []ast.Expr, rhs []ast.Expr) {
		for i, l := range lhs {
			id, ok := l.(*ast.Ident)
			if !ok || i >= len(rhs) {
				continue
			}
			t := label(rhs[i])
			delete(taint, id.Name)
			if len(t) > 0 {
				taint[id.Name] = t
				if t[diTaintAnnotation] {
					hasAnnotation = true
				}
			}
		}
	}

	ast.Inspect(fd.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignStmt:
			record(x.Lhs, x.Rhs)
		case *ast.ValueSpec:
			lhs := make([]ast.Expr, 0, len(x.Names))
			for _, nm := range x.Names {
				lhs = append(lhs, nm)
			}
			record(lhs, x.Values)
		case *ast.CallExpr:
			sel, ok := x.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Set" || len(x.Args) != 1 {
				return true
			}

			recv := label(sel.X)
			arg := label(x.Args[0])
			if (recv[diTaintField] || recv[diTaintFieldValue]) && arg[diTaintBeanValue] {
				setOK = true
				if line == 0 && ctx.fset != nil {
					line = ctx.fset.Position(x.Lparen).Line
				}
			}
			if (arg[diTaintBean] || arg[diTaintBeanValue]) && len(x.Args) > 0 {

				if recv[diTaintField] {
					setOK = true
				}
			}
		}
		return true
	})
	if !setOK || !hasGet || !hasField || !hasAnnotation {
		return seqDIContract{}, false
	}
	keys := []string{"annotation", "get", "field", "set"}
	if line == 0 && ctx.fset != nil && fd.Body != nil {
		line = ctx.fset.Position(fd.Body.Lbrace).Line
	}
	var containers []string
	for k := range getContainer {
		containers = append(containers, k)
	}
	sortStrings(containers)
	return seqDIContract{file: rel, line: line, steps: keys, containers: containers}, true
}

package action

// todo25 §3.3：DI 引导函数扫描（含局部闭包被顶层直接调用的注册）。
// 从 action_seq_di.go 拆出（行数治理）。

import "go/ast"

// seqScanRegistrations 扫描一个引导函数体内的注册调用（顶层语句；条件/
// 循环里的注册不计入——无法证明一定执行）。局部闭包被**顶层直接调用**时，
// 闭包体内的注册同样确定执行（todo25 §3.3：闭包捕获后注册）。
func seqScanRegistrations(a *Actions, req CodeSequenceRequest, ctx *seqFileCtx,
	body *ast.BlockStmt, out map[string][]seqDIReg) {
	seqScanBootstrapBlock(a, req, ctx, body, out, 0)
}

func seqScanBootstrapBlock(a *Actions, req CodeSequenceRequest, ctx *seqFileCtx,
	body *ast.BlockStmt, out map[string][]seqDIReg, depth int) {
	closures := map[*ast.Object]*ast.FuncLit{}
	ast.Inspect(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignStmt:
			if len(x.Lhs) == 1 && len(x.Rhs) == 1 {
				if id, ok := x.Lhs[0].(*ast.Ident); ok {
					if lit, ok2 := x.Rhs[0].(*ast.FuncLit); ok2 && id.Obj != nil {
						closures[id.Obj] = lit
					}
				}
			}
		case *ast.ValueSpec:
			for i, name := range x.Names {
				if i >= len(x.Values) {
					continue
				}
				if lit, ok := x.Values[i].(*ast.FuncLit); ok && name.Obj != nil {
					closures[name.Obj] = lit
				}
			}
		}
		return true
	})
	for _, st := range body.List {
		es, ok := st.(*ast.ExprStmt)
		if !ok {
			continue
		}
		call, ok := es.X.(*ast.CallExpr)
		if !ok {
			continue
		}
		if depth < 3 {
			if id, ok := call.Fun.(*ast.Ident); ok && id.Obj != nil {
				if lit := closures[id.Obj]; lit != nil && lit.Body != nil {
					seqScanBootstrapBlock(a, req, ctx, lit.Body, out, depth+1)
					continue
				}
			}
		}
		// 允许 init 里调用 RegisterXxx()（一跳展开到同包函数）
		seqCollectRegCalls(a, req, ctx, call, out, 0)
	}
	seqInvalidateRegistrationRewrites(body, out)
}

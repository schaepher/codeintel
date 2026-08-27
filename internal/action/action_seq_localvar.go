package action

import "go/ast"

// localVarImpl P0-6（用户实测）：调用点 X 是局部变量（m.SubmitOrder
// 的 m）时的 DI 注入具体化——变量初始化自构造器（m := newX()，newX
// 返回接口、函数体 return 真实实现）或字面量（m := &Impl{}）→ 构造
// (Impl).Method。找不到（参数形态/无初始化）返回空——fallback 现有
// 接口匹配/枚举。
func (a *Actions) localVarImpl(req CodeSequenceRequest, f *ast.File, curPkg, varName, method string) string {
	imports := importAliases(f)
	var out string
	ast.Inspect(f, func(node ast.Node) bool {
		if out != "" {
			return false
		}
		as, ok := node.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
			return true
		}
		id, ok := as.Lhs[0].(*ast.Ident)
		if !ok || id.Name != varName {
			return true
		}
		for _, c := range rhsTypes(a, req, f, imports, as.Rhs[0], 3, curPkg) {
			if id2 := GrpcMethodEntryID("symbol:go:"+c.pkgPath+":"+c.typeName, method); id2 != "" {
				out = id2
				return false
			}
		}
		return true
	})
	return out
}

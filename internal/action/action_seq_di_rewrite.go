package action

// todo25 §3.3：未知容器方法可能改写/替换已注册实现——不能因为"注册调用还在"
// 就当成最终绑定。只对同一个容器变量（*ast.Object 身份）判定的保守规则：
//   - 已知注册方法：Register/Bind/Set/Provide/Apply/Put（收集 key）；
//   - 同一容器上的其它方法：带字符串字面量 → 只失效该 key；无字面量 key →
//     失效该容器全部注册。
// 条件/循环内的调用同样计入（无法证明不执行）。

import (
	"go/ast"
	"strings"
)

var seqRegistrationCallNames = map[string]bool{
	"Register": true, "Bind": true, "Set": true,
	"Provide": true, "Apply": true, "Put": true,
}

// seqInvalidateRegistrationRewrites 标记被未知容器方法改写的注册。
func seqInvalidateRegistrationRewrites(body *ast.BlockStmt, out map[string][]seqDIReg) {
	if body == nil || len(out) == 0 {
		return
	}
	containerKeys := map[*ast.Object]map[string]bool{}
	containerNames := map[string]map[string]bool{}
	note := func(id *ast.Ident, key string) {
		if id == nil || id.Name == "" || key == "" {
			return
		}
		if id.Obj != nil {
			if containerKeys[id.Obj] == nil {
				containerKeys[id.Obj] = map[string]bool{}
			}
			containerKeys[id.Obj][key] = true
		}
		if containerNames[id.Name] == nil {
			containerNames[id.Name] = map[string]bool{}
		}
		containerNames[id.Name][key] = true
	}
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !seqRegistrationCallNames[sel.Sel.Name] {
			return true
		}
		id, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		if k := seqFirstStringLit(call); k != "" {
			note(id, k)
		}
		return true
	})
	if len(containerKeys) == 0 && len(containerNames) == 0 {
		return
	}
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || seqRegistrationCallNames[sel.Sel.Name] {
			return true
		}
		id, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		keys := map[string]bool{}
		if id.Obj != nil {
			for k := range containerKeys[id.Obj] {
				keys[k] = true
			}
		}
		for k := range containerNames[id.Name] {
			keys[k] = true
		}
		if len(keys) == 0 {
			return true
		}
		if k := seqFirstStringLit(call); k != "" {
			keys = map[string]bool{k: true}
		}
		for k := range keys {
			for i := range out[k] {
				out[k][i].invalid = true
			}
		}
		return true
	})
}

func seqFirstStringLit(call *ast.CallExpr) string {
	if call == nil || len(call.Args) == 0 {
		return ""
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind.String() != "STRING" {
		return ""
	}
	return strings.Trim(lit.Value, "\"`")
}

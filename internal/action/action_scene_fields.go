package action

// todo22 P1：场景值沿**实参/结构体字段**跨函数传播（从 action_scene_flow.go
// 与 action_scene_lit.go 拆出——行数治理）。

import (
	"go/ast"
	"go/token"
	"strings"
)

// resolveSceneArgFields 调用实参的**结构体字段已知值**（todo22 P1：
// 跨函数传递 req 的来源分派）。支持三种形态：
//   - 直接复合字面量：`Submit(&Req{Source: SourceB})`
//   - 局部变量：`r := &Req{Source: SourceB}` / `r.Source = SourceB`（沿
//     调用点之前的可达定义，含分支合流/取址改写失效）
//   - 当前函数形参：字段值已由上游调用传播到 req.sceneArgs（键 `param.Field`）
//
// 只传播**能证明**的字段值；字段被未知值赋值/被取址调用改写时记
// `sceneArgUnknown`（内层不得回退 --scene 键，与实参值的既有语义一致）。
func (a *Actions) resolveSceneArgFields(req CodeSequenceRequest, fset *token.FileSet,
	src []byte, call *ast.CallExpr, body *ast.BlockStmt, params *ast.FieldList) []map[string]sceneArg {
	if call == nil {
		return nil
	}
	out := make([]map[string]sceneArg, len(call.Args))
	var scope *litScopes
	for i, arg := range call.Args {
		fields := map[string]sceneArg{}
		inner := arg
		if u, ok := inner.(*ast.UnaryExpr); ok && u.Op == token.AND {
			inner = u.X
		}
		if lit, ok := inner.(*ast.CompositeLit); ok {
			for _, el := range lit.Elts {
				kv, ok := el.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok {
					continue
				}
				if v, ok := sceneValueOf(kv.Value, req.sceneConsts); ok {
					fields[key.Name] = sceneArg{val: v,
						source: "literal-field@" + fileLine("", fset.Position(kv.Value.Pos()).Line)}
					continue
				}
				fields[key.Name] = sceneArg{val: sceneArgUnknown, reason: "unknown:field-value"}
			}
		} else if id, ok := inner.(*ast.Ident); ok && (id.Obj == nil || id.Obj.Kind != ast.Pkg) {
			if scope == nil {
				scope = reachingScopeAt(fset, src, body, call.Pos(), params,
					req.incomingArgs, req.incomingSrc, req.sceneConsts)
			}
			prefix := id.Name + "."
			if scope != nil {
				for field, v := range scope.prefixValues(prefix) {
					fields[field] = sceneArgFromLitVal(v, req.sceneConsts)
				}
			}
			// 当前函数形参的字段值（上游传播）——本地定义优先覆盖
			for k, v := range req.sceneArgs {
				if !strings.HasPrefix(k, prefix) {
					continue
				}
				field := strings.TrimPrefix(k, prefix)
				if _, set := fields[field]; set {
					continue
				}
				fields[field] = sceneArg{val: v, source: req.sceneSrc[k], reason: req.sceneWhy[k]}
			}
		}
		if len(fields) > 0 {
			out[i] = fields
		}
	}
	return out
}

// sceneArgFromLitVal 抽象值 → 实参字段值（未知保留原因，不回退场景键）。
func sceneArgFromLitVal(v litVal, consts sceneConstLookup) sceneArg {
	if v.known {
		return sceneArg{val: v.val, source: v.src}
	}
	reason := v.reason
	if reason == "" {
		reason = "unknown:field-value"
	}
	return sceneArg{val: sceneArgUnknown, source: v.src, reason: reason}
}

// paramFieldArgs 结构体字段值 → 被调函数形参映射（键 `param.Field`）。
func paramFieldArgs(fl *ast.FieldList, fields []map[string]sceneArg) map[string]sceneArg {
	if fl == nil || len(fields) == 0 {
		return nil
	}
	out := map[string]sceneArg{}
	idx := 0
	for _, f := range fl.List {
		n := len(f.Names)
		if n == 0 {
			n = 1
		}
		for i := 0; i < n; i++ {
			name := ""
			if i < len(f.Names) {
				name = f.Names[i].Name
			}
			if name != "" && idx < len(fields) {
				for field, v := range fields[idx] {
					out[name+"."+field] = v
				}
			}
			idx++
		}
	}
	return out
}

// applySceneFieldArgs 把（字段值 → 形参映射）合并进当前场景实参表。
func applySceneFieldArgs(args, srcs, whys map[string]string, fl *ast.FieldList, fields []map[string]sceneArg) {
	merged := paramFieldArgs(fl, fields)
	for k, v := range merged {
		if args == nil {
			continue
		}
		args[k] = v.val
		if srcs != nil && v.source != "" {
			srcs[k] = v.source
		}
		if whys != nil && v.reason != "" {
			whys[k] = v.reason
		}
	}
}

// declareStructFields 复合字面量（`T{...}` / `&T{...}`）的字段值写进
// 作用域（键 `var.Field`）——后续把变量作为实参时可沿函数边界传播。
func declareStructFields(fset *token.FileSet, s *litScopes, varName string, expr ast.Expr) {
	if u, ok := expr.(*ast.UnaryExpr); ok && u.Op == token.AND {
		expr = u.X
	}
	lit, ok := expr.(*ast.CompositeLit)
	if !ok || varName == "" {
		return
	}
	for _, el := range lit.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		s.declare(varName+"."+key.Name, evalLitExpr(fset, kv.Value, s))
	}
}

// clearStructFields 清掉变量的全部字段键（整体重新赋值后旧字段不再成立）。
func clearStructFields(s *litScopes, varName string) {
	prefix := varName + "."
	for i := range s.scopes {
		for k := range s.scopes[i] {
			if strings.HasPrefix(k, prefix) {
				delete(s.scopes[i], k)
			}
		}
	}
}

// prefixValues 收集指定前缀（如 `req.`）的全部可达键值（内层作用域覆盖外层）。
func (s *litScopes) prefixValues(prefix string) map[string]litVal {
	out := map[string]litVal{}
	for i := 0; i < len(s.scopes); i++ {
		for k, v := range s.scopes[i] {
			if strings.HasPrefix(k, prefix) {
				out[strings.TrimPrefix(k, prefix)] = v
			}
		}
	}
	return out
}

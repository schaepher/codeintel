package action

// U04bU.3：被调函数形参 → 调用实参指向的包级资源（跨包 `*handle=nil`）。
//
// 从 action_seq_workflow_resource_builder.go 拆出（行数治理）。

import (
	"go/ast"
	"go/token"
)

// ptrBindOf 形参当前绑定的包级资源名（沿调用帧自顶向下查找）。
func (b *seqResourceFactBuilder) ptrBindOf(id *ast.Ident) string {
	if id == nil || id.Obj == nil {
		return ""
	}
	for i := len(b.ptrBinds) - 1; i >= 0; i-- {
		if v := b.ptrBinds[i][id.Obj]; v != "" {
			return v
		}
	}
	return ""
}

// setPtrBind 在当前调用帧登记指针别名。
func (b *seqResourceFactBuilder) setPtrBind(obj *ast.Object, target string) {
	if obj == nil || target == "" || len(b.ptrBinds) == 0 {
		return
	}
	b.ptrBinds[len(b.ptrBinds)-1][obj] = target
}

// ptrArgTarget 调用实参是否指向包级资源（`&DB` / 指针形参别名）。
func (b *seqResourceFactBuilder) ptrArgTarget(arg ast.Expr) string {
	switch v := runtimeUnparen(arg).(type) {
	case *ast.UnaryExpr:
		if v.Op == token.AND {
			if id, ok := runtimeUnparen(v.X).(*ast.Ident); ok && b.isPkgVar(id) {
				return id.Name
			}
		}
	case *ast.Ident:
		return b.ptrBindOf(v)
	}
	return ""
}

// pushFuncBinds 按被调函数形参位置绑定调用实参指向的资源。
func (b *seqResourceFactBuilder) pushFuncBinds(fd *ast.FuncDecl, args []ast.Expr) {
	frame := map[*ast.Object]string{}
	if fd != nil && fd.Type != nil && fd.Type.Params != nil {
		idx := 0
		for _, field := range fd.Type.Params.List {
			for _, name := range field.Names {
				if idx < len(args) {
					if target := b.ptrArgTarget(args[idx]); target != "" && name.Obj != nil {
						frame[name.Obj] = target
					}
				}
				idx++
			}
			if len(field.Names) == 0 {
				idx++
			}
		}
	}
	b.ptrBinds = append(b.ptrBinds, frame)
}

// popFuncBinds 弹出当前调用帧。
func (b *seqResourceFactBuilder) popFuncBinds() {
	if n := len(b.ptrBinds); n > 0 {
		b.ptrBinds = b.ptrBinds[:n-1]
	}
}

// walkFuncIn 被调函数体（事件记调用点位置）。
func (b *seqResourceFactBuilder) walkFuncIn(fn *ast.FuncDecl, file *seqParsedGoFile,
	callOffset, depth int, args []ast.Expr) {
	if fn == nil || file == nil || fn.Body == nil {
		return
	}
	b.calls++
	if depth > seqResourceFactMaxDepth || b.calls > seqResourceFactMaxCalls {
		b.out.partial = true
		return
	}
	b.pushFuncBinds(fn, args)
	defer b.popFuncBinds()
	b.walkBlock(fn.Body, file, callOffset, false, depth, map[*ast.Object]*ast.FuncLit{})
}

// pkgFunc 同包函数（唯一同名）。
func (b *seqResourceFactBuilder) pkgFunc(name string) *ast.FuncDecl {
	var found *ast.FuncDecl
	for _, f := range b.files {
		for _, d := range f.file.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv != nil || fd.Name == nil || fd.Name.Name != name {
				continue
			}
			if found != nil {
				return nil
			}
			found = fd
		}
	}
	return found
}

// addEvent 记录一次资源改写事件。
func (b *seqResourceFactBuilder) addEvent(name string, offset int) {
	if b == nil || b.out == nil || name == "" {
		return
	}
	sym := "symbol:go:" + b.pkg + ":" + name
	b.out.events[sym] = append(b.out.events[sym], seqResEvent{off: offset})
	b.log = append(b.log, seqResEventRef{name: sym, idx: len(b.out.events[sym]) - 1})
}

// isPkgVar 赋值目标是否指向包级 var（AST 声明身份；局部同名不命中）。
func (b *seqResourceFactBuilder) isPkgVar(id *ast.Ident) bool {
	if id == nil {
		return false
	}
	if id.Obj != nil {
		return b.pkgVarObjs[id.Obj]
	}
	// 跨文件引用在单文件 parser 里没有 Obj：只能按名称回退。
	return b.pkgVars[id.Name]
}

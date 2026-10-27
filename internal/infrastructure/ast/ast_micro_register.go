package ast

import (
	"go/types"
	"strings"

	"github.com/schaepher/codeintel/internal/canonicalizer"
	"github.com/schaepher/codeintel/internal/domain"
)

// microRegisterShape 注册调用形态：服务短名（handler 名）+ 方法全集。
func microRegisterShape(callee *types.Func, sig *types.Signature, argType types.Type) (handler string, methods []string, ok bool) {
	calleePkg := callee.Pkg().Path()

	if strings.HasPrefix(callee.Name(), "Register") && strings.HasSuffix(callee.Name(), "Handler") &&
		len(callee.Name()) > len("RegisterHandler") && !isMicroRootPkg(calleePkg) {
		handler = strings.TrimSuffix(strings.TrimPrefix(callee.Name(), "Register"), "Handler")
		if h, ms, okI := microHandlerIface(sig.Params().At(1).Type()); okI {

			if h != "" && strings.TrimSuffix(handler, "Handler") != strings.TrimSuffix(h, "Handler") {
				return "", nil, false
			}
			if h != "" {
				handler = h
			}
			methods = ms
		}
		return handler, methods, handler != ""
	}

	if isMicroRootPkg(calleePkg) && callee.Name() == "RegisterHandler" {
		if h, ms, okC := microConcreteHandler(argType); okC {
			return h, ms, true
		}
		handler, methods, _ = microHandlerIface(sig.Params().At(1).Type())
		if handler == "" {
			return "", nil, false
		}
		return handler, methods, true
	}
	return "", nil, false
}

// microHandlerIface 注册第二参（handler 接口）→ 服务短名 + 方法名。
// 接口名 `<X>Handler` → X；非接口（`any`/具体类型）返回空（不猜）。
func microHandlerIface(t types.Type) (handler string, methods []string, ok bool) {
	if p, isPtr := t.(*types.Pointer); isPtr {
		t = p.Elem()
	}
	named, okN := t.(*types.Named)
	if !okN || named.Obj() == nil {
		return "", nil, false
	}
	iface, okI := named.Underlying().(*types.Interface)
	if !okI || iface.NumMethods() == 0 {
		return "", nil, false
	}
	name := named.Obj().Name()
	if !strings.HasSuffix(name, "Handler") || len(name) <= len("Handler") {
		return "", nil, false
	}
	for i := 0; i < iface.NumMethods(); i++ {
		methods = append(methods, iface.Method(i).Name())
	}
	return strings.TrimSuffix(name, "Handler"), methods, true
}

// microConcreteHandler 注册实参是**具体 handler 类型**（`new(AccountServer)`）：
// go-micro 的 handler 名 = 反射得到的**类型名本身**（endpoint 前缀
// `AccountServer.Apply`），因此服务短名取类型名，不做后缀裁剪；方法取该
// 类型的指针方法集（导出方法）。类型名不可识别 → 不猜。
func microConcreteHandler(t types.Type) (handler string, methods []string, ok bool) {
	if t == nil {
		return "", nil, false
	}
	if p, isPtr := t.(*types.Pointer); isPtr {
		t = p.Elem()
	}
	named, okN := t.(*types.Named)
	if !okN || named.Obj() == nil {
		return "", nil, false
	}
	if _, isIface := named.Underlying().(*types.Interface); isIface {
		return "", nil, false
	}
	name := named.Obj().Name()
	if name == "" {
		return "", nil, false
	}
	mset := types.NewMethodSet(types.NewPointer(named))
	for i := 0; i < mset.Len(); i++ {
		obj := mset.At(i).Obj()
		if obj == nil || !obj.Exported() {
			continue
		}
		if strings.HasPrefix(obj.Name(), "mustEmbed") {
			continue
		}
		methods = append(methods, obj.Name())
	}
	return name, methods, true
}

// typePkgPath 具名类型的包路径（指针自动解引用；非具名返回空）。
func typePkgPath(t types.Type) string {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok || named.Obj() == nil || named.Obj().Pkg() == nil {
		return ""
	}
	return named.Obj().Pkg().Path()
}

// typeCanonicalID 具名类型 → (canonical ID, 短名)；非具名（接口字面量/
// 内建）返回 false。
func typeCanonicalID(t types.Type) (domain.CanonicalID, string, bool) {
	if t == nil {
		return "", "", false
	}
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok || named.Obj() == nil || named.Obj().Pkg() == nil {
		return "", "", false
	}
	return canonicalizer.GoSymbolID(named.Obj().Pkg().Path(), named.Obj().Name()),
		named.Obj().Name(), true
}

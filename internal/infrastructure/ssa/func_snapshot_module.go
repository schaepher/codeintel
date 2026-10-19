package ssa

// todo9-8：函数全集按模块采集（不再 ssautil.AllFunctions 扫全程序）。
//
// 背景：`ssautil.AllFunctions(prog)` 会遍历**所有**包（含依赖）的函数体
// 指令并物化全部运行时类型的方法包装——go2o 实测 85MB 分配（占构建总
// 分配约 6%）+ 约 280ms CPU，而消费者（moduleFuncs）随后只保留模块内
// 函数。这里按同样的可达性规则、只从模块包出发采集：
//   - 模块包 Members（包级函数/init）为根，遍历指令操作数里的
//     *ssa.Function（静态调用/泛型实例/方法值包装/闭包 Fn）；
//   - 导出具名类型（非泛型）的 T 与 *T 方法集（AllFunctions 的
//     exportedTypeHack）——只处理模块包（依赖不可能嵌入模块类型）；
//   - prog.RuntimeTypes() 的方法集（MakeInterface 物化的运行时类型）：
//     只处理具名声明在模块内的类型（泛型实例的方法由此进入）；
//   - 依赖包函数不遍历（不能静态引用模块代码）；但**合成包装**
//     （Pkg == nil，如提升方法的 $bound/thunk）必须遍历——其操作数可能
//     引用模块内的泛型 origin 方法（go2o 实测 12 个 BaseRepository[M]
//     方法即由此可达）。
//
// 与 AllFunctions 的模块子集逐函数等价由测试与 go2o 一次性核对（11996/11996，
// missing=0 extra=0）验证；差异仅限"不再物化依赖方法的包装函数"（本仓库
// 不调用 MethodValue，不影响产物）。

import (
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/ssa"
)

// isModuleType 类型（去指针）的具名声明是否属于模块包。
func isModuleType(T types.Type, modules []string) bool {
	if p, ok := T.(*types.Pointer); ok {
		T = p.Elem()
	}
	named, ok := T.(*types.Named)
	if !ok {
		return false
	}
	obj := named.Obj()
	return obj != nil && obj.Pkg() != nil && isInModule(obj.Pkg().Path(), modules)
}

// moduleFunctions 模块内函数集合（含闭包、泛型实例、方法值包装）。
func moduleFunctions(prog *ssa.Program, modules []string) []*ssa.Function {
	seen := make(map[*ssa.Function]struct{}, 4096)
	walked := make(map[*ssa.Function]struct{}, 4096)
	var visit func(fn *ssa.Function)
	visit = func(fn *ssa.Function) {
		if fn == nil {
			return
		}
		if _, ok := walked[fn]; ok {
			return
		}
		walked[fn] = struct{}{}
		if isModuleFunction(fn, modules) {
			seen[fn] = struct{}{}
		}
		if fn.Pkg != nil && fn.Pkg.Pkg != nil && !isInModule(fn.Pkg.Pkg.Path(), modules) {
			return
		}
		var buf [10]*ssa.Value
		for _, b := range fn.Blocks {
			for _, instr := range b.Instrs {
				for _, op := range instr.Operands(buf[:0]) {
					if callee, ok := (*op).(*ssa.Function); ok {
						visit(callee)
					}
				}
			}
		}
	}
	methodsOf := func(T types.Type) {
		if types.IsInterface(T) {
			return
		}
		mset := prog.MethodSets.MethodSet(T)
		for i := 0; i < mset.Len(); i++ {
			// 不能在 MethodValue 前按 sel.Obj().Pkg() 过滤：泛型实例方法的
			// 对象 Pkg 可能为 nil（包装函数的实际 Pkg 由 visit 判定）。
			visit(prog.MethodValue(mset.At(i)))
		}
	}
	for _, p := range prog.AllPackages() {
		if p == nil || p.Pkg == nil || !isInModule(p.Pkg.Path(), modules) {
			continue
		}
		for _, mem := range p.Members {
			switch mem := mem.(type) {
			case *ssa.Function:
				visit(mem)
			case *ssa.Type:
				// AllFunctions 的 exportedTypeHack：导出、非接口、非泛型具名类型。
				if !ast.IsExported(mem.Name()) || types.IsInterface(mem.Type()) {
					continue
				}
				if named, ok := mem.Type().(*types.Named); ok && named.TypeParams() == nil {
					methodsOf(named)
					methodsOf(types.NewPointer(named))
				}
			}
		}
	}
	for _, T := range prog.RuntimeTypes() {
		if isModuleType(T, modules) {
			methodsOf(T)
		}
	}
	out := make([]*ssa.Function, 0, len(seen))
	for fn := range seen {
		out = append(out, fn)
	}
	return out
}

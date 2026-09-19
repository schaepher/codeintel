package ssa

import (
	"go/types"
	"sync"

	"go.uber.org/zap"
)

// implTypePool 模块内具名类型池（Q246：接口动态派发候选枚举的性能改造）。
//
// 原实现（原 implMethodsFor）在**每个接口调用点**重新走一遍全部模块内
// 包的 scope.Names()（每次调用分配一个完整 []string）+ 对每个具名类型
// 调用 types.Implements（每类型再分配一个 types.NewPointer），复杂度
// = 调用点数 × 模块内类型数 × 类型方法数；emitCall（cf_call.go）是**按
// 调用点**调用它，go2o 全量构建里这是 SSA 阶段最大的分配/GC 来源之一。
//
// 改造两点，语义与旧实现逐条对齐：
//  1. 类型池 Index 级构建一次（只扫一遍各包 scope，顺带预计算
//     *types.Pointer），types.NewPointer 不再按 (调用点 × 类型) 分配
//  2. 结果按 (接口, 方法名) memo——同一接口方法的后续调用点 O(1)
type implTypePool struct {
	// types 模块内全部包级具名类型（Q246：Index 级一次扫描）
	types []pooledType
	mu    sync.RWMutex
	// cached 结果 memo（键为接口类型指针 + 方法名——池与程序同生命周期）
	cached map[implKey][]*types.Func
}

// pooledType 池内类型：具名类型 + 预计算的指针类型（值与指针方法集都查）。
type pooledType struct {
	named *types.Named
	ptr   *types.Pointer
}

type implKey struct {
	iface  *types.Named
	method string
}

// newImplTypePool 扫描模块内包，收集全部包级具名类型。接口类型也留在
// 池内——methodsFor 内按「接口自身」排除（与旧实现一致：旧实现是
// 在候选循环里 named == iface 跳过）。
func newImplTypePool(pkgs []*types.Package, modules []string) *implTypePool {
	logger := zap.L()
	logger.Debug("enter newImplTypePool")
	defer logger.Debug("exit newImplTypePool")
	p := &implTypePool{cached: map[implKey][]*types.Func{}}
	for _, pkg := range pkgs {
		if pkg == nil || !isInModule(pkg.Path(), modules) {
			continue
		}
		scope := pkg.Scope()
		for _, name := range scope.Names() {
			tn, ok := scope.Lookup(name).(*types.TypeName)
			if !ok {
				continue
			}
			named, ok := tn.Type().(*types.Named)
			if !ok {
				continue
			}
			p.types = append(p.types, pooledType{named: named, ptr: types.NewPointer(named)})
		}
	}
	return p
}

// Len 返回池内类型数（测试/日志用）。
func (p *implTypePool) Len() int {
	if p == nil {
		return 0
	}
	return len(p.types)
}

// methodsFor 枚举模块内实现 iface.method 的具名类型方法（值与指针方法集
// 都查）。返回切片由池持有——调用方只读，不得修改。
func (p *implTypePool) methodsFor(iface *types.Named, method string) []*types.Func {
	if p == nil || iface == nil {
		return nil
	}
	key := implKey{iface: iface, method: method}
	p.mu.RLock()
	out, ok := p.cached[key]
	p.mu.RUnlock()
	if ok {
		return out
	}

	out = p.compute(iface, method)

	p.mu.Lock()
	if prev, ok := p.cached[key]; ok { // 并发同键：保留先到者，保证同键返回同一底层数组
		p.mu.Unlock()
		return prev
	}
	p.cached[key] = out
	p.mu.Unlock()
	return out
}

// compute 未命中时的实际枚举（无锁；结果由 methodsFor 落 memo）。
func (p *implTypePool) compute(iface *types.Named, method string) []*types.Func {
	// iface 非接口不应出现（调用方只传 interfaceNamedOf 的结果）；
	// 旧实现直接断言会 panic，此处返回空更稳
	it, ok := iface.Underlying().(*types.Interface)
	if !ok {
		return nil
	}
	var out []*types.Func
	for _, t := range p.types {
		if t.named == iface {
			continue
		}
		if !types.Implements(t.named, it) && !types.Implements(t.ptr, it) {
			continue
		}
		if fn := findMethod(t.named, method); fn != nil {
			out = append(out, fn)
		}
	}
	return out
}

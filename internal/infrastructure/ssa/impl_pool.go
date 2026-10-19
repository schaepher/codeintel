package ssa

import (
	"go/types"
	"sync"
	"sync/atomic"

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
// 改造三点，语义与旧实现逐条对齐：
//  1. 类型池 Index 级构建一次（只扫一遍各包 scope，顺带预计算
//     *types.Pointer），types.NewPointer 不再按 (调用点 × 类型) 分配
//  2. 实现类型集合**按接口缓存**——同一接口的不同方法（Save/Load...）
//     复用同一份 types.Implements 扫描结果，不再逐方法重扫全库类型
//  3. 结果按 (接口, 方法名) 过滤——方法级切片由调用方只读
type implTypePool struct {
	// types 模块内全部包级具名类型（Q246：Index 级一次扫描）
	types []pooledType
	mu    sync.RWMutex
	// cached 实现类型集合（键为接口类型指针——池与程序同生命周期；
	// todo8-13：entry.ready 让并发首次只计算一次）
	cached map[*types.Named]*implEntry
	// methods 候选方法二级缓存（键为接口 + 方法名；同上）
	methods map[implMethodKey]*methodEntry
	// computes 实现集合计算次数（测试断言「同接口只算一次」用；并发安全）
	computes atomic.Int64
	// methodComputes 方法过滤次数（测试断言「同 (接口,方法) 只过滤一次」）。
	methodComputes atomic.Int64
	// funcs 构建级函数快照（todo8-5：方法集缓存；SetFuncSnapshot 注入，
	// 未注入时 findMethodCached 回退直接构建）
	funcs *funcSnapshot
}

// SetFuncSnapshot 注入构建级快照（findMethod 方法集缓存用）。
func (p *implTypePool) SetFuncSnapshot(s *funcSnapshot) {
	if p != nil {
		p.funcs = s
	}
}

// implEntry 实现集合缓存条目（ready 关闭即可读 types）。
type implEntry struct {
	ready chan struct{}
	types []pooledType
}

// methodEntry 方法过滤缓存条目（ready 关闭即可读 fns）。
type methodEntry struct {
	ready chan struct{}
	fns   []*types.Func
}

// implMethodKey 候选方法缓存键（接口类型指针 + 方法名）。
type implMethodKey struct {
	iface  *types.Named
	method string
}

// pooledType 池内类型：具名类型 + 预计算的指针类型（值与指针方法集都查）。
type pooledType struct {
	named *types.Named
	ptr   *types.Pointer
}

// newImplTypePool 扫描模块内包，收集全部包级具名类型。接口类型也留在
// 池内——implsFor 内按「接口自身」排除（与旧实现一致：旧实现是
// 在候选循环里 named == iface 跳过）。
func newImplTypePool(pkgs []*types.Package, modules []string) *implTypePool {
	logger := zap.L()
	logger.Debug("enter newImplTypePool")
	defer logger.Debug("exit newImplTypePool")
	p := &implTypePool{cached: map[*types.Named]*implEntry{}, methods: map[implMethodKey]*methodEntry{}}
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
// 都查）。返回切片为新建只读切片——调用方只读，不得修改。
func (p *implTypePool) methodsFor(iface *types.Named, method string) []*types.Func {
	if p == nil || iface == nil {
		return nil
	}
	key := implMethodKey{iface: iface, method: method}
	p.mu.Lock()
	if e, ok := p.methods[key]; ok {
		p.mu.Unlock()
		<-e.ready
		return e.fns
	}
	e := &methodEntry{ready: make(chan struct{})}
	p.methods[key] = e
	p.mu.Unlock()
	// 不持 p.mu 调用 implsFor（两层缓存各自等待，避免死锁）
	impls := p.implsFor(iface)
	var res []*types.Func
	for _, t := range impls {
		if fn := findMethodCached(p.funcs, t.named, method); fn != nil {
			res = append(res, fn)
		}
	}
	p.methodComputes.Add(1)
	e.fns = res
	close(e.ready)
	return res
}

// implsFor 返回实现 iface 的具名类型集合（按接口缓存——同一接口的不同
// 方法共用一次全库 types.Implements 扫描）。
func (p *implTypePool) implsFor(iface *types.Named) []pooledType {
	p.mu.Lock()
	if e, ok := p.cached[iface]; ok {
		p.mu.Unlock()
		<-e.ready
		return e.types
	}
	e := &implEntry{ready: make(chan struct{})}
	p.cached[iface] = e
	p.mu.Unlock()
	// 无锁计算（不同接口并行）；同键并发者等待 ready
	e.types = p.computeImpls(iface)
	close(e.ready)
	return e.types
}

// computeImpls 未命中时的实际枚举（无锁；结果由 implsFor 落 memo）。
func (p *implTypePool) computeImpls(iface *types.Named) []pooledType {
	p.computes.Add(1)
	// iface 非接口不应出现（调用方只传 interfaceNamedOf 的结果）；
	// 旧实现直接断言会 panic，此处返回空更稳
	it, ok := iface.Underlying().(*types.Interface)
	if !ok {
		return nil
	}
	var out []pooledType
	for _, t := range p.types {
		if t.named == iface {
			continue
		}
		if !types.Implements(t.named, it) && !types.Implements(t.ptr, it) {
			continue
		}
		out = append(out, t)
	}
	return out
}

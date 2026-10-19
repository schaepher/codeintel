package ssa

// todo8-5：具名类型的值/指针方法集构建级复用（findMethod 不再每次
// types.NewMethodSet×2）。

import "go/types"

// typeMethodSets 具名类型的值方法集与指针方法集（构建级只读）。
type typeMethodSets struct {
	value *types.MethodSet
	ptr   *types.MethodSet
}

// methodSetOf 取具名类型的值/指针方法集（缓存；nil 快照回退直接构建）。
func (s *funcSnapshot) methodSetOf(named *types.Named) (*types.MethodSet, *types.MethodSet) {
	if named == nil {
		return nil, nil
	}
	if s == nil {
		return types.NewMethodSet(named), types.NewMethodSet(types.NewPointer(named))
	}
	s.msMu.Lock()
	if v, ok := s.methodSets[named]; ok {
		s.msMu.Unlock()
		return v.value, v.ptr
	}
	s.msMu.Unlock()
	// 构建在锁外（幂等；并发首访最多重复构建一次）
	v := &typeMethodSets{value: types.NewMethodSet(named), ptr: types.NewMethodSet(types.NewPointer(named))}
	s.msMu.Lock()
	if prev, ok := s.methodSets[named]; ok {
		s.msMu.Unlock()
		return prev.value, prev.ptr
	}
	if s.methodSets == nil {
		s.methodSets = map[*types.Named]*typeMethodSets{}
	}
	s.methodSets[named] = v
	s.msMu.Unlock()
	return v.value, v.ptr
}

// findMethodCached 与 findMethod 同语义（先值方法集、后指针方法集，
// Lookup(nil, name)），方法集走构建级缓存；不直接用 Named.Method（会漏
// 嵌入提升方法）。
func findMethodCached(funcs *funcSnapshot, t types.Type, name string) *types.Func {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok {
		return nil
	}
	value, ptr := funcs.methodSetOf(named)
	for _, ms := range []*types.MethodSet{value, ptr} {
		if ms == nil {
			continue
		}
		if sel := ms.Lookup(nil, name); sel != nil {
			if fn, ok := sel.Obj().(*types.Func); ok {
				return fn
			}
		}
	}
	return nil
}

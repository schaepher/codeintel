package ssa

// todo5-2：动态类型查询的构建级缓存（避免每次全包扫描）。
//
// 原实现 dynamicTypeOf 每次遍历 prog.AllPackages() 做 Scope().Lookup +
// Type().String() 比对（dispatch 注册/派发发射按动态类型重复调用）。
// 现改为：
//   - 包快照在 newFuncSnapshot 时建立（按包路径排序——同名不同包的选择
//     稳定，不随 map 迭代顺序变）
//   - 按 typeStr 缓存**成功与失败**（nil）结果；缓存挂在 funcSnapshot
//     （按构建创建，随其释放，不跨 SSA Program/构建复用）
//   - 具名/指针/别名语义与旧实现一致（仍按 obj.Type().String() 匹配，
//     指针转 types.NewPointer(named)）

import (
	"go/types"
	"strings"
)

// dynTypeEntry 动态类型查找缓存条目（done 区分“未查”与“查了没有”）。
type dynTypeEntry struct {
	typ  types.Type
	done bool
}

// dynamicTypeOf 查询动态类型（缓存；s 为 nil 时回退直接扫描空快照）。
func (s *funcSnapshot) dynamicTypeOf(typeStr string) types.Type {
	if s == nil {
		return nil
	}
	s.dynMu.RLock()
	e, ok := s.dynTypes[typeStr]
	s.dynMu.RUnlock()
	if ok && e.done {
		return e.typ
	}
	// 写锁内二次检查后计算——并发首次查询只扫描一次（缓存含失败结果）
	s.dynMu.Lock()
	defer s.dynMu.Unlock()
	if e, ok := s.dynTypes[typeStr]; ok && e.done {
		return e.typ
	}
	t := resolveDynamicType(typeStr, s.pkgs)
	if s.dynTypes == nil {
		s.dynTypes = map[string]dynTypeEntry{}
	}
	s.dynTypes[typeStr] = dynTypeEntry{typ: t, done: true}
	return t
}

// resolveDynamicType 旧实现逻辑（在给定包快照上扫描），保留作对照/等价测试。
func resolveDynamicType(typeStr string, pkgs []*types.Package) types.Type {
	ptr := strings.HasPrefix(typeStr, "*")
	full := strings.TrimPrefix(typeStr, "*")
	name := full
	if i := strings.LastIndex(full, "."); i >= 0 {
		name = full[i+1:]
	}
	for _, pkg := range pkgs {
		obj := pkg.Scope().Lookup(name)
		if obj == nil || obj.Type() == nil || obj.Type().String() != full {
			continue
		}
		if ptr {
			if named, ok := obj.Type().(*types.Named); ok {
				return types.NewPointer(named)
			}
		}
		return obj.Type()
	}
	return nil
}

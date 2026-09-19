package ssa

// Q249：程序函数全集快照（Index 级一次）。
//
// 原状：`ssautil.AllFunctions(prog)` 在构建期被调用 6 处
// （collectDispatchRegistrations / emitDispatches / emitGlobalInit /
// collectOrmMappings / computeAliases / byPkg 建索引），每次都全程序走
// 一遍所有函数体、遍历指令操作数并建 map——go2o 实测累计分配 213MB
// （占 6.7%）；另有 summary_dynsql 的 `ext.funcCache` 把结果**按函数**
// 物化成 []*ssa.Function（每个用到的 extractor 各一份）。
//
// 改为 Index 级一份快照（`Adapter.funcs`）：list 供需要全程序扫描的
// 调用方（summary_dynsql 找静态调用点），moduleFuncs 供"只看模块内
// 函数"的调用方（集中过滤一次）。list 顺序固定 → 比原先"每次遍历一个
// map"更确定。
//
// 为什么快照一次是安全的：
//   - 模块内函数在 `sp.Build()` 循环结束后已全部存在（emit 阶段不会再
//     新增模块函数）
//   - AllFunctions 自身会通过 prog.MethodValue 物化方法包装函数——**第一次
//     调用**就把它们建出来并纳入结果，后续调用只会命中已有包装
//   - 我们自己的代码不调用 MethodValue（仅 go/ssa 构建器在 sp.Build 阶段
//     调用），故不存在"晚些调用能看到更多函数"的情况

import (
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// funcSnapshot 程序函数全集（Index 级快照）。
type funcSnapshot struct {
	list []*ssa.Function
}

// newFuncSnapshot 采集一次（内部即 ssautil.AllFunctions）。
func newFuncSnapshot(prog *ssa.Program) *funcSnapshot {
	set := ssautil.AllFunctions(prog)
	list := make([]*ssa.Function, 0, len(set))
	for fn := range set {
		list = append(list, fn)
	}
	return &funcSnapshot{list: list}
}

// all 全程序函数列表（含合成包装；只读，调用方不得修改/排序）。
func (s *funcSnapshot) all() []*ssa.Function {
	if s == nil {
		return nil
	}
	return s.list
}

// moduleFuncs 模块内函数（集中过滤一次，各消费者共享同一份只读切片）。
func (s *funcSnapshot) moduleFuncs(modules []string) []*ssa.Function {
	if s == nil {
		return nil
	}
	out := make([]*ssa.Function, 0, len(s.list))
	for _, fn := range s.list {
		if isModuleFunction(fn, modules) {
			out = append(out, fn)
		}
	}
	return out
}

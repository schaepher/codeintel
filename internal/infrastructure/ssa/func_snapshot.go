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
	"go/types"
	"sort"
	"strings"
	"sync"

	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// funcSnapshot 程序函数全集（Index 级快照）。
//
// 除函数列表外，还承载**构建级只读缓存**（Q?：重复解析/重复计算整
// 治）——函数身份、静态调用点反向索引、Return 操作数、类型表名、SQL
// 解析结果。extractor 按包并发，各缓存自带锁；缓存只增不改（空结果也
// 缓存），生命周期与 funcSnapshot 一致（不跨 SSA Program）。
type funcSnapshot struct {
	list []*ssa.Function

	identMu sync.Mutex
	idents  map[*types.Func]funcIdent

	callSitesOnce sync.Once
	callSites     map[*ssa.Function][][]ssa.Value

	retMu sync.Mutex
	rets  map[*ssa.Function][][]ssa.Value

	tableMu    sync.Mutex
	tableNames map[*types.Named]string

	sqlMu        sync.Mutex
	sqlCache     map[string]sqlParseEntry
	sqlCacheFull bool

	// todo5-2：动态类型查询（Mi.X.Type().String() → types.Type）。
	// pkgs 为**排序后的包快照**（同名不同包时按路径稳定选择）；dynTypes
	// 缓存成功与失败（nil）结果——按构建创建，随 funcSnapshot 释放。
	pkgs       []*types.Package
	pkgsByName map[string][]*types.Package
	pkgsByPath map[string][]*types.Package
	dynMu      sync.RWMutex
	dynTypes   map[string]dynTypeEntry

	// todo6-2：模块函数过滤结果复用（同一构建 + 同一模块集合只过滤一次；
	// 只读切片，随 funcSnapshot 释放）。
	moduleMu    sync.RWMutex
	moduleCache map[string][]*ssa.Function

	// todo8-5：具名类型值/指针方法集（findMethod 构建级复用）。
	msMu       sync.Mutex
	methodSets map[*types.Named]*typeMethodSets

	// 构建级字符串缓存（string_cache.go）：类型字符串 + 候选键。
	strMu    sync.RWMutex
	typeStrs map[types.Type]string
	candKeys map[*types.Func]string

	// 构建级"重复构建"缓存（func_snapshot_cache.go）：
	//   snippets  = 调用点源码片段（cc.String() 昂贵，同一调用点每字段/列复用）
	//   owners    = 实参嵌套 owner 类型路径（ownerTypesOf 递归纯函数）
	//   sk        = 摘要键（summaryKey 纯函数）
	snippetMu sync.Mutex
	snippets  map[*ssa.CallCommon]string
	ownerMu   sync.Mutex
	owners    map[types.Type][]string
	skMu      sync.Mutex
	sk        map[*ssa.Function]string

	// snakes = snakeCase 缓存（字段/类型名重复率高）
	snakeMu sync.RWMutex
	snakes  map[string]string
}

// newFuncSnapshot 采集一次（内部即 ssautil.AllFunctions）。
func newFuncSnapshot(prog *ssa.Program) *funcSnapshot {
	set := ssautil.AllFunctions(prog)
	// 实验：排序消除 map 迭代顺序依赖（跨函数处理顺序影响 slot 归属）
	// Q252e：比较器里现算 String()（内部 RelString 拼字符串）在
	// AllFunctions 量级（含依赖，百万级）是 O(n log n) 次字符串构造——
	// 预计算 key 后是 O(n)。只减分配，不是热点（采样仅 0.13% 栈顶）。
	list := make([]*ssa.Function, 0, len(set))
	keys := make([]string, 0, len(set))
	for fn := range set {
		list = append(list, fn)
		keys = append(keys, fn.String())
	}
	sort.Sort(&funcKeySorter{list: list, keys: keys})
	// 包快照（排序保证同名不同包时的选择稳定）
	all := prog.AllPackages()
	pkgs := make([]*types.Package, 0, len(all))
	for _, ap := range all {
		if ap != nil && ap.Pkg != nil {
			pkgs = append(pkgs, ap.Pkg)
		}
	}
	// 排序键 (Path, Name)：同路径多个包对象时顺序稳定。
	sort.SliceStable(pkgs, func(i, j int) bool {
		if pkgs[i].Path() != pkgs[j].Path() {
			return pkgs[i].Path() < pkgs[j].Path()
		}
		return pkgs[i].Name() < pkgs[j].Name()
	})
	// todo6-1：包索引（名字/路径 → 包对象，保持排序后的候选顺序）
	byName := make(map[string][]*types.Package, len(pkgs))
	byPath := make(map[string][]*types.Package, len(pkgs))
	for _, p := range pkgs {
		byName[p.Name()] = append(byName[p.Name()], p)
		byPath[p.Path()] = append(byPath[p.Path()], p)
	}
	return &funcSnapshot{list: list, pkgs: pkgs, pkgsByName: byName, pkgsByPath: byPath}
}

// funcKeySorter 按预计算 key 排序（避免比较器内重复构造字符串）。
type funcKeySorter struct {
	list []*ssa.Function
	keys []string
}

func (s *funcKeySorter) Len() int           { return len(s.list) }
func (s *funcKeySorter) Less(i, j int) bool { return s.keys[i] < s.keys[j] }
func (s *funcKeySorter) Swap(i, j int) {
	s.list[i], s.list[j] = s.list[j], s.list[i]
	s.keys[i], s.keys[j] = s.keys[j], s.keys[i]
}

// all 全程序函数列表（含合成包装；只读，调用方不得修改/排序）。
func (s *funcSnapshot) all() []*ssa.Function {
	if s == nil {
		return nil
	}
	return s.list
}

// moduleFuncs 模块内函数（集中过滤一次，各消费者共享同一份只读切片；
// todo6-2：结果按模块集合缓存——快照初始化/别名/全局初始化/派发分析复用）。
func (s *funcSnapshot) moduleFuncs(modules []string) []*ssa.Function {
	if s == nil {
		return nil
	}
	key := moduleSetKey(modules)
	s.moduleMu.RLock()
	if v, ok := s.moduleCache[key]; ok {
		s.moduleMu.RUnlock()
		return v
	}
	s.moduleMu.RUnlock()
	// 写锁内二次检查后过滤——并发首次访问只遍历一次
	s.moduleMu.Lock()
	defer s.moduleMu.Unlock()
	if v, ok := s.moduleCache[key]; ok {
		return v
	}
	out := make([]*ssa.Function, 0, len(s.list))
	for _, fn := range s.list {
		if isModuleFunction(fn, modules) {
			out = append(out, fn)
		}
	}
	if s.moduleCache == nil {
		s.moduleCache = map[string][]*ssa.Function{}
	}
	s.moduleCache[key] = out
	return out
}

// moduleSetKey 模块集合的稳定键（集合语义：顺序无关；不同集合不复用）。
func moduleSetKey(modules []string) string {
	// 单 module 仓库（绝大多数）零分配快路径。
	switch len(modules) {
	case 0:
		return ""
	case 1:
		return modules[0]
	}
	cp := append([]string(nil), modules...)
	sort.Strings(cp)
	return strings.Join(cp, "\x00")
}

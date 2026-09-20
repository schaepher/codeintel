// Package ssa 实现字段追溯适配器（docs/field_trace.md v2.2）。
// 基于 go/packages + go/ssa 构建 SSA IR，产出字段访问节点与数据流边，
// 接替 2026-08-13 移除的 Joern 适配器（TD.md 12.7）。
//
// Phase 1（骨架）：加载 + SSA 构建，发射函数/方法节点（保证后续边端点存在）。
// Phase 2+：字段提取（field_access + data_flows_to）、跨过程边、间接写、摘要。
package ssa

import (
	"fmt"
	"go/token"
	"go/types"
	"os"
	"runtime"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/progress"
	"go.uber.org/zap"
)

var _ domain.IndexerPort = (*Adapter)(nil)

// Adapter 是 SSA 字段追溯适配器。
type Adapter struct {
	// fd 摘要收集（构建期内存态）：function_field_summary 预计算用
	fd map[domain.CanonicalID]*funcData
	// dispatchRegs 接口注册点缓存（Q161 动态边候选元数据）：Index 级
	// 共享一次扫描——放 extractor（每函数新建）会每函数全 prog 扫描
	dispatchRegs dispatchReg
	// regHits Q221：注册命中预处理表（Q168 O(1) 判定）——Index 级一次，
	// 原 extractor 懒构建每函数重复遍历全部注册点方法
	regHits regHits
	// typeMapping Q211：orm.Mapping 实体类型→表名注册（Index 级收集，
	// 发射前全量扫描——规避按包处理顺序：Mapping 可能在包 A 注册、
	// 包 B 使用）
	typeMapping map[*types.Named]string
	// workers 按包并发数（Q169/Q170）：默认 1=串行；命令行 --workers N
	// 指定（orchestrator SetWorkers 注入）
	workers int
	// progress 进度上报（Q253，orchestrator 注入）；nil = 逐行打到 stderr
	progress      domain.Progress
	plainProgress *progress.Plain // 逐行保底实现（懒建，复用）
	// lines Q248：Index 级共享源码行缓存（extractor 与 aliasPass 共用，
	// 每轮 Index 新建——原实现每函数一份，占全部分配 25%）
	lines *lineCache
	// funcs Q249：程序函数全集快照（Index 级一次；原实现 6 处各自
	// ssautil.AllFunctions 全程序扫描）
	funcs *funcSnapshot
	// dispatchPkgs P0-2：dispatch 相关模块内包（注册点包 ∪ 动态调用
	// 包）——本轮 Index 运行收集，构建后供 orchestrator 持久化到
	// build_metadata（增量补 Load 用）
	dispatchPkgs []string
}

// SetProgress 注入进度上报（Q253：orchestrator/cli 传入）。
//
// 逐行（plain）模式下换用自己的保底实现：这样 SSA 子步骤保持改动前的
// `[index] ssa 步骤 X（41ms, heap 170MB/189MB inuse）` 格式（前缀 + heap
// 统计——Q247/Q248 的内存排查靠这行）；TTY 模式则共用同一个渲染器，
// 子步骤以缩进层呈现。
func (a *Adapter) SetProgress(p domain.Progress) {
	if plain, ok := p.(*progress.Plain); ok {
		a.progress = &progress.Plain{W: plain.W, Prefix: "[index] ssa 步骤", Detail: heapDetail}
		return
	}
	a.progress = p
}

// prog 返回进度实现（注入优先，否则逐行 stderr，含 heap 统计）。
func (a *Adapter) prog() domain.Progress {
	if a.progress != nil {
		return a.progress
	}
	if a.plainProgress == nil {
		a.plainProgress = &progress.Plain{W: os.Stderr, Prefix: "[index] ssa 步骤", Detail: heapDetail}
	}
	return a.plainProgress
}

// heapDetail 逐行模式的附加信息（Q247/Q248 的内存排查靠这行）。
func heapDetail() string {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return fmt.Sprintf("heap %dMB/%dMB inuse", int64(ms.HeapAlloc>>20), int64(ms.HeapInuse>>20))
}

// SetWorkers 设置按包并发数（Q170：--workers 参数；≤1 退串行）。
func (a *Adapter) SetWorkers(n int) {
	a.workers = n
}

// Name 实现 IndexerPort。
func (a *Adapter) Name() string {
	return "ssa"
}

// DispatchPkgs 返回本轮 Index 收集的 dispatch 相关模块内包路径
// （P0-2：增量构建补 Load 用）。
func (a *Adapter) DispatchPkgs() []string {
	logger := zap.L()
	logger.Debug("enter (Adapter).DispatchPkgs")
	defer logger.Debug("exit (Adapter).DispatchPkgs")
	return a.dispatchPkgs
}

// assignTarget 赋值表达式区间 → 目标变量名。
type assignTarget struct {
	name  string
	start token.Pos
	end   token.Pos
	// Q193：RHS 直接调用（顶层表达式是 CallExpr）的 '(' 位置——go/ssa
	// 的 Call.Pos 语义（嵌套子调用不记录，其 Pos 无法恢复为变量名）
	topCallPos token.Pos
}

package sqlite

// P1-02：全扫类查询的进程内投影缓存（按 build_id）。
//
// 背景：Q252d 的邻接表缓存（edge_graph.go）只接了 GetPath。P1-02 逐个
// 核查了 4 处"每次调用都把整批边/节点读进内存再建 map"的消费者：
//
//	repo_chain.go   GetImplementsEdges（接口具体化——wiki 时序/证据按页调用）
//	repo_flows.go   GetAllCalls（wiki 模块页包间调用图）
//	repo_unused.go  calls/引用/var 初始化边 + 函数清单（unused 两档报告）
//	rg_bfs.go       loadRelationGraph 的全边集 + 节点属性
//
// 前 3 处已接入（同一进程内会被反复调用：一次 `query unused` 就先后走
// GetUncalledFunctions + GetIsolatedChains，serve/MCP 更是常驻复用）。
//
// rg_bfs **不接入**：它的派生结果已有进程内缓存（rg_cache.go 的
// relationGraph，键含 build_id + 分析逻辑版本），常态下每个构建只加载
// 一次；再缓存一份原始行（7.5M 边级别 ≈ 上百 MB）只增加常驻内存、没有
// 第二次加载可省。超大图（超 shouldCacheGraph 阈值）走 SQL 路径，
// 本就不加载内存图。
//
// 这些查询的**原始行**在同一构建内不变（边/节点语义变化必然伴随重建 →
// build_id 变），而各自的派生逻辑（过滤、建 map、BFS）留在消费者里，
// 故缓存"投影行"而不是"派生结果"：
//
//   - 键 = build_id：增量构建写新 build_id 自动失效；本文件改动不需要
//     额外版本常量（与 edge_graph.go 同理，缓存的是原始行）
//   - 无 build_metadata（build_id 空）不缓存，每次现查（裸库/测试语义）
//   - 只读共享：加载完成后切片/map 不再修改（并发读安全）；锁只保护
//     槽位；同一视图的并发首次请求共享一次加载（entry.ready），失败不
//     缓存（下次重试）
//   - 规模上限：行数超 maxCachedProjectionRows 不缓存（防超大图内存
//     翻倍）——本次调用照常返回，等待中的并发请求共享同一份
//   - 诊断：loads 计数（每个视图实际扫表次数）供测试/日志核对

import (
	"fmt"
	"sync"

	"go.uber.org/zap"
)

// maxCachedProjectionRows 单视图投影缓存的行数上限。
// 行内含 canonical ID（≈145B 字符串 ×1–2）——200k 行约 60–100MB/视图；
// 变量而非常量：测试用小阈值覆盖"超限不缓存"路径。
var maxCachedProjectionRows = 200_000

// projEntry 单视图加载状态：ready 关闭即结果可用（并发首次共享一次加载）。
type projEntry struct {
	ready chan struct{}
	rows  any
	err   error
}

// projCache 一个 build_id 下的投影视图集合（只增不改，随 build_id 换代）。
type projCache struct {
	key   string
	mu    sync.Mutex
	projs map[string]*projEntry
	loads map[string]int // 实际扫表次数（诊断）
}

// projectionCache 取当前 build_id 的缓存槽（必要时换代）。调用方持 r.projMu。
func (r *Repo) projectionCache(buildID string) *projCache {
	logger := zap.L()
	logger.Debug("enter (Repo).projectionCache")
	defer logger.Debug("exit (Repo).projectionCache")
	if r.projCacheKey == buildID && r.projCache != nil {
		return r.projCache
	}
	r.projCacheKey = buildID
	r.projCache = &projCache{key: buildID, projs: map[string]*projEntry{}, loads: map[string]int{}}
	return r.projCache
}

// cachedProjection 取（必要时加载）某个视图的原始行集合。
//
// label 是视图标识（不同消费者的过滤条件/返回字段各不相同 → 各自一个
// 标签）；load 返回行切片、行数（规模判定用）与错误。同一标签的并发
// 首次请求只执行一次 load。
func cachedProjection[T any](r *Repo, label string, load func() (T, int, error)) (T, error) {
	var zero T
	buildID := r.currentBuildID()
	if buildID == "" {
		// 无 build_metadata：不缓存（与 relations/邻接表同语义）
		rows, _, err := load()
		return rows, err
	}
	r.projMu.Lock()
	c := r.projectionCache(buildID)
	c.mu.Lock()
	if e, ok := c.projs[label]; ok {
		c.mu.Unlock()
		r.projMu.Unlock()
		<-e.ready // 首次加载完成（close 后发 happens-before）
		if e.err != nil {
			return zero, e.err
		}
		rows, ok := e.rows.(T)
		if !ok {
			return zero, fmt.Errorf("projection %q 类型不匹配", label)
		}
		return rows, nil
	}
	e := &projEntry{ready: make(chan struct{})}
	c.projs[label] = e
	c.mu.Unlock()
	r.projMu.Unlock()

	// 加载在锁外（不同视图可并行；同一视图的等待者阻塞在 ready 上）
	rows, n, err := load()

	c.mu.Lock()
	c.loads[label]++
	e.rows = rows
	e.err = err
	if err != nil || n > maxCachedProjectionRows {
		// 失败/超限：不缓存，后续调用重新加载（本次等待者共享这份结果）
		delete(c.projs, label)
	}
	c.mu.Unlock()
	close(e.ready)
	if err != nil {
		return zero, err
	}
	return rows, nil
}

// projectionLoadCount 视图实际扫表次数（诊断/测试）。
func (r *Repo) projectionLoadCount(label string) int {
	logger := zap.L()
	logger.Debug("enter (Repo).projectionLoadCount")
	defer logger.Debug("exit (Repo).projectionLoadCount")
	buildID := r.currentBuildID()
	if buildID == "" {
		return 0
	}
	r.projMu.Lock()
	defer r.projMu.Unlock()
	if r.projCacheKey != buildID || r.projCache == nil {
		return 0
	}
	r.projCache.mu.Lock()
	defer r.projCache.mu.Unlock()
	return r.projCache.loads[label]
}

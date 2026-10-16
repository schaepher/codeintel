package sqlite

// 构建期端点正向缓存（canonical ID → nodes.id_int）。
//
// 动机：每批边都要把端点 canonical ID 解析成整数代理键；同一端点会在
// 后续批次的边里反复出现（调用图/值流端点高度重叠），重复走 SQL 查询。
// 构建期把**已成功提交**的正向解析结果缓存起来，命中即跳过查询。
//
// 语义约束（严格保守）：
//   - 只缓存正向（已存在）结果，**不缓存缺失端点**——缺失端点在后续批次
//     节点落库后必须能重新解析到（否则边会永久丢失）
//   - 只在事务**提交成功后**发布——回滚/失败批次的新节点 id_int 未持久化，
//     提前发布会让后续边引用不存在的行
//   - 容量上限 32768（避免大仓全量端点常驻内存）；淘汰策略为两代分段
//     轮换（见 buildWriteCache 注释）——对照整表清空的命中率差异在
//     docs/codeintel-gc-investigation.md 记录
//   - 不跨构建复用：由编排器在构建生命周期内 Begin/End（build_write_cache.go
//     只负责缓存本体与统计，生命周期由 Repo 持有 + 编排器驱动）

import (
	"database/sql"
	"sync"

	"github.com/schaepher/codeintel/internal/domain"
)

// buildWriteCacheCapacity 正向缓存容量上限（项）。
const buildWriteCacheCapacity = 32768

// buildWriteCache 端点正向缓存 + 统计（可并发访问）。
//
// 淘汰策略（P2）：两代分段轮换（segmented）——比"容量满整表清空"
// （大仓实测命中率仅 13.1%）保留更多热键：cur 为新一代，prev 为上一代；
// cur 达 capacity/2 时 prev←cur、cur←empty，prev 在下次轮换时丢弃。
// partition 命中 prev 时在 cur 有空间时晋昇（防 cur 超限）。总条数
// ≤ capacity。合成幂律序列对照（同容量 32768）命中率 9.1% → 12.6%。
type buildWriteCache struct {
	mu       sync.Mutex
	cur      map[string]int64
	prev     map[string]int64
	capacity int

	inputs    int64 // 查过的（去重后）端点输入数
	writes    int64 // 发布的正向条目数
	hits      int64 // 命中数
	rotations int64 // 分段轮换次数
}

// WriteCacheStats 构建期端点缓存统计（构建结束日志用）。
type WriteCacheStats struct {
	Inputs    int64
	Writes    int64
	Hits      int64
	Rotations int64
	Size      int64
}

func newBuildWriteCache(capacity int) *buildWriteCache {
	if capacity <= 0 {
		capacity = buildWriteCacheCapacity
	}
	return &buildWriteCache{
		cur:      make(map[string]int64, capacity/4),
		capacity: capacity,
	}
}

// partition 把 ids 分为命中（直接写入 refs）与未命中（返回）——合并
// 查重与结果状态，避免额外 hits map。ids 应已去重。prev 命中且有空间时
// 晋升到 cur。
func (c *buildWriteCache) partition(refs map[string]int64, ids []string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.inputs += int64(len(ids))
	half := c.capacity / 2
	miss := make([]string, 0, len(ids))
	for _, id := range ids {
		if v, ok := c.cur[id]; ok {
			refs[id] = v
			c.hits++
			continue
		}
		if v, ok := c.prev[id]; ok {
			refs[id] = v
			c.hits++
			if len(c.cur) < half {
				c.cur[id] = v
			}
			continue
		}
		miss = append(miss, id)
	}
	return miss
}

// publish 事务提交成功后写入正向结果；cur 达 capacity/2 时分段轮换
// （prev←cur，cur 清空），旧 prev 随之丢弃。
func (c *buildWriteCache) publish(fresh map[string]int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	half := c.capacity / 2
	for id, v := range fresh {
		if len(c.cur) >= half {
			c.prev = c.cur
			c.cur = make(map[string]int64, half)
			c.rotations++
		}
		c.cur[id] = v
		c.writes++
	}
}

func (c *buildWriteCache) snapshot() WriteCacheStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return WriteCacheStats{
		Inputs:    c.inputs,
		Writes:    c.writes,
		Hits:      c.hits,
		Rotations: c.rotations,
		Size:      int64(len(c.cur) + len(c.prev)),
	}
}

// BeginBuildWriteCache 开启构建期端点正向缓存（编排器在适配器阶段开始时调用）。
func (r *Repo) BeginBuildWriteCache() {
	r.buildWriteMu.Lock()
	r.buildWrite = newBuildWriteCache(buildWriteCacheCapacity)
	r.buildWriteMu.Unlock()
}

// EndBuildWriteCache 关闭并清空缓存（不跨构建复用），返回统计。
func (r *Repo) EndBuildWriteCache() WriteCacheStats {
	r.buildWriteMu.Lock()
	c := r.buildWrite
	r.buildWrite = nil
	r.buildWriteMu.Unlock()
	if c == nil {
		return WriteCacheStats{}
	}
	return c.snapshot()
}

// currentBuildWriteCache 取当前缓存（未启用返回 nil）。
func (r *Repo) currentBuildWriteCache() *buildWriteCache {
	r.buildWriteMu.Lock()
	c := r.buildWrite
	r.buildWriteMu.Unlock()
	return c
}

// publishBuildWriteCache 提交成功后发布本批正向结果（缓存未启用时 no-op）。
func (r *Repo) publishBuildWriteCache(fresh map[string]int64) {
	if len(fresh) == 0 {
		return
	}
	if c := r.currentBuildWriteCache(); c != nil {
		c.publish(fresh)
	}
}

// resolveNodeRefsCached 带构建期正向缓存的端点解析（直接消费 edges，
// 不先建 edgeEndpointIDs 大切片）。返回全部 refs 与 **本批新解析到的
// 正向结果** fresh（供提交成功后发布）。缓存未启用时退化为
// resolveNodeRefs（fresh = refs，publish 为 no-op）。
func (r *Repo) resolveNodeRefsCached(tx *sql.Tx, edges []*domain.Fact) (refs map[string]int64, fresh map[string]int64, err error) {
	cache := r.currentBuildWriteCache()
	if cache == nil {
		refs, err = resolveNodeRefs(tx, edgeEndpointIDs(edges))
		if err != nil {
			return nil, nil, err
		}
		return refs, refs, nil
	}
	// 单遍去重：seen 查重，uniq 同时作为缓存查询输入（避免 edgeEndpointIDs 中间切片）
	n := len(edges) * 2
	seen := make(map[string]struct{}, n)
	uniq := make([]string, 0, n)
	for _, e := range edges {
		for _, id := range [2]string{string(e.SourceID), string(e.TargetID)} {
			if id == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			uniq = append(uniq, id)
		}
	}
	refs = make(map[string]int64, len(uniq))
	miss := cache.partition(refs, uniq)
	if len(miss) == 0 {
		return refs, nil, nil
	}
	got, rerr := resolveNodeRefsUnique(tx, miss)
	if rerr != nil {
		return nil, nil, rerr
	}
	fresh = make(map[string]int64, len(got))
	for id, v := range got {
		refs[id] = v
		fresh[id] = v
	}
	return refs, fresh, nil
}

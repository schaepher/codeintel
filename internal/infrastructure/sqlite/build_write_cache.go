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
//   - 容量上限 32768（避免大仓全量端点常驻内存）；超出淘汰策略见 P2
//     （当前为整表清空，P2 再对比分段轮换/时钟淘汰后替换）
//   - 不跨构建复用：由编排器在构建生命周期内 Begin/End（build_write_cache.go
//     只负责缓存本体与统计，生命周期由 Repo 持有 + 编排器驱动）

import (
	"database/sql"
	"sync"
)

// buildWriteCacheCapacity 正向缓存容量上限（项）。
const buildWriteCacheCapacity = 32768

// buildWriteCache 端点正向缓存 + 统计（可并发访问）。
type buildWriteCache struct {
	mu       sync.Mutex
	refs     map[string]int64
	capacity int

	inputs int64 // 查过的（去重后）端点输入数
	writes int64 // 发布的正向条目数
	hits   int64 // 命中数
	clears int64 // 容量溢出清表次数
}

// WriteCacheStats 构建期端点缓存统计（构建结束日志用）。
type WriteCacheStats struct {
	Inputs int64
	Writes int64
	Hits   int64
	Clears int64
	Size   int64
}

func newBuildWriteCache(capacity int) *buildWriteCache {
	if capacity <= 0 {
		capacity = buildWriteCacheCapacity
	}
	return &buildWriteCache{
		refs:     make(map[string]int64, capacity/16),
		capacity: capacity,
	}
}

// lookup 查缓存：ids 应已去重；返回命中映射与未命中列表。
func (c *buildWriteCache) lookup(ids []string) (map[string]int64, []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.inputs += int64(len(ids))
	hits := make(map[string]int64, len(ids)/4)
	miss := make([]string, 0, len(ids))
	for _, id := range ids {
		if v, ok := c.refs[id]; ok {
			hits[id] = v
			c.hits++
			continue
		}
		miss = append(miss, id)
	}
	return hits, miss
}

// publish 事务提交成功后写入正向结果；容量达上限时整表清空（递增 clears）。
func (c *buildWriteCache) publish(fresh map[string]int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, v := range fresh {
		if len(c.refs) >= c.capacity {
			c.refs = make(map[string]int64, c.capacity/16)
			c.clears++
		}
		c.refs[id] = v
		c.writes++
	}
}

func (c *buildWriteCache) snapshot() WriteCacheStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return WriteCacheStats{
		Inputs: c.inputs,
		Writes: c.writes,
		Hits:   c.hits,
		Clears: c.clears,
		Size:   int64(len(c.refs)),
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

// resolveNodeRefsCached 带构建期正向缓存的端点解析：返回全部 refs 与
// **本批新解析到的正向结果** fresh（供提交成功后发布）。缓存未启用时
// 退化为 resolveNodeRefs（fresh = refs，publish 为 no-op）。
func (r *Repo) resolveNodeRefsCached(tx *sql.Tx, ids []string) (refs map[string]int64, fresh map[string]int64, err error) {
	cache := r.currentBuildWriteCache()
	if cache == nil {
		refs, err = resolveNodeRefs(tx, ids)
		if err != nil {
			return nil, nil, err
		}
		return refs, refs, nil
	}
	uniq := uniqueIDs(ids)
	refs, miss := cache.lookup(uniq)
	if len(miss) == 0 {
		return refs, nil, nil
	}
	got, rerr := resolveNodeRefs(tx, miss)
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

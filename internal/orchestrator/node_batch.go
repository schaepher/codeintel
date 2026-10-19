package orchestrator

// 等价节点写入消除（Q?：重复解析/重复计算治理）。
//
// 构建期同一节点（canonical ID 相同、内容完全相同）会被多个发射路径
// 重复投递（如签名节点 + 值节点同 ID、同一全局对象从多处引用）。这些
// 写入在 SQLite 层是无谓的 UPSERT + json_patch 合并，重复率高时占用
// 可观（flush 是单写者串行热点）。
//
// 规则（严格保守）：
//   - 只在**同一批次内**比较；批次间不去重（跨批最多多写一次，语义不变）
//   - 只跳过与**同 ID 上一次贡献**内容完全相同的写入——保留 A→B→A
//     的多来源贡献（B 之后再写 A 会改变 json_patch 合并结果，不能按
//     "是否出现过" 去重）
//   - 只处理节点：边/摘要/来源不去重，count 与失败重试语义不变
//
// 内存代价：每批一张 ID → 上一次节点指针的表（BatchSize 上限约 2 万项），
// 只在批次生命周期内存在。

import (
	"reflect"

	"github.com/schaepher/codeintel/internal/domain"
)

// nodeDedup 同批次节点去重表。
type nodeDedup struct {
	last map[domain.CanonicalID]*domain.CodeEntity
}

// newNodeDedup 创建空去重表。
func newNodeDedup() *nodeDedup {
	return &nodeDedup{last: map[domain.CanonicalID]*domain.CodeEntity{}}
}

// reset 清空去重表但保留桶（todo9-7：批次对象复用；Go 的 clear 原地
// 清空，避免每批重新建表/增长复制）。
func (d *nodeDedup) reset() {
	if d == nil {
		return
	}
	if d.last == nil {
		d.last = map[domain.CanonicalID]*domain.CodeEntity{}
		return
	}
	clear(d.last)
}

// shouldAppend 判断节点是否写入：与同 ID 上一次贡献不同（或首次出现）
// 返回 true 并记录为新的上一次贡献；完全相同返回 false（跳过写入）。
// nil 节点直接写入（由下游处理，不在此吞掉）。
func (d *nodeDedup) shouldAppend(n *domain.CodeEntity) bool {
	if d == nil || n == nil {
		return true
	}
	if prev, ok := d.last[n.ID]; ok && nodesIdentical(prev, n) {
		return false
	}
	d.last[n.ID] = n
	return true
}

// nodesIdentical 节点内容全等（ID/Kind/Name/位置/属性）。属性用
// reflect.DeepEqual 逐值比较（map 无序）。
func nodesIdentical(a, b *domain.CodeEntity) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if a.Kind != b.Kind || a.Name != b.Name || a.FilePath != b.FilePath ||
		a.LineStart != b.LineStart || a.LineEnd != b.LineEnd {
		return false
	}
	return propertiesIdentical(a.Properties, b.Properties)
}

// propertiesIdentical 属性 map 全等（键集合相同且每个值 DeepEqual）。
func propertiesIdentical(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		bv, ok := b[k]
		if !ok || !reflect.DeepEqual(av, bv) {
			return false
		}
	}
	return true
}

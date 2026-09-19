package sqlite

// Q252d：邻接表进程内缓存（按视图惰性加载）。
//
// 动机：`GetPath`（query path / value-trace 的底层）每次调用都要**扫边集 +
// 建邻接 map**——ana 实测 4.5 万条数据流边 77ms/次，go2o ~110ms/次，BFS
// 本身只占很小一部分。跨函数链完整性门槛（scripts/chaincheck.sh）要跑上千
// 次查询，成本几乎全在这里。
//
// 语义：
//   - 视图（view）= 一组 kind 的邻接表（dataflow / calls）。**按视图惰性
//     加载**——单条 CLI 查询仍只扫它需要的那批边（不因缓存改造变慢），
//     serve / 门槛这类进程内多次查询才复用
//   - 键 = **build_id**：缓存的是**原始边**而非推断结果，边语义变化必然
//     伴随重新构建索引（build_id 变），故**不需要**额外版本常量（与
//     relationsAlgoVersion 不同——改本文件或边发射逻辑无需手动递增）
//   - 只读共享：视图建好后不再修改（Go map 并发读安全），锁只保护缓存槽；
//     构建在锁外、写锁内二次检查（并发首访可能重复建一次，用先到者）
//   - 单视图边数超阈值（maxCachedEdgeCount）不缓存：小内存机器不再叠大图
//   - 无 build_metadata（currentBuildID 为空）不缓存，每次现算

import (
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// maxCachedEdgeCount 单视图邻接表缓存上限（边数）——超过则不缓存。
// go2o 数据流视图约 6.5 万条，阈值留 30× 余量。
const maxCachedEdgeCount = 2_000_000

// 视图标签（按 kind 集合切；见 edgeViewKinds）。
const (
	edgeViewDataFlow = "dataflow"
	edgeViewCalls    = "calls"
)

// edgeViewKinds 视图 → 边类型集合（与 GetPath/trace 的边集一致）。
var edgeViewKinds = map[string]map[string]bool{
	edgeViewDataFlow: {
		"data_flows_to": true, "argument": true, "returns": true,
		"phi_operand": true, "summary_io": true,
	},
	edgeViewCalls: {"calls": true, "passes_to": true, "passes_result": true},
}

// edgeNeighbor 邻接表一项（目标 + 边类型）。
type edgeNeighbor struct {
	to   domain.CanonicalID
	kind string
}

// edgeAdjacency 某个视图的邻接表（构建后只读）。
type edgeAdjacency map[domain.CanonicalID][]edgeNeighbor

// edgeGraph 一个 build_id 下的视图集合（按需填充）。
type edgeGraph struct {
	key   string
	views map[string]edgeAdjacency
}

// shouldCacheEdgeGraph 边数阈值判定（大图不缓存）。
func shouldCacheEdgeGraph(edges int) bool { return edges <= maxCachedEdgeCount }

// loadEdgeAdjacency 只扫该视图需要的边并建邻接表。kind 串内部化
// （同类型只留一份字符串）。
func loadEdgeAdjacency(r *Repo, label string) (edgeAdjacency, int, error) {
	kinds := make([]string, 0, len(edgeViewKinds[label]))
	for k := range edgeViewKinds[label] {
		kinds = append(kinds, "'"+k+"'")
	}
	rows, err := r.Query(`SELECT source_id, target_id, kind FROM edges WHERE kind IN (` +
		strings.Join(kinds, ",") + `)`)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	adj := edgeAdjacency{}
	intern := map[string]string{}
	n := 0
	for rows.Next() {
		var src, dst, kind string
		if err := rows.Scan(&src, &dst, &kind); err != nil {
			return nil, 0, err
		}
		k, ok := intern[kind]
		if !ok {
			k, intern[kind] = kind, kind
		}
		s := domain.CanonicalID(src)
		adj[s] = append(adj[s], edgeNeighbor{to: domain.CanonicalID(dst), kind: k})
		n++
	}
	return adj, n, rows.Err()
}

// adjacencyView 取某视图邻接表（进程内缓存；无 build_metadata 不缓存）。
func (r *Repo) adjacencyView(label string) (edgeAdjacency, error) {
	key := r.currentBuildID()
	r.edgeGraphMu.RLock()
	if key != "" && r.edgeGraphCache != nil && r.edgeGraphCache.key == key {
		if v, ok := r.edgeGraphCache.views[label]; ok {
			r.edgeGraphMu.RUnlock()
			return v, nil
		}
	}
	r.edgeGraphMu.RUnlock()
	// 构建在锁外（并发首访可能重复建一次，用先到者）
	adj, n, err := loadEdgeAdjacency(r, label)
	if err != nil {
		return nil, err
	}
	if key == "" {
		return adj, nil
	}
	r.edgeGraphMu.Lock()
	defer r.edgeGraphMu.Unlock()
	if r.edgeGraphCache == nil || r.edgeGraphCache.key != key {
		r.edgeGraphCache = &edgeGraph{key: key, views: map[string]edgeAdjacency{}}
	}
	if v, ok := r.edgeGraphCache.views[label]; ok {
		return v, nil
	}
	if shouldCacheEdgeGraph(n) {
		r.edgeGraphCache.views[label] = adj
	}
	return adj, nil
}

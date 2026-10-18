package sqlite

// Q252d / R1：邻接表进程内缓存 + 超大图按需查询（按视图）。
//
// 动机：`GetPath`（query path / value-trace 的底层）每次调用都要**扫边集 +
// 建邻接 map**——ana 实测 4.5 万条数据流边 77ms/次，go2o ~110ms/次，BFS
// 本身只占很小一部分。跨函数链完整性门槛（scripts/chaincheck.sh）要跑上千
// 次查询，成本几乎全在这里。
//
// 语义（R1 修订）：
//   - 视图（view）= 一组 kind 的邻接表（dataflow / calls）。**按视图惰性
//     加载**——单条 CLI 查询仍只扫它需要的那批边（不因缓存改造变慢），
//     serve / 门槛这类进程内多次查询才复用
//   - **规模判断复用**：视图边数 `count(*)` 与"是否可缓存"结论按 build_id
//     缓存在 `edgeGraph.verdict`——同一构建版本内只算一次（原先每次都要靠
//     整表加载才知道超限，超大图因此反复全量加载）
//   - **超大图按需查询**：边数超阈值时**不整表加载、不缓存邻接表**，BFS
//     逐节点 SQL 查该节点的出边（同一次 BFS 内 memo）——保留深度限制与
//     有向语义，内存只与访问节点数相关
//   - 键 = **build_id**：缓存的是**原始边**而非推断结果，边语义变化必然
//     伴随重新构建索引（build_id 变），故**不需要**额外版本常量（与
//     relationsAlgoVersion 不同——改本文件或边发射逻辑无需手动递增）
//   - 只读共享：视图建好后不再修改（Go map 并发读安全），锁只保护缓存槽；
//     构建在锁外、写锁内二次检查（并发首访可能重复建一次，用先到者）
//   - 无 build_metadata（currentBuildID 为空）不缓存，每次现算

import (
	"sort"
	"strings"
)

// maxCachedEdgeCount 单视图邻接表缓存上限（边数）——超过则改**按需查询**
// （不整表加载）。go2o 数据流视图约 6.5 万条，阈值留 30× 余量。
// 变量而非常量：测试用小阈值覆盖以验证按需路径。
var maxCachedEdgeCount = 2_000_000

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

// edgeNeighbor 邻接表一项（目标 **id_int** + 边类型）。
//
// Q254c Stage 2：邻接表在**整数代理键空间**构建（edges.source_ref/target_ref）
// ——全表扫边不再携带 145B 的 canonical ID（真实库 7.46M 边），canonical ID
// 只在查询边界（起点/终点解析、结果回填）做少量点查。
type edgeNeighbor struct {
	to   int64
	kind string
}

// edgeAdjacency 某个视图的邻接表（构建后只读）。
type edgeAdjacency map[int64][]edgeNeighbor

// edgeVerdict 视图规模判断（边数 + 是否可缓存）。
type edgeVerdict struct {
	edges    int
	tooLarge bool
}

// edgeGraph 一个 build_id 下的视图集合（按需填充）。
type edgeGraph struct {
	key     string
	views   map[string]edgeAdjacency
	verdict map[string]edgeVerdict
}

// shouldCacheEdgeGraph 边数阈值判定（大图不缓存邻接表）。
func shouldCacheEdgeGraph(edges int) bool { return edges <= maxCachedEdgeCount }

// edgeViewKindList 视图的边类型集合（排序保证稳定顺序）。
func edgeViewKindList(label string) []string {
	kinds := make([]string, 0, len(edgeViewKinds[label]))
	for k := range edgeViewKinds[label] {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	return kinds
}

// edgeViewKindSQL 视图的 kind IN 子句（已加引号）。
func edgeViewKindSQL(label string) string {
	kinds := edgeViewKindList(label)
	q := make([]string, len(kinds))
	for i, k := range kinds {
		q[i] = "'" + k + "'"
	}
	return strings.Join(q, ",")
}

// loadEdgeAdjacency 只扫该视图需要的边并建邻接表。kind 串内部化
// （同类型只留一份字符串）。
func loadEdgeAdjacency(r *Repo, label string) (edgeAdjacency, int, error) {
	rows, err := r.Query(`SELECT source_ref, target_ref, kind FROM edges WHERE kind IN (` +
		edgeViewKindSQL(label) + `)`)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	adj := edgeAdjacency{}
	intern := map[string]string{}
	n := 0
	for rows.Next() {
		var src, dst int64
		var kind string
		if err := rows.Scan(&src, &dst, &kind); err != nil {
			return nil, 0, err
		}
		k, ok := intern[kind]
		if !ok {
			k, intern[kind] = kind, kind
		}
		adj[src] = append(adj[src], edgeNeighbor{to: dst, kind: k})
		n++
	}
	return adj, n, rows.Err()
}

// viewEdgeCount 视图边数与可缓存判定（同一 build 内只算一次——规模判断复用）。
func (r *Repo) viewEdgeCount(label string) (edgeVerdict, error) {
	key := r.currentBuildID()
	if key != "" {
		r.edgeGraphMu.RLock()
		if r.edgeGraphCache != nil && r.edgeGraphCache.key == key {
			if v, ok := r.edgeGraphCache.verdict[label]; ok {
				r.edgeGraphMu.RUnlock()
				return v, nil
			}
		}
		r.edgeGraphMu.RUnlock()
	}
	var n int
	if err := r.QueryRow(`SELECT count(*) FROM edges WHERE kind IN (` + edgeViewKindSQL(label) + `)`).Scan(&n); err != nil {
		return edgeVerdict{}, err
	}
	v := edgeVerdict{edges: n, tooLarge: !shouldCacheEdgeGraph(n)}
	if key == "" {
		return v, nil
	}
	r.edgeGraphMu.Lock()
	defer r.edgeGraphMu.Unlock()
	if r.edgeGraphCache == nil || r.edgeGraphCache.key != key {
		r.edgeGraphCache = &edgeGraph{key: key}
	}
	if r.edgeGraphCache.verdict == nil {
		r.edgeGraphCache.verdict = map[string]edgeVerdict{}
	}
	r.edgeGraphCache.verdict[label] = v
	return v, nil
}

// cachedAdjacency 整表加载视图邻接表（进程内缓存；仅用于未超阈值的视图）。
func (r *Repo) cachedAdjacency(label string) (edgeAdjacency, error) {
	key := r.currentBuildID()
	if key != "" {
		r.edgeGraphMu.RLock()
		if r.edgeGraphCache != nil && r.edgeGraphCache.key == key {
			if v, ok := r.edgeGraphCache.views[label]; ok {
				r.edgeGraphMu.RUnlock()
				return v, nil
			}
		}
		r.edgeGraphMu.RUnlock()
	}
	// 构建在锁外（并发首访可能重复建一次，用先到者）
	adj, _, err := loadEdgeAdjacency(r, label)
	if err != nil {
		return nil, err
	}
	if key == "" {
		return adj, nil
	}
	r.edgeGraphMu.Lock()
	defer r.edgeGraphMu.Unlock()
	if r.edgeGraphCache == nil || r.edgeGraphCache.key != key {
		r.edgeGraphCache = &edgeGraph{key: key}
	}
	if r.edgeGraphCache.views == nil {
		r.edgeGraphCache.views = map[string]edgeAdjacency{}
	}
	if v, ok := r.edgeGraphCache.views[label]; ok {
		return v, nil
	}
	r.edgeGraphCache.views[label] = adj
	return adj, nil
}

// adjacencyFunc 返回邻居查询函数（按视图）：
//   - 视图规模 ≤ 阈值：整表加载一次并缓存（进程内复用）
//   - 超过阈值：**按需 SQL 查询**——每次只查当前节点的出边，不再反复全量
//     加载超大图（内存与访问节点数相关，而非全边集）
//
// 规模判断（count + 可缓存结论）按 build_id 缓存，同一构建版本内只算一次。
func (r *Repo) adjacencyFunc(label string) (func(int64) ([]edgeNeighbor, error), error) {
	verdict, err := r.viewEdgeCount(label)
	if err != nil {
		return nil, err
	}
	if !verdict.tooLarge {
		adj, err := r.cachedAdjacency(label)
		if err != nil {
			return nil, err
		}
		return func(ref int64) ([]edgeNeighbor, error) { return adj[ref], nil }, nil
	}
	kindSQL := edgeViewKindSQL(label)
	return func(ref int64) ([]edgeNeighbor, error) {
		rows, err := r.Query(`SELECT target_ref, kind FROM edges WHERE source_ref = ? AND kind IN (`+kindSQL+`)`, ref)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []edgeNeighbor
		for rows.Next() {
			var e edgeNeighbor
			if err := rows.Scan(&e.to, &e.kind); err != nil {
				return nil, err
			}
			out = append(out, e)
		}
		return out, rows.Err()
	}, nil
}

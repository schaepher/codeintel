package sqlite

// P1-02：投影视图的加载器（SQL 与过滤条件与原实现逐字一致）。
//
// 每个视图对应一个消费者的"全扫类"查询——缓存只消除重复扫表，过滤/
// 建 map/BFS 等派生逻辑仍在消费者里执行，因此输出语义不变。
// （rg_bfs 不接入的理由见 edge_projection.go 文件头。）

import (
	"database/sql"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// 投影视图标签（同类数据不同过滤/字段 → 各自一个标签）。
const (
	projImplementsFacts = "implements-facts" // []*domain.Fact（排除 Unimplemented 桩）
	projCallFacts       = "call-facts"       // []*domain.Fact（kind=calls）
	projTargetRefs      = "target-refs:"     // map[ID]bool（按 kind 集区分，后缀 kinds）
	projVarInitFuncs    = "var-init-funcs"   // map[ID]bool（var 初始化引用的函数）
	projUnusedFuncs     = "unused-funcs"     // []unusedNodeRow（unused 报告口径）
	projIsolatedFuncs   = "isolated-funcs"   // []unusedNodeRow（孤立链口径）
)

// unusedNodeRow 函数/方法清单行（unused 两档报告的节点口径）。
type unusedNodeRow struct {
	id        domain.CanonicalID
	kind      string
	name      string
	file      string
	lineStart int
	lineEnd   int
}

// implementsFacts 全部 implements 边（排除 protoc 生成桩
// UnimplementedXxxServer——链上接口具体化不把桩当实现）。
func (r *Repo) implementsFacts() ([]*domain.Fact, error) {
	return cachedProjection(r, projImplementsFacts, func() ([]*domain.Fact, int, error) {
		rows, err := r.Query(`SELECT source_id, target_id FROM edges_v
			WHERE kind = 'implements' AND target_id NOT LIKE '%Unimplemented%'`)
		if err != nil {
			return nil, 0, err
		}
		defer rows.Close()
		var out []*domain.Fact
		for rows.Next() {
			f := &domain.Fact{Kind: domain.FactImplements}
			if err := rows.Scan(&f.SourceID, &f.TargetID); err != nil {
				return nil, 0, err
			}
			out = append(out, f)
		}
		return out, len(out), rows.Err()
	})
}

// callFacts 全量 calls 边（wiki 包间调用图 / unused 的调用关系数据源）。
func (r *Repo) callFacts() ([]*domain.Fact, error) {
	return cachedProjection(r, projCallFacts, func() ([]*domain.Fact, int, error) {
		rows, err := r.Query(`SELECT source_id, target_id FROM edges_v WHERE kind = 'calls'`)
		if err != nil {
			return nil, 0, err
		}
		defer rows.Close()
		var out []*domain.Fact
		for rows.Next() {
			f := &domain.Fact{Kind: domain.FactCalls}
			if err := rows.Scan(&f.SourceID, &f.TargetID); err != nil {
				return nil, 0, err
			}
			out = append(out, f)
		}
		return out, len(out), rows.Err()
	})
}

// edgeTargetKinds 指定 kind 边的全部 target_id 集合（unused 预聚合）。
// 返回的 map 为只读共享（调用方不得修改）。
func (r *Repo) edgeTargetKinds(kinds ...string) (map[domain.CanonicalID]bool, error) {
	ks := append([]string(nil), kinds...)
	sort.Strings(ks) // 标签稳定：同 kind 集不同顺序复用同一视图
	label := projTargetRefs + strings.Join(ks, ",")
	return cachedProjection(r, label, func() (map[domain.CanonicalID]bool, int, error) {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ks)), ",")
		args := make([]any, len(ks))
		for i, k := range ks {
			args[i] = k
		}
		rows, err := r.Query(`SELECT target_id FROM edges_v WHERE kind IN (`+placeholders+`)`, args...)
		if err != nil {
			return nil, 0, err
		}
		defer rows.Close()
		out := map[domain.CanonicalID]bool{}
		for rows.Next() {
			var t string
			if err := rows.Scan(&t); err != nil {
				return nil, 0, err
			}
			out[domain.CanonicalID(t)] = true
		}
		return out, len(out), rows.Err()
	})
}

// varInitFuncs 被全局变量初始化表达式引用的函数集合（data_flows_to →
// var.* 节点，source 的 func_id——Q108：包初始化调用的函数不算孤立）。
func (r *Repo) varInitFuncs() (map[domain.CanonicalID]bool, error) {
	return cachedProjection(r, projVarInitFuncs, func() (map[domain.CanonicalID]bool, int, error) {
		rows, err := r.Query(`SELECT DISTINCT json_extract(s.properties, '$.func_id')
	FROM edges_v e JOIN nodes s ON s.id = e.source_id
	WHERE e.kind = 'data_flows_to' AND e.target_id LIKE 'symbol:go:%:var.%'`)
		if err != nil {
			return nil, 0, err
		}
		defer rows.Close()
		out := map[domain.CanonicalID]bool{}
		for rows.Next() {
			var fid sql.NullString
			if err := rows.Scan(&fid); err != nil {
				return nil, 0, err
			}
			if fid.Valid && fid.String != "" {
				out[domain.CanonicalID(fid.String)] = true
			}
		}
		return out, len(out), rows.Err()
	})
}

// unusedFuncRows 函数/方法清单（GetUncalledFunctions 口径：排除 main/init
// 与测试文件，按文件+行号排序）。
func (r *Repo) unusedFuncRows() ([]unusedNodeRow, error) {
	return cachedProjection(r, projUnusedFuncs, func() ([]unusedNodeRow, int, error) {
		rows, err := r.Query(`SELECT n.id, n.kind, n.name, n.file_path, n.line_start, n.line_end
	FROM nodes n
	WHERE n.kind IN ('function','method')
	  AND n.name NOT IN ('main','init')
	  AND n.file_path IS NOT NULL
	  AND n.file_path NOT LIKE '%_test.go'
	ORDER BY n.file_path, n.line_start`)
		if err != nil {
			return nil, 0, err
		}
		defer rows.Close()
		var out []unusedNodeRow
		for rows.Next() {
			var row unusedNodeRow
			var id, kind, file string
			if err := rows.Scan(&id, &kind, &row.name, &file, &row.lineStart, &row.lineEnd); err != nil {
				return nil, 0, err
			}
			row.id, row.kind, row.file = domain.CanonicalID(id), kind, file
			out = append(out, row)
		}
		return out, len(out), rows.Err()
	})
}

// isolatedFuncRows 函数/方法清单（GetIsolatedChains 口径：不排除 main/init
// ——它们参与构图，由消费者在链头/链成员判定时过滤）。
func (r *Repo) isolatedFuncRows() ([]unusedNodeRow, error) {
	return cachedProjection(r, projIsolatedFuncs, func() ([]unusedNodeRow, int, error) {
		rows, err := r.Query(`SELECT n.id, n.kind, n.name, n.file_path, n.line_start, n.line_end
	FROM nodes n
	WHERE n.kind IN ('function','method')
	  AND n.file_path IS NOT NULL AND n.file_path NOT LIKE '%_test.go'`)
		if err != nil {
			return nil, 0, err
		}
		defer rows.Close()
		var out []unusedNodeRow
		for rows.Next() {
			var row unusedNodeRow
			var id, kind, file string
			if err := rows.Scan(&id, &kind, &row.name, &file, &row.lineStart, &row.lineEnd); err != nil {
				return nil, 0, err
			}
			row.id, row.kind, row.file = domain.CanonicalID(id), kind, file
			out = append(out, row)
		}
		return out, len(out), rows.Err()
	})
}

package sqlite

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// TraceBackward 反向追溯字段产生点（S2，field_trace.md §6.3）：
// 起点为入口函数内匹配 full_path 的 field_access 节点，沿
// data_flows_to / argument / returns / alias / phi_operand 反向遍历。
func (r *Repo) TraceBackward(field string, funcID domain.CanonicalID, maxDepth int) ([]*domain.TraceRow, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).TraceBackward")
	defer logger.Debug("exit (Repo).TraceBackward")
	return r.trace(field, funcID, maxDepth, false)
}

// TraceBackwardIndirect 反向追溯 + 跨函数间接写（Q172 --follow-indirect）：
// 起点函数对目标字段只有 function_field_summary.indirect_write（无本函数
// 真实 field_access）时，沿 summary_origins 递归解析调用链（outer → inner
// → ... → 真实写者），收集链上全部函数的 field_access 写节点作起点，
// 再执行反向 data_flows_to 遍历（赋值来源）。
func (r *Repo) TraceBackwardIndirect(field string, funcID domain.CanonicalID, maxDepth int) ([]*domain.TraceRow, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).TraceBackwardIndirect")
	defer logger.Debug("exit (Repo).TraceBackwardIndirect")
	if maxDepth <= 0 {
		maxDepth = 8
	}

	chain := map[domain.CanonicalID]bool{funcID: true}
	frontier := []domain.CanonicalID{funcID}
	for len(frontier) > 0 && len(chain) < maxDepth*4 {
		cur := frontier[0]
		frontier = frontier[1:]
		// P1-11：function_id → function_ref（按链上函数逐个解析；未索引的
		// 函数按"无来源"跳过——与旧等值查无行等价）。
		ref, ok, rerr := r.nodeRef(cur)
		if rerr != nil {
			return nil, rerr
		}
		if !ok {
			continue
		}
		rows, err := r.Query(`SELECT DISTINCT callee_id FROM summary_origins
			WHERE function_ref = ? AND access_kind = 'indirect_write' AND field_path = ?`,
			ref, field)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var cid sql.NullString
			if err := rows.Scan(&cid); err != nil {
				rows.Close()
				return nil, err
			}
			if !cid.Valid {
				continue
			}
			id := domain.CanonicalID(cid.String)
			if !chain[id] {
				chain[id] = true
				frontier = append(frontier, id)
			}
		}
		rows.Close()
	}

	ids := make([]string, 0, len(chain))
	for id := range chain {
		ids = append(ids, string(id))
	}
	if len(ids) == 0 {
		return nil, nil
	}
	// P1-11：不再用 idx_nodes_func_id（json_extract 表达式索引）——
	// "带 func_id 的节点 ID 都是 funcID#…"是构造不变量（SSA 发射端
	// 全部走 funcID+"#"+slot，写入时校验），故改走 nodes.id 唯一索引的
	// 前缀范围扫（每个函数一个 [funcID# , funcID$) 区间）。
	ranges := make([]string, 0, len(ids))
	for range ids {
		ranges = append(ranges, "(n.id >= ? AND n.id < ?)")
	}
	ph := strings.Join(ranges, " OR ")

	q := fmt.Sprintf(`WITH RECURSIVE back(id, depth, name, edge_kinds, line, kind, access, func_id, full_path) AS (
    SELECT n.id, 0, n.name, '', n.line_start, n.kind,
           json_extract(n.properties, '$.access_kind'),
           json_extract(n.properties, '$.func_id'),
           json_extract(n.properties, '$.full_path')
    FROM nodes n
    WHERE n.kind = 'field_access'
      AND json_extract(n.properties, '$.full_path') = ?
      AND json_extract(n.properties, '$.access_kind') = 'write'
      AND (%s)
    UNION
    SELECT e.source_id, d.depth + 1, n_prev.name,
           CASE WHEN d.edge_kinds = '' THEN e.kind ELSE d.edge_kinds || ',' || e.kind END,
           n_prev.line_start, n_prev.kind,
           json_extract(n_prev.properties, '$.access_kind'),
           json_extract(n_prev.properties, '$.func_id'),
           json_extract(n_prev.properties, '$.full_path')
    FROM edges_v e
    JOIN back d ON e.target_id = d.id
    JOIN nodes n_prev ON e.source_id = n_prev.id
    WHERE e.kind = 'data_flows_to' AND d.depth < ?
)
SELECT id, depth, name, edge_kinds, line, kind, access, func_id, full_path
FROM back ORDER BY depth, id`, ph)
	args := make([]any, 0, 1+2*len(ids)+1)
	args = append(args, field)
	for _, id := range ids {
		lo, hi := funcIDPrefixBounds(id)
		args = append(args, lo, hi)
	}
	args = append(args, maxDepth)
	rows, err := r.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.TraceRow
	for rows.Next() {
		var (
			row      domain.TraceRow
			id, kind string
			line     sql.NullInt64
			access   sql.NullString
			funcID   sql.NullString
			fullPath sql.NullString
		)
		if err := rows.Scan(&id, &row.Depth, &row.Name, &row.EdgeKinds, &line, &kind, &access, &funcID, &fullPath); err != nil {
			return nil, err
		}
		row.ID = domain.CanonicalID(id)
		row.Kind = domain.EntityKind(kind)
		row.EdgeKinds = sortEdgeKinds(row.EdgeKinds)
		if line.Valid {
			row.Line = int(line.Int64)
		}
		if access.Valid {
			row.Access = access.String
		}
		if funcID.Valid {
			row.FuncID = funcID.String
		}
		if fullPath.Valid {
			row.FullPath = fullPath.String
		}
		out = append(out, &row)
	}
	return out, rows.Err()
}

// TraceForward 正向追踪字段后续使用（S3，field_trace.md §6.4）：
// 起点同 S2，沿 data_flows_to / argument / returns / phi_operand / alias
// 正向遍历（跨函数经 argument/returns 边，不沿函数级 calls 边跳跃）；
// 遇到匹配 full_path 的 field_access 标记为使用点。
func (r *Repo) TraceForward(field string, funcID domain.CanonicalID, maxDepth int) ([]*domain.TraceRow, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).TraceForward")
	defer logger.Debug("exit (Repo).TraceForward")
	return r.trace(field, funcID, maxDepth, true)
}

// trace 递归 CTE 实现 S2/S3；UNION 去重 + 深度限制防环（Q49）。
func (r *Repo) trace(field string, funcID domain.CanonicalID, maxDepth int, forward bool) ([]*domain.TraceRow, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).trace")
	defer logger.Debug("exit (Repo).trace")
	if maxDepth <= 0 {
		maxDepth = 8
	}
	query := traceBackwardSQL
	if forward {
		query = traceForwardSQL
	}
	var (
		rows *sql.Rows
		err  error
	)
	lo, hi := funcIDPrefixBounds(string(funcID))
	if forward {
		targetType := strings.TrimSuffix(field, "."+lastSeg(field))
		rows, err = r.Query(query, field, lo, hi,
			lo, hi, targetType, "*"+targetType,
			targetType, "*"+targetType,
			lo, hi, targetType, "*"+targetType,
			field, field,
			"%."+lastSeg(field), "%"+pkgOf(field)+".%", maxDepth)
	} else {
		rows, err = r.Query(query, field, lo, hi, maxDepth)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.TraceRow
	for rows.Next() {
		var (
			row  domain.TraceRow
			id   string
			line sql.NullInt64
		)
		if forward {
			var usage int
			if err := rows.Scan(&id, &row.Depth, &row.Name, &row.EdgeKinds, &line, &usage); err != nil {
				return nil, err
			}
			row.IsUsage = usage == 1
		} else {
			if err := rows.Scan(&id, &row.Depth, &row.Name, &row.EdgeKinds, &line); err != nil {
				return nil, err
			}
		}
		row.ID = domain.CanonicalID(id)
		if line.Valid {
			row.Line = int(line.Int64)
		}
		out = append(out, &row)
	}
	return out, rows.Err()
}

// lastSeg 取类型限定字段路径的字段名（example.com/m.T.FinalFee → FinalFee）。
func lastSeg(fullPath string) string {
	if i := strings.LastIndex(fullPath, "."); i >= 0 {
		return fullPath[i+1:]
	}
	return fullPath
}

// pkgOf 取类型限定字段路径的模块包路径（example.com/m.T.FinalFee → example.com/m）。
func pkgOf(fullPath string) string {
	parts := strings.Split(fullPath, ".")
	if len(parts) <= 2 {
		return fullPath
	}
	return strings.Join(parts[:len(parts)-2], ".")
}

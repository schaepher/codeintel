package sqlite

import (
	"database/sql"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

func valueTraceFilter(anchorCtx, anchorInst string, reverse bool, tbl string, includeContainer bool) string {
	fp := `json_extract(` + tbl + `.properties, '$.full_path')`
	inst := `COALESCE(json_extract(` + tbl + `.properties, '$.instance_path'), json_extract(` + tbl + `.properties, '$.full_path'))`

	prefix := ""
	if includeContainer {
		prefix = `
OR (` + q(anchorCtx) + ` != '' AND (instr(` + q(anchorInst) + `, ` + inst + `) = 1 OR instr(` + inst + `, ` + q(anchorInst) + `) = 1))`
	}
	valueBridge := ""
	if reverse {
		valueBridge = `
OR json_extract(` + tbl + `.properties, '$.access_kind') = 'read'`
	} else {
		valueBridge = `
OR json_extract(` + tbl + `.properties, '$.access_kind') = 'write'`
	}
	return fp + ` = ` + q(anchorCtx) + prefix + valueBridge + `
OR json_extract(` + tbl + `.properties, '$.is_external') = 'true'
OR ` + q(anchorCtx) + ` = ''`
}
func q(s string) string { return "'" + s + "'" }

// sortEdgeKinds 排序边类型集合（Q155：GROUP_CONCAT(DISTINCT) 无序，
// server/CLI 按 LastIndex 取末段展示——排序保证输出稳定）。
func sortEdgeKinds(s string) string {
	if s == "" {
		return s
	}
	parts := strings.Split(s, ",")
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// GetValueTraceMulti 多锚点合并正向追踪（⑧ 跳板合并）：一次查询返回
// 全部锚点的下游使用链（dir=1），字段访问步按锚点字段 ctx 限定。
// trampoline 用它替代 N 次 GetValueTrace——读点多时累计查询成本
// 大幅下降（单次 CTE + UNION 去重）。
func (r *Repo) GetValueTraceMulti(anchors []domain.CanonicalID, ctxField string, maxDepth int) ([]*domain.TraceRow, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).GetValueTraceMulti")
	defer logger.Debug("exit (Repo).GetValueTraceMulti")
	if len(anchors) == 0 {
		return nil, nil
	}
	if maxDepth <= 0 {
		maxDepth = 4
	}
	ids := make([]string, 0, len(anchors))
	for _, a := range anchors {
		ids = append(ids, string(a))
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	fwdFilter := valueTraceFilter(ctxField, ctxField, false, "n_next", false)
	rows, err := r.Query(`WITH RECURSIVE
vt(id, dir, depth, kind) AS (
    SELECT n.id, 0, 0, n.kind FROM nodes n WHERE n.id IN (`+placeholders+`)
    UNION
    SELECT e.target_id, 1, d.depth + 1, n_next.kind FROM edges e INDEXED BY idx_edges_source
    JOIN vt d ON e.source_id = d.id
    JOIN nodes n_next ON e.target_id = n_next.id
    WHERE (d.dir = 1 OR d.depth = 0) AND d.depth < ? AND e.kind IN ('data_flows_to','argument','returns','phi_operand','summary_io')
      AND (n_next.kind != 'field_access' OR (`+fwdFilter+`))
)
SELECT dp.id, MIN(dp.depth), n.name,
       (SELECT COALESCE(GROUP_CONCAT(DISTINCT e2.kind), '') FROM edges e2
         WHERE ((dp.dir = 0 AND e2.target_id = dp.id) OR (dp.dir = 1 AND e2.source_id = dp.id))
           AND e2.kind IN ('data_flows_to','argument','returns','phi_operand','summary_io')),
       n.line_start, dp.dir, n.kind, n.file_path,
       json_extract(n.properties, '$.access_kind'), json_extract(n.properties, '$.func_id'),
       json_extract(n.properties, '$.full_path')
FROM vt dp JOIN nodes n ON n.id = dp.id
GROUP BY dp.id, dp.dir
ORDER BY MIN(dp.depth), dp.id`,
		append(anySlice(ids), maxDepth)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.TraceRow
	for rows.Next() {
		var (
			row      domain.TraceRow
			id       string
			line     sql.NullInt64
			kind     string
			access   sql.NullString
			funcID   sql.NullString
			fullPath sql.NullString
		)
		var filePath sql.NullString
		if err := rows.Scan(&id, &row.Depth, &row.Name, &row.EdgeKinds, &line, &row.Dir, &kind, &filePath, &access, &funcID, &fullPath); err != nil {
			return nil, err
		}
		row.ID = domain.CanonicalID(id)
		row.EdgeKinds = sortEdgeKinds(row.EdgeKinds)
		row.Kind = domain.EntityKind(kind)
		if filePath.Valid {
			row.FilePath = filePath.String
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
		if line.Valid {
			row.Line = int(line.Int64)
		}
		out = append(out, &row)
	}
	return out, rows.Err()
}

package sqlite

import (
	"database/sql"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// AllSummaries 返回全部函数字段摘要行（S4 导出用，field_trace.md §2），
// 按 field_path, access_kind 排序。
func (r *Repo) AllSummaries() ([]*domain.FunctionFieldSummary, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).AllSummaries")
	defer logger.Debug("exit (Repo).AllSummaries")
	// P1-11：物理列是 function_ref（整数代理键）——canonical ID 在边界
	// join nodes 还原（全表导出路径，join 代价可忽略）。
	rows, err := r.Query(`SELECT n.id, s.access_kind, s.field_path, s.instance_path, s.line_start, s.code_snippet
		FROM function_field_summary s JOIN nodes n ON n.id_int = s.function_ref
		ORDER BY s.field_path, s.access_kind`)
	if err != nil {
		return nil, err
	}
	return scanSummaries(rows)
}

// GetFunctionFields 按函数查询字段摘要，附带 Q161 origins（间接写
// 多来源：调用点 × 被调函数，origin/confidence 由 action 层 join
// dispatch_to 边填充）。
func (r *Repo) GetFunctionFields(funcID domain.CanonicalID) ([]*domain.FunctionFieldSummary, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).GetFunctionFields")
	defer logger.Debug("exit (Repo).GetFunctionFields")
	// P1-11：canonical 只在边界解析成 nodes.id_int（function_ref）；未索引
	// 的函数按"无摘要"返回（与旧实现无行等价）。
	ref, ok, err := r.nodeRef(funcID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	rows, err := r.Query(`SELECT n.id, s.access_kind, s.field_path, s.instance_path, s.line_start, s.code_snippet
		FROM function_field_summary s JOIN nodes n ON n.id_int = s.function_ref
		WHERE s.function_ref = ?
		ORDER BY s.access_kind, s.field_path`, ref)
	if err != nil {
		return nil, err
	}
	out, err := scanSummaries(rows)
	if err != nil {
		return nil, err
	}

	orows, err := r.Query(`SELECT function_ref, access_kind, field_path, call_line, callee_id
		FROM summary_origins WHERE function_ref = ? ORDER BY call_line`, ref)
	if err != nil {
		return nil, err
	}
	defer orows.Close()
	origins := map[string][]*domain.SummaryOrigin{}
	for orows.Next() {
		var (
			o     domain.SummaryOrigin
			fref  int64
			cline sql.NullInt64
			cid   sql.NullString
		)
		if err := orows.Scan(&fref, &o.AccessKind, &o.FieldPath, &cline, &cid); err != nil {
			return nil, err
		}
		o.FunctionID = funcID
		if cline.Valid {
			o.CallLine = int(cline.Int64)
		}
		if cid.Valid {
			o.CalleeID = domain.CanonicalID(cid.String)
		}
		key := string(o.AccessKind) + "|" + o.FieldPath
		origins[key] = append(origins[key], &o)
	}
	if len(origins) > 0 {
		for _, s := range out {
			key := string(s.AccessKind) + "|" + s.FieldPath
			if os, ok := origins[key]; ok {
				s.Origins = os
			}
		}
	}
	return out, nil
}

// scanSummaries 扫描摘要查询行（GetFunctionFields/AllSummaries 共用）。
func scanSummaries(rows *sql.Rows) ([]*domain.FunctionFieldSummary, error) {
	defer rows.Close()
	var out []*domain.FunctionFieldSummary
	for rows.Next() {
		var (
			s   domain.FunctionFieldSummary
			fid string
		)
		if err := rows.Scan(&fid, &s.AccessKind, &s.FieldPath, &s.InstancePath, &s.LineStart, &s.CodeSnippet); err != nil {
			return nil, err
		}
		s.FunctionID = domain.CanonicalID(fid)
		out = append(out, &s)
	}
	return out, rows.Err()
}

// GetFunctionFlows 返回函数内完整字段数据流（前端 /api/flows 用）：
// 起点 = 函数内全部 field_access 节点，双向遍历 data_flows_to / phi_operand
// （func_id 限定在函数内，到参数/返回边界即止）；Dir=0 为产生链（反向），
// Dir=1 为使用链（正向）。
func (r *Repo) GetFunctionFlows(funcID domain.CanonicalID, maxDepth int) ([]*domain.TraceRow, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).GetFunctionFlows")
	defer logger.Debug("exit (Repo).GetFunctionFlows")
	if maxDepth <= 0 {
		maxDepth = 8
	}
	// P1-11：函数内节点用 nodes.id 前缀范围扫（funcID+"#"+slot 不变量），
	// 替代已删除的 idx_nodes_func_id 表达式索引。
	lo, hi := funcIDPrefixBounds(string(funcID))
	rows, err := r.Query(`WITH RECURSIVE flows(id, depth, name, edge_kinds, line, dir, kind, access, func_id, full_path, ctx) AS (
    SELECT n.id, 0, n.name, '', n.line_start, 0, n.kind,
           json_extract(n.properties, '$.access_kind'),
           json_extract(n.properties, '$.func_id'),
           json_extract(n.properties, '$.full_path'),
           json_extract(n.properties, '$.full_path')
    FROM nodes n
    WHERE n.kind = 'field_access'
      AND n.id >= ? AND n.id < ?
    UNION
    -- 反向：流向当前节点（产生链）；字段访问步限定起始字段（⑥：
    -- 共享中间值节点不把其他字段的访问带入本字段链）
    SELECT e.source_id, d.depth + 1, n_prev.name,
           CASE WHEN d.edge_kinds = '' THEN e.kind
                ELSE d.edge_kinds || ',' || e.kind END, n_prev.line_start, 0,
           n_prev.kind, json_extract(n_prev.properties, '$.access_kind'),
           json_extract(n_prev.properties, '$.func_id'),
           json_extract(n_prev.properties, '$.full_path'),
           d.ctx
    FROM edges_v e
    JOIN flows d ON e.target_id = d.id
    JOIN nodes n_prev ON e.source_id = n_prev.id
    WHERE e.kind IN ('data_flows_to','phi_operand')
      AND (d.dir = 0 OR d.depth = 0) AND d.depth < ?
      AND n_prev.id >= ? AND n_prev.id < ?
      AND (n_prev.kind != 'field_access'
           OR json_extract(n_prev.properties, '$.full_path') = d.ctx)
    UNION
    -- 正向：从当前节点流出（使用链）
    SELECT e.target_id, d.depth + 1, n_next.name,
           CASE WHEN d.edge_kinds = '' THEN e.kind
                ELSE d.edge_kinds || ',' || e.kind END, n_next.line_start, 1,
           n_next.kind, json_extract(n_next.properties, '$.access_kind'),
           json_extract(n_next.properties, '$.func_id'),
           json_extract(n_next.properties, '$.full_path'),
           d.ctx
    FROM edges_v e
    JOIN flows d ON e.source_id = d.id
    JOIN nodes n_next ON e.target_id = n_next.id
    WHERE e.kind IN ('data_flows_to','phi_operand')
      AND (d.dir = 1 OR d.depth = 0) AND d.depth < ?
      AND n_next.id >= ? AND n_next.id < ?
      AND (n_next.kind != 'field_access'
           OR json_extract(n_next.properties, '$.full_path') = d.ctx)
)
SELECT id, depth, name, edge_kinds, line, dir, kind, access, func_id, full_path FROM flows ORDER BY dir, depth, id`,
		lo, hi, maxDepth, lo, hi, maxDepth, lo, hi)
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
			dir      int
			kind     string
			access   sql.NullString
			funcID   sql.NullString
			fullPath sql.NullString
		)
		if err := rows.Scan(&id, &row.Depth, &row.Name, &row.EdgeKinds, &line, &dir, &kind, &access, &funcID, &fullPath); err != nil {
			return nil, err
		}
		row.ID = domain.CanonicalID(id)
		row.Dir = dir
		row.Kind = domain.EntityKind(kind)
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

// GetAllCalls 全量 calls 边（Q251-A：wiki 模块页包间调用图聚合数据源）。
//
// P1-02：走 build_id 投影缓存（wiki 生成/helpers 对同一构建反复调用）。
// **返回切片为只读共享**——调用方不得修改元素或追加。
func (r *Repo) GetAllCalls() ([]*domain.Fact, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).GetAllCalls")
	defer logger.Debug("exit (Repo).GetAllCalls")
	return r.callFacts()
}

// GetFunctionsByNames 按名字批量取函数节点（kind=function；排除测试/外部
// 路径）。todo22 P1：跨包 DI 注册点定位——只解析引导函数所在文件，不遍历
// 全仓源码。
func (r *Repo) GetFunctionsByNames(names []string, limit int) ([]*domain.CodeEntity, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).GetFunctionsByNames", zap.Strings("names", names))
	defer logger.Debug("exit (Repo).GetFunctionsByNames")
	if len(names) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 500
	}
	ph := make([]string, 0, len(names))
	args := make([]any, 0, len(names)+1)
	for _, n := range names {
		ph = append(ph, "?")
		args = append(args, n)
	}
	args = append(args, limit)
	rows, err := r.Query(`SELECT id, name, file_path, line_start FROM nodes
		WHERE kind = 'function' AND name IN (`+strings.Join(ph, ",")+`)
		AND file_path NOT LIKE '%_test.go' AND file_path NOT LIKE '../%'
		ORDER BY id LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.CodeEntity
	for rows.Next() {
		n := &domain.CodeEntity{Kind: domain.KindFunction}
		if err := rows.Scan(&n.ID, &n.Name, &n.FilePath, &n.LineStart); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// GetFunctions 游离函数清单（R89：query helpers——kind=function 非
// 方法；排除 _test.go 与仓库外文件）。
func (r *Repo) GetFunctions() ([]*domain.CodeEntity, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).GetFunctions")
	defer logger.Debug("exit (Repo).GetFunctions")
	rows, err := r.Query(`SELECT id, name FROM nodes
		WHERE kind = 'function' AND file_path NOT LIKE '%_test.go' AND file_path NOT LIKE '../%'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.CodeEntity
	for rows.Next() {
		n := &domain.CodeEntity{Kind: domain.KindFunction}
		if err := rows.Scan(&n.ID, &n.Name); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

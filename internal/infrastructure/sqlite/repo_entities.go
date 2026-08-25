package sqlite

// R9 实体协作图原始数据：一次查询拿齐类型/方法归属/游离函数/调用边，
// 聚合（实体提取/过滤/边计数/诊断）在 action 层做——仓储只出事实。

import (
	"database/sql"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// GetEntityRaw 实体聚合数据源（R9）：类型节点 + 游离函数节点 +
// has_method 边 + 全量 calls 边——四组数据一次往返。
func (r *Repo) GetEntityRaw() (*domain.EntityRaw, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).GetEntityRaw")
	defer logger.Debug("exit (Repo).GetEntityRaw")
	out := &domain.EntityRaw{}
	if err := r.queryNodes(`SELECT id, name, kind, file_path, line_start FROM nodes
		WHERE kind IN ('struct', 'interface')`, &out.Types); err != nil {
		return nil, err
	}
	if err := r.queryNodes(`SELECT id, name, kind, file_path, line_start FROM nodes
		WHERE kind = 'function'`, &out.Funcs); err != nil {
		return nil, err
	}
	// R66：方法节点（接口方法统计——has_method 边不覆盖接口声明）
	if err := r.queryNodes(`SELECT id, name, kind, file_path, line_start FROM nodes
		WHERE kind = 'method'`, &out.Methods); err != nil {
		return nil, err
	}
	if err := r.queryEdges(`SELECT source_id, target_id, kind, tool_source, confidence FROM edges
		WHERE kind = 'has_method'`, &out.HasM); err != nil {
		return nil, err
	}
	if err := r.queryEdges(`SELECT source_id, target_id, kind, tool_source, confidence FROM edges
		WHERE kind = 'calls'`, &out.Calls); err != nil {
		return nil, err
	}
	return out, nil
}

// queryNodes 通用节点查询。
func (r *Repo) queryNodes(q string, out *[]*domain.CodeEntity) error {
	rows, err := r.Query(q)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var n domain.CodeEntity
		var file sql.NullString
		var line int
		if err := rows.Scan(&n.ID, &n.Name, &n.Kind, &file, &line); err != nil {
			return err
		}
		n.FilePath = file.String
		n.LineStart = line
		*out = append(*out, &n)
	}
	return rows.Err()
}

// queryEdges 通用边查询。
func (r *Repo) queryEdges(q string, out *[]*domain.Fact) error {
	rows, err := r.Query(q)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var f domain.Fact
		if err := rows.Scan(&f.SourceID, &f.TargetID, &f.Kind, &f.ToolSource, &f.Confidence); err != nil {
			return err
		}
		*out = append(*out, &f)
	}
	return rows.Err()
}

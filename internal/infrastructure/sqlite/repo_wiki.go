package sqlite

// #238 wiki：模块内被调用最多的符号（核心符号 Top N）。

import (
	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// TopCallersInModule 模块内被调用最多的符号（#238 wiki 核心符号）：
// 统计 calls 入边（e.target_id = n.id——被调用次数），按模块前缀过滤，
// 降序取 limit。prefix 形如 "symbol:go:example.com/m:"（含末冒号）。
func (r *Repo) TopCallersInModule(prefix string, limit int) ([]*domain.WikiSymbol, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).TopCallersInModule", zap.String("prefix", prefix), zap.Int("limit", limit))
	defer logger.Debug("exit (Repo).TopCallersInModule")
	if limit <= 0 {
		limit = 5
	}
	rows, err := r.Query(`SELECT n.name, n.kind, n.file_path, n.line_start, COUNT(e.target_id) AS c
		FROM nodes n LEFT JOIN edges e ON e.kind = 'calls' AND e.target_id = n.id
		WHERE (n.id LIKE ? OR n.id LIKE ?) AND n.kind IN ('function','method')
		GROUP BY n.id ORDER BY c DESC, n.id LIMIT ?`, prefix+":%", prefix+"/%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.WikiSymbol
	for rows.Next() {
		var s domain.WikiSymbol
		if err := rows.Scan(&s.Name, &s.Kind, &s.File, &s.Line, &s.Callers); err != nil {
			return nil, err
		}
		out = append(out, &s)
	}
	return out, rows.Err()
}

// TablesWrittenByModule 模块代码写入的表（#238 wiki 相关表）：external
// 表列虚拟节点的 func_id（所属函数）前缀匹配模块。
func (r *Repo) TablesWrittenByModule(prefix string) ([]string, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).TablesWrittenByModule", zap.String("prefix", prefix))
	defer logger.Debug("exit (Repo).TablesWrittenByModule")
	rows, err := r.Query(`SELECT DISTINCT name FROM nodes
		WHERE kind = 'field_access'
		  AND json_extract(properties, '$.is_external') = 'true'
		  AND json_extract(properties, '$.type_string') IN ('gorm', 'sql', 'xorm')
		  AND (json_extract(properties, '$.func_id') LIKE ? OR json_extract(properties, '$.func_id') LIKE ?)
		  AND name LIKE '%.%' AND name NOT LIKE '%.%.%'
		ORDER BY name`, prefix+":%", prefix+"/%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// TopLevelEntries 模块入口（#238 wiki：main + serves 服务，不含框架
// 回调 struct——那是 serve 图探索的顶层入口展示，wiki 不需要）。
func (r *Repo) TopLevelEntries() ([]*domain.CodeEntity, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).TopLevelEntries")
	defer logger.Debug("exit (Repo).TopLevelEntries")
	rows, err := r.Query(`
SELECT id, kind, name, file_path, line_start, line_end, properties FROM nodes
WHERE file_path IS NOT NULL
  AND file_path NOT LIKE '%_test.go'
  AND ((name = 'main' AND kind = 'function' AND id NOT LIKE '%.test:main')
   OR json_extract(properties, '$.serves_http') = 'true'
   OR json_extract(properties, '$.serves_grpc') = 'true')
ORDER BY kind, name LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanNodes(rows)
}

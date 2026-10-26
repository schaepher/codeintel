package sqlite

// 边行扫描（从 repo_graph.go 拆出——行数治理）：6 列热路径 scanFacts 与
// todo11-1 的带 edge_key 8 列 scanFactsKeyed。

import (
	"database/sql"
	"encoding/json"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

func scanFacts(rows *sql.Rows) ([]*domain.Fact, error) {
	logger := zap.L()
	logger.Debug("enter scanFacts")
	defer logger.Debug("exit scanFacts")
	var out []*domain.Fact
	for rows.Next() {
		f := &domain.Fact{}
		var meta string
		if err := rows.Scan(&f.SourceID, &f.TargetID, &f.Kind, &f.ToolSource, &f.Confidence, &meta); err != nil {
			return nil, err
		}
		if meta != "" {
			var m map[string]any
			if err := json.Unmarshal([]byte(meta), &m); err == nil {
				f.Metadata = m
			}
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// scanFactsKeyed 读带 edge_key 的边（列序：source,target,kind,key,tool,conf,meta,count）。
// todo11-1：只有需要关系身份的读取路径（grpc_call 等）用它——其余查询仍是
// 6 列 scanFacts（避免给全部热路径加列）。
func scanFactsKeyed(rows *sql.Rows) ([]*domain.Fact, error) {
	var out []*domain.Fact
	for rows.Next() {
		f := &domain.Fact{}
		var meta string
		if err := rows.Scan(&f.SourceID, &f.TargetID, &f.Kind, &f.Key, &f.ToolSource, &f.Confidence, &meta, &f.Count); err != nil {
			return nil, err
		}
		if meta != "" {
			var m map[string]any
			if err := json.Unmarshal([]byte(meta), &m); err == nil {
				f.Metadata = m
			}
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

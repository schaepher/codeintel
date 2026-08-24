package sqlite

// R19 表 schema 事实源：sqlite_master 的 CREATE TABLE——列类型/默认值
// 权威（不借助 AI 填类型）。

import (
	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// GetTableSchemas 读取 sqlite_master 全部建表语句（业务表 +
// 索引表）。返回 map[表名]DDL。
func (r *Repo) GetTableSchemas() (map[string]string, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).GetTableSchemas")
	defer logger.Debug("exit (Repo).GetTableSchemas")
	rows, err := r.Query(`SELECT name, sql FROM sqlite_master
		WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var name, sql string
		if err := rows.Scan(&name, &sql); err != nil {
			return nil, err
		}
		out[name] = sql
	}
	return out, rows.Err()
}

var _ = domain.EntityKindStruct // 保持 domain 引用（构建一致性）

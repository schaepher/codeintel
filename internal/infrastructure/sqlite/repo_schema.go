package sqlite

// R19 表 schema 事实源：sqlite_master 的 CREATE TABLE——列类型/默认值
// 权威（不借助 AI 填类型）。

import (
	"database/sql"
	"os"
	"path/filepath"

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

// RegistryTableSchemas 全局注册表（~/.codeintel/codeintel.db）的建表
// 语句（P1-05）。仓库外表的类型事实源：注册表 `repos` 表的 DDL 在
// 被分析仓库内不存在，R19/R21 的四层兜底（yaml/schema/gorm/Go 类型）
// 都拿不到列类型——wiki 里 10 列无类型。
//
// 语义：
//   - 库不存在（没注册过任何仓库）→ 返回空 map + nil，**不创建库文件**
//     （只读事实源，不能有副作用）
//   - 打开失败/查询失败 → 返回错误（调用方降级为「无此事实源」，
//     不让 wiki 生成失败）
//   - 只读仓库外事实，不写任何表
func RegistryTableSchemas(dir string) (map[string]string, error) {
	path := filepath.Join(dir, "codeintel.db")
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	// 与 OpenRegistry 同一 DSN（WAL/busy_timeout）——注册表可能正被
	// 其他进程写（serve/init），读 sqlite_master 只取已提交 schema。
	db, err := openRawSQLite(path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT name, sql FROM sqlite_master
		WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var name string
		var ddl sql.NullString
		if err := rows.Scan(&name, &ddl); err != nil {
			return nil, err
		}
		if ddl.Valid && ddl.String != "" {
			out[name] = ddl.String
		}
	}
	return out, rows.Err()
}

var _ = domain.EntityKindStruct // 保持 domain 引用（构建一致性）

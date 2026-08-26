package sqlite

// R95：grpc/http 调用链查询（query grpc-callers/http-callers/ext-chain）
// 窄接口实现——action 层 Reader 补充方法。调用链 BFS（calls+grpc_call
// 边内存遍历）+ 接口具体化（implements 边）+ ext_chain_cache 缓存表。

import (
	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// GetImplementsEdges 全部 implements 边（排除 protoc 生成桩
// UnimplementedXxxServer——链上接口具体化不把桩当实现）。
func (r *Repo) GetImplementsEdges() ([]*domain.Fact, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).GetImplementsEdges")
	defer logger.Debug("exit (Repo).GetImplementsEdges")
	rows, err := r.Query(`SELECT source_id, target_id FROM edges
		WHERE kind = 'implements' AND target_id NOT LIKE '%Unimplemented%'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.Fact
	for rows.Next() {
		f := &domain.Fact{Kind: domain.FactImplements}
		if err := rows.Scan(&f.SourceID, &f.TargetID); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// AllNodeIDs 全部节点 ID（接口具体化构造的方法 ID 存在性判定）。
func (r *Repo) AllNodeIDs() ([]domain.CanonicalID, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).AllNodeIDs")
	defer logger.Debug("exit (Repo).AllNodeIDs")
	rows, err := r.Query(`SELECT id FROM nodes`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.CanonicalID
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, domain.CanonicalID(id))
	}
	return out, rows.Err()
}

// EnsureExtChainCache 建 ext_chain_cache 表（幂等——懒建，schema 无）。
func (r *Repo) EnsureExtChainCache() error {
	logger := zap.L()
	logger.Debug("enter (Repo).EnsureExtChainCache")
	defer logger.Debug("exit (Repo).EnsureExtChainCache")
	_, err := r.Exec(`CREATE TABLE IF NOT EXISTS ext_chain_cache (
		symbol TEXT PRIMARY KEY,
		build TEXT NOT NULL,
		result TEXT NOT NULL
	)`)
	return err
}

// ExtChainCacheGet 读外部系统调用链缓存（symbol+build 命中）。
func (r *Repo) ExtChainCacheGet(symbol, build string) (string, bool) {
	logger := zap.L()
	logger.Debug("enter (Repo).ExtChainCacheGet")
	defer logger.Debug("exit (Repo).ExtChainCacheGet")
	row := r.QueryRow(`SELECT result FROM ext_chain_cache WHERE symbol = ? AND build = ?`, symbol, build)
	var result string
	if row.Scan(&result) != nil || result == "" {
		return "", false
	}
	return result, true
}

// ExtChainCacheSet 写外部系统调用链缓存（INSERT OR REPLACE）。
func (r *Repo) ExtChainCacheSet(symbol, build, result string) error {
	logger := zap.L()
	logger.Debug("enter (Repo).ExtChainCacheSet")
	defer logger.Debug("exit (Repo).ExtChainCacheSet")
	_, err := r.Exec(`INSERT OR REPLACE INTO ext_chain_cache (symbol, build, result) VALUES (?, ?, ?)`,
		symbol, build, result)
	return err
}

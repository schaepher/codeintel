package sqlite

// todo21：包清单查询（包职责地图数据源）——窄索引计划 + 可取消入口。
// 从 repo_graph.go 拆出（行数治理）。

import (
	"context"
	"fmt"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// GetPackages 全部包节点（R1 自举分析：包职责地图——包注释即职责）。
// 排除外部模块包（无 file_path/仓库外路径——与 GetRoots 同模式）。
// 兼容入口：无请求上下文（后台执行）；需要可取消时用 GetPackagesContext。
func (r *Repo) GetPackages() ([]*domain.CodeEntity, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).GetPackages")
	defer logger.Debug("exit (Repo).GetPackages")
	return r.GetPackagesContext(context.Background())
}

// packagesWhere 包清单谓词与排序（两种计划共用，避免口径分叉）。
const packagesWhere = `
WHERE kind = 'package'
  AND file_path IS NOT NULL
  AND file_path NOT LIKE '../%'
  AND file_path NOT LIKE 'tmp/%'
  AND file_path NOT LIKE 'integration/%'
  AND file_path NOT LIKE 'examples/%'
  AND file_path NOT LIKE 'e2e/%'
  AND file_path NOT LIKE 'skills/%'
ORDER BY name`

// packagesQueryIndexed todo21-14.2：原 SQL 的真实计划是
// 「SCAN nodes USING INDEX idx_nodes_name」——按名称索引遍历全库再回表
// 过滤 kind/file_path（大库上是全量回表）。INDEXED BY 窄索引
// idx_nodes_file_kind（file_path, kind，部分索引 WHERE file_path IS NOT NULL）
// 让 kind 与 file_path 谓词都在索引内求值，只有命中行才回表取
// name/properties；谓词与 ORDER BY 不变，代价是排序需要临时 b-tree
// （包数量远小于节点总数）。不新增索引/schema。
const packagesQueryIndexed = `
SELECT id, kind, name, file_path, line_start, line_end, properties
FROM nodes INDEXED BY idx_nodes_file_kind` + packagesWhere

// packagesQueryPlain 无索引提示的等价语句（索引缺失期间的兜底——全量
// 构建的 BeginBulkLoad 会 DROP 该索引，构建完成/下次 Open 重建）。
const packagesQueryPlain = `
SELECT id, kind, name, file_path, line_start, line_end, properties FROM nodes` + packagesWhere

// GetPackagesContext 同 GetPackages，查询可被 ctx 取消（连接等待与 SQL 执行）。
//
// 索引依赖：schema DDL、批量构建恢复路径（bulk_load.go 的 EndBulkLoad）与
// Open 的幂等补建都提供 idx_nodes_file_kind；但全量构建**进行中**该索引
// 被临时 DROP，此时 INDEXED BY 会报 no query solution——回退到等价语句，
// 保持"构建期间的只读查询不因索引维护而失败"（旧实现无此依赖）。
func (r *Repo) GetPackagesContext(ctx context.Context) ([]*domain.CodeEntity, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).GetPackagesContext")
	defer logger.Debug("exit (Repo).GetPackagesContext")
	rows, err := r.QueryContext(ctx, packagesQueryIndexed)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("get packages: %w", err)
		}
		logger.Debug("packages indexed query failed, falling back", zap.Error(err))
		if rows, err = r.QueryContext(ctx, packagesQueryPlain); err != nil {
			return nil, fmt.Errorf("get packages: %w", err)
		}
	}
	defer rows.Close()
	pkgs, err := scanNodes(rows)
	if err != nil {
		return nil, err
	}
	// 去重（同包多文件？包节点每包一个）按 id
	seen := map[domain.CanonicalID]bool{}
	var out []*domain.CodeEntity
	for _, p := range pkgs {
		if seen[p.ID] {
			continue
		}
		seen[p.ID] = true
		out = append(out, p)
	}
	return out, nil
}

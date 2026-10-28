package sqlite

import (
	"context"
	"fmt"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// GetImpact 计算变更影响范围：从 id 出发沿任意方向遍历，深度 ≤ depth（TD.md 决策 10）。
func (r *Repo) GetImpact(id domain.CanonicalID, depth int) ([]*domain.CodeEntity, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).GetImpact")
	defer logger.Debug("exit (Repo).GetImpact")
	q := `
WITH RECURSIVE reach(id, d) AS (
    SELECT target_id, 1 FROM edges_v WHERE source_id = ?
    UNION
    SELECT source_id, 1 FROM edges_v WHERE target_id = ?
    UNION
    SELECT e.target_id, r.d + 1 FROM edges_v e JOIN reach r ON e.source_id = r.id WHERE r.d < ?
    UNION
    SELECT e.source_id, r.d + 1 FROM edges_v e JOIN reach r ON e.target_id = r.id WHERE r.d < ?
)
SELECT id FROM reach LIMIT 2000`

	rows, err := r.Query(q, string(id), string(id), depth, depth)
	if err != nil {
		return nil, fmt.Errorf("impact of %s: %w", id, err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var idStr string
		if err := rows.Scan(&idStr); err != nil {
			return nil, err
		}
		ids = append(ids, idStr)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, v := range ids {
		args[i] = v
	}
	nodeRows, err := r.Query(
		"SELECT id, kind, name, file_path, line_start, line_end, properties FROM nodes WHERE id IN ("+placeholders+")",
		args...)
	if err != nil {
		return nil, err
	}
	defer nodeRows.Close()
	return scanNodes(nodeRows)
}

// GetRoots 返回顶层入口节点（前端初始视图）：
//   - main 入口函数（排除测试包生成的 main，其 id 形如 <pkg>.test:main）
//   - HTTP 服务入口（serves_http 标记）
//   - gRPC 服务入口（serves_grpc 标记）
//   - 框架回调 struct：方法未被当前 module 其他文件调用（由框架调用）
//
// 约束：入口必须落在当前 module 内的文件（file_path 非空、非 _test.go、
// 非仓库外路径）。
func (r *Repo) GetRoots() ([]*domain.CodeEntity, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).GetRoots")
	defer logger.Debug("exit (Repo).GetRoots")
	rows, err := r.Query(`
SELECT id, kind, name, file_path, line_start, line_end, properties FROM nodes
WHERE file_path IS NOT NULL
  AND file_path NOT LIKE '%_test.go'
  AND file_path NOT LIKE '../%'
  AND ((name = 'main' AND kind = 'function' AND id NOT LIKE '%.test:main')
   OR json_extract(properties, '$.serves_http') = 'true'
   OR json_extract(properties, '$.serves_grpc') = 'true')
ORDER BY kind, name LIMIT 200`)
	if err != nil {
		return nil, fmt.Errorf("get roots: %w", err)
	}
	roots, err := scanNodes(rows)
	if err != nil {
		return nil, err
	}

	framework, err := r.GetFrameworkStructs()
	if err != nil {
		return nil, err
	}
	seen := map[domain.CanonicalID]bool{}
	for _, n := range roots {
		seen[n.ID] = true
	}
	for _, n := range framework {
		if !seen[n.ID] {
			roots = append(roots, n)
			seen[n.ID] = true
		}
	}
	return roots, nil
}

// InterfaceMethodImpl 接口方法/类型 → 实现（R75：调用链接口具体化——
// implements 边 + 方法名匹配；排除 Unimplemented 桩；多实现取首个有
// 该方法者）。两种输入形态：
//  1. 接口方法：symbol:go:<pkg>:(Iface).Method → 实现类型同名方法
//  2. 接口类型：symbol:go:<pkg>:Iface（go2o 实测 calls 边 target 是
//     接口类型节点——调用点未记录方法名）→ 实现类型节点
func (r *Repo) InterfaceMethodImpl(methodID string) (string, bool) {
	i := strings.Index(methodID, ":(")
	if i < 0 {
		// 形态 2：接口类型节点 → 实现类型节点
		implIDs := r.orderImplsByGrpc(r.interfaceImpls(methodID))
		if len(implIDs) == 0 {
			return "", false
		}
		return implIDs[0], true
	}
	pkg := methodID[:i]
	rest := methodID[i+2:]
	j := strings.Index(rest, ").")
	if j < 0 {
		return "", false
	}
	ifaceID := pkg + ":" + rest[:j]
	methodName := rest[j+2:]
	implIDs := r.orderImplsByGrpc(r.interfaceImpls(ifaceID))
	for _, implID := range implIDs {
		// 3. 实现类型同名方法：symbol:go:<pkg>:(Type).Method
		mi := strings.LastIndex(implID, ":")
		if mi < 0 {
			continue
		}
		implMethod := implID[:mi+1] + "(" + implID[mi+1:] + ")." + methodName
		var cnt int
		rows, err := r.Query(`SELECT COUNT(*) FROM nodes WHERE id = ?`, implMethod)
		if err == nil && rows.Next() {
			_ = rows.Scan(&cnt)
		}
		rows.Close()
		if cnt > 0 {
			return implMethod, true
		}
	}
	return "", false
}

// interfaceImpls 接口的 implements 实现类型列表（校验接口 kind；排除
// Unimplemented 桩）。
func (r *Repo) interfaceImpls(ifaceID string) []string {
	var kind string
	rows, err := r.Query(`SELECT kind FROM nodes WHERE id = ?`, ifaceID)
	if err == nil && rows.Next() {
		_ = rows.Scan(&kind)
	}
	rows.Close()
	if kind != "interface" {
		return nil
	}
	implIDs, err := r.InterfaceImplsExact(ifaceID)
	if err != nil {
		return nil
	}
	return implIDs
}

// InterfaceImplsExact 接口的实现类型列表（todo22 §7.1：**精确**查询——
// 用 nodes.id → id_int 的整数邻接 + UNIQUE(source_ref,target_ref,kind) 前缀，
// 不 JOIN edges_v 视图、不加载全量 implements 边）。
func (r *Repo) InterfaceImplsExact(ifaceID string) ([]string, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).InterfaceImplsExact")
	defer logger.Debug("exit (Repo).InterfaceImplsExact")
	// T16：与 GetInterfaceImplements 同一查询（稳定排序 + 只取 implements），
	// 无 ctx 版本保留为后台兼容入口。
	ids, err := r.GetInterfaceImplements(context.Background(), ifaceID)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, string(id))
	}
	return out, nil
}

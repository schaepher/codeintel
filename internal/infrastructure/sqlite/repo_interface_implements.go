package sqlite

// T16：单接口 implements 邻接查询（可取消、稳定排序、只取 implements）。
//
// 为什么单独一条：GetImplementsEdges 会加载全量 implements 边，
// Expand/双向探索会混入 calls/imports 等邻居——都不是"接口 → 实现集合"
// 的语义。这里从准确接口 ID → 整数邻接（id_int/source_ref）→ 所有实现
// 目标节点，未知接口返回空集。

import (
	"context"
	"github.com/schaepher/codeintel/internal/domain"
)

// interfaceImplementsSQL 与 InterfaceImplsExact 共用同一形态（整数邻接 +
// 排除 protoc 生成桩），但按 id 稳定排序以便输出可复现。
const interfaceImplementsSQL = `SELECT t.id FROM nodes s
	JOIN edges e ON e.source_ref = s.id_int AND e.kind = 'implements'
	JOIN nodes t ON t.id_int = e.target_ref
	WHERE s.id = ? AND t.id NOT LIKE '%Unimplemented%'
	ORDER BY t.id`

// GetInterfaceImplements 接口 implementation 集合（ctx 取消 → ctx.Err()）。
// 未知接口返回空切片且无错误；同一查询只取 implements 边。
func (r *Repo) GetInterfaceImplements(ctx context.Context, ifaceID string) ([]domain.CanonicalID, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := r.QueryContext(ctx, interfaceImplementsSQL, ifaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.CanonicalID{}
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, domain.CanonicalID(id))
	}
	if err := rows.Err(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}
	return out, nil
}

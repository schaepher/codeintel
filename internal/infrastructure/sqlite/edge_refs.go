package sqlite

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// Q254c Stage 2：canonical ID → nodes.id_int（整数代理键）批量解析。
//
// 为什么按批查询而不是维护全量 map：真实库 1.2M 节点 ×（canonical ID 平均
// 145B + map 开销）常驻内存不划算；按批只解析本批边用到的端点（几百个），
// 走 nodes.id 的唯一索引，代价可忽略。
//
// 同事务内可见性：节点先于边写入同一 tx，故本批新插入的节点也能查到。
func resolveNodeRefs(tx *sql.Tx, ids []string) (map[string]int64, error) {
	refs := make(map[string]int64, len(ids))
	uniq := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		uniq = append(uniq, id)
	}
	const chunk = 800 // 绑定变量上限（SQLITE_MAX_VARIABLE_NUMBER 默认 32766）
	for start := 0; start < len(uniq); start += chunk {
		end := start + chunk
		if end > len(uniq) {
			end = len(uniq)
		}
		part := uniq[start:end]
		args := make([]any, 0, len(part))
		for _, id := range part {
			args = append(args, id)
		}
		rows, err := tx.Query("SELECT id, id_int FROM nodes WHERE id IN ("+
			strings.TrimSuffix(strings.Repeat("?,", len(part)), ",")+")", args...)
		if err != nil {
			return nil, fmt.Errorf("resolve node refs: %w", err)
		}
		for rows.Next() {
			var id string
			var ref int64
			if err := rows.Scan(&id, &ref); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan node ref: %w", err)
			}
			refs[id] = ref
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return refs, nil
}

// edgeEndpointIDs 取边的端点 canonical ID（去重前，顺序无关）。
func edgeEndpointIDs(edges []*domain.Fact) []string {
	out := make([]string, 0, len(edges)*2)
	for _, e := range edges {
		out = append(out, string(e.SourceID), string(e.TargetID))
	}
	return out
}

// nodeRef 单个 canonical ID → id_int（不存在返回 ok=false）。
func (r *Repo) nodeRef(id domain.CanonicalID) (int64, bool, error) {
	var ref int64
	err := r.QueryRow("SELECT id_int FROM nodes WHERE id = ?", string(id)).Scan(&ref)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return ref, true, nil
}

// canonicalOfRefs 批量 id_int → canonical ID（结果回填用，少量）。
func (r *Repo) canonicalOfRefs(refs []int64) (map[int64]domain.CanonicalID, error) {
	out := make(map[int64]domain.CanonicalID, len(refs))
	const chunk = 800
	for start := 0; start < len(refs); start += chunk {
		end := start + chunk
		if end > len(refs) {
			end = len(refs)
		}
		part := refs[start:end]
		args := make([]any, 0, len(part))
		for _, ref := range part {
			args = append(args, ref)
		}
		rows, err := r.Query("SELECT id_int, id FROM nodes WHERE id_int IN ("+
			strings.TrimSuffix(strings.Repeat("?,", len(part)), ",")+")", args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var ref int64
			var id string
			if err := rows.Scan(&ref, &id); err != nil {
				rows.Close()
				return nil, err
			}
			out[ref] = domain.CanonicalID(id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

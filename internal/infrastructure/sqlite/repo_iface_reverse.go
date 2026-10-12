package sqlite

// 接口反向具体化（从 repo_graph.go 拆出——行数治理）：实现方法 → 其
// 实现的接口方法，供查询端把"经接口方法发出的调用"映射回实现方法。

import "strings"

// ConcreteMethodIfaces 实现方法 → 其实现的接口方法列表（接口反向具体
// 化：查询实现方法的调用者时，需要把"调用接口方法"的调用者也算进来）。
// 只做"接口方法 → 实现方法"的精确映射：从方法 ID 取出 receiver 类型，
// 经 implements 边找到接口类型，再构造 <接口>.(方法名) 并校验节点存在。
// 索引只记录了接口类型而未记录方法名的形态不猜测（避免把调用错误
// 归因到某个实现方法）。
func (r *Repo) ConcreteMethodIfaces(methodID string) []string {
	i := strings.Index(methodID, ":(")
	if i < 0 {
		return nil
	}
	pkg := methodID[:i]    // symbol:go:<pkg>
	rest := methodID[i+2:] // Type).Method
	j := strings.Index(rest, ").")
	if j < 0 {
		return nil
	}
	recvID := pkg + ":" + rest[:j]
	methodName := rest[j+2:]
	ifaceRows, err := r.Query(`SELECT source_id FROM edges_v
		WHERE target_id = ? AND kind = 'implements'`, recvID)
	if err != nil {
		return nil
	}
	var ifaceIDs []string
	for ifaceRows.Next() {
		var id string
		if ifaceRows.Scan(&id) == nil {
			ifaceIDs = append(ifaceIDs, id)
		}
	}
	ifaceRows.Close()
	var out []string
	for _, ifaceID := range ifaceIDs {
		mi := strings.LastIndex(ifaceID, ":")
		if mi < 0 {
			continue
		}
		m := ifaceID[:mi+1] + "(" + ifaceID[mi+1:] + ")." + methodName
		var cnt int
		rows, err := r.Query(`SELECT COUNT(*) FROM nodes WHERE id = ?`, m)
		if err == nil && rows.Next() {
			_ = rows.Scan(&cnt)
		}
		rows.Close()
		if cnt > 0 {
			out = append(out, m)
		}
	}
	return out
}

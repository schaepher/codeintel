package cli

import (
	"strings"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// chainSymbols 递归收集调用链符号（BFS——一次加载全部 calls/grpc_call
// 边做内存遍历，去重，上限 300 防爆炸）。R84：接口不停止解析——链上
// 遇到接口方法/类型（grpc 服务入口接口是动态入口，无直接 caller）经
// implements 边具体化到实现（接口类型 → 首个实现；接口方法 → 首个有
// 该方法者——与 InterfaceMethodImpl 语义一致，控制膨胀）。
func chainSymbols(acts *action.Actions, repo *sqlite.Repo, root string) map[string]bool {
	adj := map[string][]string{}
	rows, err := repo.Query(`SELECT source_id, target_id FROM edges WHERE kind IN ('calls', 'grpc_call')`)
	if err != nil {
		return map[string]bool{root: true}
	}
	for rows.Next() {
		var src, tgt string
		if rows.Scan(&src, &tgt) == nil {
			adj[src] = append(adj[src], tgt)
		}
	}
	rows.Close()

	ifaces := map[string][]string{}
	if irows, err := repo.Query(`SELECT source_id, target_id FROM edges
		WHERE kind = 'implements' AND target_id NOT LIKE '%Unimplemented%'`); err == nil {
		for irows.Next() {
			var src, tgt string
			if irows.Scan(&src, &tgt) == nil {
				ifaces[src] = append(ifaces[src], tgt)
			}
		}
		irows.Close()
	}
	nodeSet := map[string]bool{}
	if nrows, err := repo.Query(`SELECT id FROM nodes`); err == nil {
		for nrows.Next() {
			var id string
			if nrows.Scan(&id) == nil {
				nodeSet[id] = true
			}
		}
		nrows.Close()
	}

	ifaceConcrete := func(t string) []string {
		var out []string
		if i := strings.Index(t, ":("); i >= 0 {
			pkg := t[:i]
			rest := t[i+2:]
			if j := strings.Index(rest, ")."); j >= 0 {
				iface := pkg + ":" + rest[:j]
				method := rest[j+2:]
				for _, impl := range ifaces[iface] {
					mi := strings.LastIndex(impl, ":")
					if mi < 0 {
						continue
					}
					m := impl[:mi+1] + "(" + impl[mi+1:] + ")." + method
					if nodeSet[m] {
						out = append(out, m)
						break
					}
				}
			}
		} else if impls, ok := ifaces[t]; ok && len(impls) > 0 {
			out = append(out, impls[0])
		}
		return out
	}
	seen := map[string]bool{}
	queue := []string{root}
	for len(queue) > 0 && len(seen) < 300 {
		id := queue[0]
		queue = queue[1:]
		if seen[id] {
			continue
		}
		seen[id] = true
		next := append([]string{}, adj[id]...)
		next = append(next, ifaceConcrete(id)...)
		for _, t := range next {
			if !seen[t] {
				queue = append(queue, t)
			}
		}
	}
	return seen
}

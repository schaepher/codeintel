package cli

// R83 `query grpc-callers <符号>` / `query http-callers <符号>`——查询
// 一个调用链最终调用了哪些 grpc 服务 / http 出站接口。递归展开
// CalleesConcrete（接口具体化），收集链上每个符号的出站 grpc/http
// 调用边。

import (
	"fmt"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// chainCallOut 调用链里的一个 grpc/http 出站调用。
type chainCallOut struct {
	Service string `json:"service"`           // grpc 服务名 / http host
	Method  string `json:"method,omitempty"`  // grpc 方法名（调用点未记录时为空）
	Path    string `json:"path,omitempty"`    // http path
	CalledBy string `json:"called_by"`        // 调用点符号短名
	Line    int    `json:"line,omitempty"`    // 调用行号
}

// chainIOOut 输出契约。
type chainIOOut struct {
	Symbol string         `json:"symbol"`
	Grpc   []chainCallOut `json:"grpc,omitempty"`
	HTTP   []chainCallOut `json:"http,omitempty"`
}

// chainSymbols 递归收集调用链符号（BFS——一次加载全部 calls/grpc_call
// 边做内存遍历，去重，上限 300 防爆炸）。
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
	seen := map[string]bool{}
	queue := []string{root}
	for len(queue) > 0 && len(seen) < 300 {
		id := queue[0]
		queue = queue[1:]
		if seen[id] {
			continue
		}
		seen[id] = true
		for _, t := range adj[id] {
			if !seen[t] {
				queue = append(queue, t)
			}
		}
	}
	return seen
}

// grpcSvcNames 全部 grpc 服务名集合（内存——grpc 客户端判定）。
func grpcSvcNames(repo *sqlite.Repo) map[string]bool {
	out := map[string]bool{}
	rows, err := repo.Query(`SELECT json_extract(properties, '$.service_name') FROM nodes WHERE kind = 'grpc_service'`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if rows.Scan(&name) == nil && name != "" {
			out[name] = true
		}
	}
	return out
}

// grpcClientName 被调符号是否为 grpc 客户端类型——短名 `<Svc>Client`
// 且 Svc 是已知 grpc 服务名（内存判定）。返回服务名。
func grpcClientName(symID string, svcs map[string]bool) string {
	rest := strings.TrimPrefix(symID, "symbol:go:")
	name := rest
	if i := strings.LastIndex(rest, ":"); i >= 0 {
		name = rest[i+1:]
	}
	if !strings.HasSuffix(name, "Client") {
		return ""
	}
	svc := strings.TrimSuffix(name, "Client")
	if svc != "" && svcs[svc] {
		return svc
	}
	return ""
}

// chainGrpcHTTP 收集调用链的 grpc/http 出站调用（一次全量扫描——
// 链符号 BFS + 出站边过滤，避免逐符号查询）。
func chainGrpcHTTP(acts *action.Actions, repo *sqlite.Repo, symbol string) chainIOOut {
	out := chainIOOut{Symbol: symbol, Grpc: []chainCallOut{}, HTTP: []chainCallOut{}}
	n, err := acts.ResolveSymbol(symbol)
	if err != nil {
		return out
	}
	svcs := grpcSvcNames(repo)
	seen := chainSymbols(acts, repo, string(n.ID))
	// 一次加载链内符号的全部出站边
	rows, err := repo.Query(`SELECT source_id, target_id, kind, COALESCE(json_extract(metadata, '$.host'), ''),
		COALESCE(json_extract(metadata, '$.path'), ''), COALESCE(json_extract(metadata, '$.line_num'), 0)
		FROM edges WHERE kind IN ('grpc_call', 'http_call')`)
	if err == nil {
		for rows.Next() {
			var src, target, kind, host, path string
			var line int
			if rows.Scan(&src, &target, &kind, &host, &path, &line) != nil || !seen[src] {
				continue
			}
			by := shortSymbolNameID(src)
			if kind == "http_call" {
				out.HTTP = append(out.HTTP, chainCallOut{Service: host, Path: path, CalledBy: by, Line: line})
			} else {
				svc := strings.TrimPrefix(target, "symbol:go:")
				if i := strings.LastIndex(svc, ":"); i >= 0 {
					svc = strings.TrimPrefix(svc[i+1:], "svc.")
				}
				out.Grpc = append(out.Grpc, chainCallOut{Service: svc, CalledBy: by, Line: line})
			}
		}
		rows.Close()
	}
	// calls 边里的 grpc 客户端类型调用（构造/方法）
	rows2, err := repo.Query(`SELECT source_id, target_id, COALESCE(json_extract(metadata, '$.line_num'), 0) FROM edges WHERE kind = 'calls'`)
	if err == nil {
		for rows2.Next() {
			var src, target string
			var line int
			if rows2.Scan(&src, &target, &line) != nil || !seen[src] {
				continue
			}
			if svc := grpcClientName(target, svcs); svc != "" {
				out.Grpc = append(out.Grpc, chainCallOut{Service: svc, CalledBy: shortSymbolNameID(src), Line: line})
			}
		}
		rows2.Close()
	}
	sortOut := func(xs []chainCallOut) {
		sort.Slice(xs, func(i, j int) bool {
			if xs[i].Service != xs[j].Service {
				return xs[i].Service < xs[j].Service
			}
			return xs[i].CalledBy < xs[j].CalledBy
		})
	}
	sortOut(out.Grpc)
	sortOut(out.HTTP)
	return out
}

// cmdChainGrpcHTTP 实现 `query grpc-callers|http-callers <符号>`。
func cmdChainGrpcHTTP(acts *action.Actions, repo *sqlite.Repo, symbol string, which string, jsonOut bool) int {
	out := chainGrpcHTTP(acts, repo, symbol)
	if jsonOut {
		encodeJSON(out)
		return 0
	}
	if which == "grpc-callers" {
		if len(out.Grpc) == 0 {
			fmt.Printf("调用链 %s 未调用任何 grpc 服务\n", symbol)
			return 0
		}
		fmt.Printf("== %s 调用链最终调用的 grpc ==\n", symbol)
		for _, g := range out.Grpc {
			loc := ""
			if g.Line > 0 {
				loc = fmt.Sprintf("（%s:%d）", g.CalledBy, g.Line)
			} else {
				loc = "（" + g.CalledBy + "）"
			}
			fmt.Printf("  %s %s\n", g.Service, loc)
		}
		return 0
	}
	if len(out.HTTP) == 0 {
		fmt.Printf("调用链 %s 未调用任何 http 接口\n", symbol)
		return 0
	}
	fmt.Printf("== %s 调用链最终调用的 http ==\n", symbol)
	for _, h := range out.HTTP {
		loc := ""
		if h.Line > 0 {
			loc = fmt.Sprintf("（%s:%d）", h.CalledBy, h.Line)
		} else {
			loc = "（" + h.CalledBy + "）"
		}
		fmt.Printf("  %s %s %s\n", h.Service, h.Path, loc)
	}
	return 0
}

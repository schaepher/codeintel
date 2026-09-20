package sqlite

import (
	"database/sql"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// maxPathVisited BFS 访问上限（病态图内存兜底；与 maxDepth 语义无关）。
const maxPathVisited = 200000

// GetPath 节点间最短路径（field_trace.md §17.3）：
// BFS（有向 from→to，visited 防环），返回路径节点序列（TraceRow，
// EdgeKinds = 进入该节点的边类型）。viaCalls=true 用函数调用边集
// （calls/passes_to/passes_result），否则数据流边集（data_flows_to/
// argument/returns/phi_operand/summary_io）。不可达返回空切片。
func (r *Repo) GetPath(from, to domain.CanonicalID, maxDepth int, viaCalls bool) ([]*domain.TraceRow, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).GetPath")
	defer logger.Debug("exit (Repo).GetPath")
	if from == to {

		n, err := r.GetSymbol(from)
		if err != nil {
			return nil, err
		}
		return []*domain.TraceRow{{ID: from, Name: n.Name, Kind: n.Kind, Line: n.LineStart}}, nil
	}
	// Q252d：邻接表来自进程内缓存（按 build_id 复用，全表扫边只做一次），
	// 按 kind 集合切视图——原先每次调用都全表扫边 + 建 map（77-110ms/次）
	view := edgeViewDataFlow
	if viaCalls {
		view = edgeViewCalls
	}
	adj, err := r.adjacencyView(view)
	if err != nil {
		return nil, err
	}
	// Q254c Stage 2：邻接表在整数代理键空间 → 先把起点/终点解析成 id_int
	// （两次唯一索引点查），BFS 只搬 int64；结果回填 canonical ID 供输出。
	fromRef, ok, err := r.nodeRef(from)
	if err != nil || !ok {
		return nil, err
	}
	toRef, ok, err := r.nodeRef(to)
	if err != nil || !ok {
		return nil, err
	}

	type hop struct {
		prev int64
		kind string
	}
	parent := map[int64]hop{}
	// Q252c：按**深度**限制扩展——原实现用 `len(parent) <= maxDepth`
	// （已发现节点数当预算），起点扇出稍宽或目标在若干跳之后时 BFS 提前
	// 停止扩展，**可达的两点被静默报成"无路径"**（go2o 实测 4 跳内抽样
	// 113 对里 6 对假阴性）。长度上限另用独立常量兜底（防病态图内存）。
	depth := map[int64]int{fromRef: 0}
	queue := []int64{fromRef}
	visited := map[int64]bool{fromRef: true}
	found := false
	for len(queue) > 0 && !found && len(visited) < maxPathVisited {
		cur := queue[0]
		queue = queue[1:]
		for _, e := range adj[cur] {
			if visited[e.to] {
				continue
			}
			visited[e.to] = true
			parent[e.to] = hop{cur, e.kind}
			depth[e.to] = depth[cur] + 1
			if e.to == toRef {
				found = true
				break
			}
			if depth[e.to] < maxDepth {
				queue = append(queue, e.to)
			}
		}
	}
	if !found {
		return nil, nil
	}
	// 回溯路径（整数 → canonical）
	var refs []int64
	var kindsPath []string
	for cur := toRef; cur != fromRef; {
		refs = append(refs, cur)
		p := parent[cur]
		kindsPath = append(kindsPath, p.kind)
		cur = p.prev
	}
	refs = append(refs, fromRef)
	for i, j := 0, len(refs)-1; i < j; i, j = i+1, j-1 {
		refs[i], refs[j] = refs[j], refs[i]
	}
	for i, j := 0, len(kindsPath)-1; i < j; i, j = i+1, j-1 {
		kindsPath[i], kindsPath[j] = kindsPath[j], kindsPath[i]
	}
	names, err := r.canonicalOfRefs(refs)
	if err != nil {
		return nil, err
	}

	out := make([]*domain.TraceRow, 0, len(refs))
	for i, ref := range refs {
		id, ok := names[ref]
		if !ok {
			continue
		}
		n, err := r.GetSymbol(id)
		if err != nil {
			continue
		}
		row := &domain.TraceRow{ID: id, Name: n.Name, Kind: n.Kind, Line: n.LineStart}
		if i > 0 {
			row.EdgeKinds = kindsPath[i-1]
		}
		out = append(out, row)
	}
	return out, nil
}

// GetGrpcCalls 模块间调用原始行（field_trace.md §18.3/§18.7）：
// grpc_call 边（客户端调用方 → grpc_service）+ 经 grpc_impl 边反查
// 服务端实现类型；http_call 边（→ http_route，经 route.handler_id
// 反查服务端 handler 函数）。无实现/无 handler 时 ImplTypeID 空——
// 服务端不在仓库内（[外部服务]）。
func (r *Repo) GetGrpcCalls() ([]*domain.GrpcCallRow, error) {
	logger := zap.L()
	logger.Debug("enter (Repo).GetGrpcCalls")
	defer logger.Debug("exit (Repo).GetGrpcCalls")
	rows, err := r.Query(`SELECT e.source_id, e.target_id, n.name,
		COALESCE(json_extract(e.metadata, '$.method'), json_extract(e.metadata, '$.path'), ''),
		COALESCE(json_extract(e.metadata, '$.line_num'), 0),
		CASE WHEN e.kind = 'grpc_call' THEN
			(SELECT s.source_id FROM edges_v s JOIN nodes sn ON sn.id = s.target_id
			 WHERE s.kind = 'grpc_impl' AND sn.name = n.name LIMIT 1)
		ELSE json_extract(n.properties, '$.handler_id') END,
		e.kind
	FROM edges_v e JOIN nodes n ON n.id = e.target_id
	WHERE e.kind IN ('grpc_call','http_call') ORDER BY e.source_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.GrpcCallRow
	for rows.Next() {
		var (
			row       domain.GrpcCallRow
			caller    string
			svc       string
			name      string
			method    string
			line      int
			impl      sql.NullString
			transport string
		)
		if err := rows.Scan(&caller, &svc, &name, &method, &line, &impl, &transport); err != nil {
			return nil, err
		}
		row.CallerID = domain.CanonicalID(caller)
		row.ServiceID = domain.CanonicalID(svc)
		row.Transport = transport
		if transport == "grpc_call" {
			row.Service = strings.TrimPrefix(name, "svc.")
			if pkg := pkgOfID(row.ServiceID); pkg != "" {
				row.Service = pkg + "." + strings.TrimPrefix(name, "svc.")
			}
			row.Method = method
		} else {

			row.Method = method
			row.Service = name
		}
		row.Line = line
		if impl.Valid && impl.String != "" {
			row.ImplTypeID = domain.CanonicalID(impl.String)
		}
		out = append(out, &row)
	}
	return out, rows.Err()
}

// pkgOfID 从 canonical ID 提取包路径（symbol:go:<pkg>:<name>）。
func pkgOfID(id domain.CanonicalID) string {
	s := strings.TrimPrefix(string(id), "symbol:go:")
	if i := strings.LastIndex(s, ":"); i >= 0 {
		return s[:i]
	}
	return s
}

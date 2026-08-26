package sqlite

// orderImplsByGrpc grpc 服务实现排后（R97：接口方法具体化优先业务
// 实现——grpc 实现内部调接口时避免具体化回自身造成时序图自环；
// grpc 实现 = grpc_impl 边 source，内存集合判定）。
func (r *Repo) orderImplsByGrpc(implIDs []string) []string {
	if len(implIDs) <= 1 {
		return implIDs
	}
	grpcImpls := map[string]bool{}
	rows, err := r.Query(`SELECT DISTINCT source_id FROM edges WHERE kind = 'grpc_impl'`)
	if err == nil {
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil {
				grpcImpls[id] = true
			}
		}
		rows.Close()
	}
	if len(grpcImpls) == 0 {
		return implIDs
	}
	var biz, grpc []string
	for _, id := range implIDs {
		if grpcImpls[id] {
			grpc = append(grpc, id)
		} else {
			biz = append(biz, id)
		}
	}
	return append(biz, grpc...)
}

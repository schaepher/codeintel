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

// GetFieldWriters 字段的 direct_write 写入函数（R97-2 数据流具体化：
// receiver 字段赋值来源——构造函数等；fieldPath 完整形态
// "<pkg>.<Type>.<field>"，匹配摘要 field_path 全等）。
func (r *Repo) GetFieldWriters(fieldPath string) ([]string, error) {
	rows, err := r.Query(`SELECT function_id FROM function_field_summary
		WHERE field_path = ? AND access_kind = 'direct_write'`, fieldPath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			out = append(out, id)
		}
	}
	return out, rows.Err()
}

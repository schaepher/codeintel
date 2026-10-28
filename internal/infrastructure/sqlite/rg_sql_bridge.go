package sqlite

// rg_sql taint 桥查询（Q218）——单独成文件做行数治理。
//
// P1-11：同函数判定改走 nodes.id 前缀范围（funcID+"#" 前缀不变量，见
// funcid_range.go），不再依赖已删除的 idx_nodes_func_id 表达式索引；
// n1 无 func_id 时范围条件为 NULL → 无行（与旧等值语义一致）。
// 参数：① n1 canonical ID ② 字段名片段（instr 匹配）。
const sqlBridgeReadsSQL = `SELECT n2.id FROM nodes n1, nodes n2
					WHERE n1.id = ? AND n2.kind = 'field_access'
					  AND json_extract(n2.properties, '$.access_kind') = 'read'
					  AND n2.id >= json_extract(n1.properties, '$.func_id') || '#'
					  AND n2.id < json_extract(n1.properties, '$.func_id') || '$'
					  AND instr(json_extract(n2.properties, '$.full_path'), ?) > 0
					  -- 精确桥：仅桥下游 2 跳内可达 filter 节点的字段读取
					  -- （字段 → 值 → filter：真正进 Where 的字段；防同类型全字段扩散）
					  AND EXISTS (
						SELECT 1 FROM edges_v e1
						JOIN edges_v e2 ON e2.source_id = e1.target_id
						JOIN nodes n3 ON n3.id = e2.target_id
						WHERE e1.source_id = n2.id
						  AND n3.kind = 'field_access'
						  AND json_extract(n3.properties, '$.access_kind') = 'filter'
						  AND json_extract(n3.properties, '$.is_external') = 'true'
					  )`

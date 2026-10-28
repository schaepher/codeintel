package sqlite

// S2/S3 字段追溯（repo_trace.go）的递归 CTE——单独成文件（行数治理）。
//
// P1-11：函数作用域不再用 `json_extract(properties,'$.func_id') = ?`
// （idx_nodes_func_id 已删），改 nodes.id 前缀范围（funcID+"#" ≤ id <
// funcID+"$"，见 funcid_range.go）。全局变量起点单列一支 UNION——放进
// func_id 的 OR 会让 id 范围扫失效。
const traceBackwardSQL = `WITH RECURSIVE def_trace(id, depth, name, edge_kinds, line) AS (
    SELECT n.id, 0, n.name, '', n.line_start
    FROM nodes n
    WHERE n.kind = 'field_access'
      AND json_extract(n.properties, '$.full_path') = ?
      AND n.id >= ? AND n.id < ?
    UNION
    SELECT e.source_id, d.depth + 1, n_prev.name,
           CASE WHEN d.edge_kinds = '' THEN e.kind
                 ELSE d.edge_kinds || ',' || e.kind END, n_prev.line_start
    FROM edges_v e
    JOIN def_trace d ON e.target_id = d.id
    JOIN nodes n_prev ON e.source_id = n_prev.id
    WHERE e.kind IN ('data_flows_to','argument','returns','alias','phi_operand')
      AND d.depth < ?
)
SELECT id, depth, name, edge_kinds, line FROM def_trace ORDER BY depth, id`

const traceForwardSQL = `WITH RECURSIVE fwd_trace(id, depth, name, edge_kinds, line, is_usage, kind) AS (
    SELECT n.id, 0, n.name, '', n.line_start, 1, n.kind
    FROM nodes n
    WHERE n.kind = 'field_access'
      AND json_extract(n.properties, '$.full_path') = ?
      AND n.id >= ? AND n.id < ?
    UNION
    -- 起点：与目标字段所属结构体同类型的 SSA 值（param/receiver/alloc/
    -- local/phi/global——① 传参、⑩ 局部对象、⑭ DAO 返回对象→局部变量→
    -- helper、global 溯源）。B2：类型不匹配的参数与全局变量（gitCommit
    -- 等 string）不再是起点——此前 origin_kind IN ('param','receiver',
    -- 'alloc','global') 无条件放行，全部参数与全局变量入链（噪音）。
    -- Q178：参数统一为签名参数节点（kind=parameter，#param.<name>）
    SELECT n.id, 0, n.name, '', n.line_start, 0, n.kind
    FROM nodes n
    WHERE n.kind IN ('ssa_value', 'parameter')
      AND n.id >= ? AND n.id < ?
      AND (json_extract(n.properties, '$.type_string') = ?
           OR json_extract(n.properties, '$.type_string') = ?)
    UNION
    -- P1-11：全局变量起点单列一支（不放进 func_id 的 OR——OR 会让
    -- id 前缀范围扫失效；UNION 去重语义与原 OR 等价）。
    SELECT n.id, 0, n.name, '', n.line_start, 0, n.kind
    FROM nodes n
    WHERE n.kind IN ('ssa_value', 'parameter')
      AND json_extract(n.properties, '$.origin_kind') = 'global'
      AND (json_extract(n.properties, '$.type_string') = ?
           OR json_extract(n.properties, '$.type_string') = ?)
    UNION
    -- 起点：承载目标类型对象的字段访问节点（C 形态：s.cfg → fill 写
    -- c.Key——容器对象 Svc 自身类型不匹配，但其字段节点 s.cfg 类型
    -- *Cfg 匹配，作起点经 argument 进入 callee）
    SELECT n.id, 0, n.name, '', n.line_start, 0, n.kind
    FROM nodes n
    WHERE n.kind = 'field_access'
      AND n.id >= ? AND n.id < ?
      AND (json_extract(n.properties, '$.type_string') = ?
           OR json_extract(n.properties, '$.type_string') = ?)
    UNION
    SELECT e.target_id, d.depth + 1, n_next.name,
           CASE WHEN d.edge_kinds = '' THEN e.kind
                 ELSE d.edge_kinds || ',' || e.kind END, n_next.line_start,
           CASE WHEN n_next.kind = 'field_access'
                     AND json_extract(n_next.properties, '$.full_path') = ? THEN 1 ELSE 0 END,
           n_next.kind
    FROM edges_v e
    JOIN fwd_trace d ON e.source_id = d.id
    JOIN nodes n_next ON e.target_id = n_next.id
    WHERE e.kind IN ('data_flows_to','argument','returns','phi_operand','alias')
      -- 字段访问步：目标字段任意读写放行；参数/值 → 其他字段读 放行
      -- 但仅限与目标字段同名的读（⑫ 跳板精确化：dest.ID = src.ID 拷贝链
      -- 同名保留——kg.ID → t42.ID；record.Metadata/Status 等无关字段
      -- 读拦截——噪音与超时来源）；字段访问 → 字段访问 仅限目标字段
      AND (n_next.kind != 'field_access'
           OR json_extract(n_next.properties, '$.full_path') = ?
           OR (d.kind != 'field_access' AND json_extract(n_next.properties, '$.access_kind') = 'read'
               AND (json_extract(n_next.properties, '$.full_path') LIKE ?
                    OR json_extract(n_next.properties, '$.type_string') LIKE ?)))
      AND d.depth < ?
)
SELECT id, depth, name, edge_kinds, line, is_usage FROM fwd_trace ORDER BY depth, id`

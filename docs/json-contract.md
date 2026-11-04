# JSON 输出契约（Q243）

所有 `--json` / `export` 输出统一 **snake_case** 字段名，稳定承诺：
schema 变更需更新本契约并走 Q 流程（Agent/MCP 依赖字段名解析）。

## 标准

- 字段名 snake_case（`file_path`、`source_id`、`access_kind`）
- 空/零值字段省略（`omitempty`），调用方按缺失处理而非 null
- 顶层为对象（`query context`）或数组（`query table`、`relations`）
- canonical ID 一律字符串；置信度 `confidence` float64（0.0~1.0）
- 行号 `line`/`line_start` int；`file`/`file_path` 为仓库相对路径
- **不泄露数据库内部整数代理键**：`id_int` / `source_ref` / `target_ref` 仅库内使用；节点与端点对外一律 canonical ID 字符串

## 核心类型（domain 层，Q243 加 tag 统一）

| 类型 | 关键字段 |
|---|---|
| CodeEntity | id / kind / name / file_path / line_start / line_end / properties |
| Fact | source_id / target_id / kind / tool_source / confidence / metadata |
| FunctionFieldSummary | function_id / access_kind / field_path / instance_path / line_start / code_snippet / origins |
| TraceRow | id / depth / parent_id / name / edge_kinds / line / is_usage / dir / kind / access / func_id / file_path / full_path / conditions / dispatch_* / edge_* |
| TableRelation | from_table / from_col / to_table / to_col / hops / type |
| RelationRule | id / from_table / from_col / to_table / to_col / created_at |
| TableColumn | name / access / line_start / writers / readers |
| SummaryStep | kind / name / file / line / func |
| TablePathStep | from_table / from_col / type / to_table / to_col |

## 各命令输出结构

- **query symbol**：对象——id/name/kind/file/line/signature/doc/callers[]/callees[]/candidates[]（callers/callees 元素：id/name/kind/file/line/signature/source/target/edge_kind/tool/confidence/line_num——source/target 为完整边端点）
- **query context**：对象——symbol/fields(direct_read|direct_write|indirect_write)/chain[]/traces[]/dispatch[]/callers[]/callees[]（顶层键缺失=无数据，omitempty）
- **query fields**：对象——name/rows[]（rows 元素：access_kind/field_path/instance_path/line/code_snippet/origins）或 field/func/rows
- **query trace-backward/forward**：对象——flows[]（元素：id/name/depth/dir/edge/line/kind/access/func_id/func_name/conditions/dispatch/edge_candidate）
- **query value-trace**：对象——flows[]（同 trace，含 [读]/[写] 语义在 access）
- **query callers/callees/impact**：对象——target/rows[] 或 target/nodes[]（callers/callees rows 元素：id/name/kind/file/line/signature/source/target/edge_kind/tool/confidence/line_num；impact nodes 元素：id/name/kind/file/line）
- **query summary**：对象——producers[]/consumers[]/chain[]（producers/consumers 元素：function/access/line/instance/code）
- **query context/table**：数组——TableColumn[]（表.列虚拟节点）
- **query relations**：数组——TableRelation[]（--all 同样式）
- **query table-path**：对象——path[]/candidates[]/hops/reachable（元素 TablePathStep）
- **query unused**：对象——unused[]/total（unused 元素：id/name/kind/file/line/exported/referenced/since）
- **query module-calls**：对象——calls[]（元素：from_module/to_module/service/method/transport/caller/line）
- **query path**：对象（--since 标注）——见 field_trace.md §17
- **list**：数组——short/path/module/status/worktree_of/workspace
- **export**：对象——fields{}（field→producers/consumers）或 relations[]（graph --type）
- **query sequence**：对象——Nodes[]/Else[]/meta（节点字段：Kind/Label/Actor/Type/Args/
  Returns/Line/Exec/InReturn/Values/Slots/Candidate/stop/evidence）。`evidence` 可含：
  - `storage`（U04b，SQL **写入**结构化证据）：`operation`（insert|update）、
    `table`、`writes[]`（column/value）、`predicates[]`（WHERE AND 等式）、
    `resource_declaration`（仅准确的包级无初始化 `var X *sql.DB`）、`guard`、
    `outcome`（恒为 `unproven`）、`boundary`（`record_identity_unproven` |
    `resource_identity_unproven` | `sql_shape_unproven` | `query_not_proven` |
    `argument_count_unproven`）。`value`：`kind`（argument|literal）、
    `argument`（零基，不含 context/SQL；字面量 -1）、`expression`（实参在调用处
    的 Go 原文）、`sql`（字面量原文）、可选 `origin`（U04c 标量来源）。
     `origin`：`kind`（parameter|literal|concat）、`scalar_type`、`symbol`
    （入口 canonical ID）+ `parameter`（零基）+ `repo`（parameter）、
     `literal`（Go 规范化原文）、`parts[]`（concat，有界：深度 8 / 64KiB）。
     **这组字段只描述语句结构与实参绑定**：值来源、资源身份、写入成功与
     跨入口顺序都未证明，不得据此生成因果边；origin 也不证明另一个入口的
     参数与本入口相等。
  - `storage.resource`（U04d）：`status`（bound|binding_missing|
    binding_mutated|analysis_not_visited|ambiguous_resource）+ `source_id`/
    `symbol`/`snapshot_hash`（仅 bound 带前两者）。
  - `storage.error_result`（U04h）：`call_id`（每个实际调用节点一个）、
    `repo`/`entry`/`symbol`/`file`/`offset`/`slot`（SQL/XORM 写入恒为 1）。
  - `storage_read`（U04e）：`table`/`columns`/`predicates`/`resource_declaration`/
    `guard`/`outcome`/`boundary`（+可选 `resource`）；不证明唯一记录或读取成功。
  - `orm`（U04f/U04g/U04i/U04j）：`framework`/`operation`/`table`（或
    `table_expression`）/`arguments`/`predicates`/`predicate_coverage`/
    `outcome`/`boundary`（orm_state_unproven|table_identity_unproven）；
    `models[]`（argument/symbol_id/source/fields[{name,go_type,tag,embedded}]/
    boundary=model_mapping_unproven）；`error_result`（+`signature_source`）；
    `row_count_result{write,slot:0,access?}`。
  - `transaction`（U04n）：`origin{id,repo,entry,symbol,file,offset,slot}`/
    `action`（begin|write|read|commit|rollback）/`guard`/`exec`（defer|async）/
    `outcome`（unproven）/`boundary`（transaction_origin_unproven）。
  - `runtime`（todo25/T19）：`table`/`columns`/`query`/`source_id`/`symbol`/
    `result`/`reason`/`boundary{kind,code,needs}`；kind = missing_fact |
    analysis_boundary | ambiguous_fact，needs 只列真正需要的事实。
    T19b：无有效匹配时缺口优先级固定为 scope_not_covered > columns_missing >
    incomplete_rows > missing_observation（与 observation 输入顺序无关）；多条
    有效观测 → ambiguous_observations。相同 source/table/规范化 scope 的观测
    不再在加载阶段拒绝，保留后由读取端判 ambiguous（不会拼接）。
  - `field_read`（U04v/U04y）：`id`（每次求值独立）、`root{kind,symbol,repo,
    parameter,type_id?,id?}`、`path`、`type_id`（字段声明 owner）、
    `source_file/source_line`（声明位置）、`read_file/read_offset`（本次读取
    位置）。根支持 parameter_object / struct_literal / struct_copy；构造/复制
    根带 `type_id` 与观察 `id`（历史对象身份，不是当前值）。
  - `struct_literal`（U04w）：`id`（构造观察）、`type_id`、`semantics=
    initialization_only`、`file/offset`、`fields[{name,origin?}]`（省略字段不
    补零值来源；显式未知字段保留名字、无 origin）。
  - `struct_copy`（U04x）：`id`、`type_id`、`semantics=
    construction_ancestry_only`、`file/offset`、`source`（构造祖先来源）、
    `guard`/`conditional`。
  - `meta.continuation`（T12b）：`expanded`/`extra_expansions`/`segments_used`
    都按**实际追加的前沿展开次数**统计（一个前沿一次；不是"一轮展开所有
    兄弟"）；预算耗尽仍停在 depth 的节点记入 `budget_stops`。identity_changed
    与预算停止不计入 expanded/extra_expansions/segments_used。
  - RPC 服务端续接证据 `evidence.source = "rpc_server"`（原 `"rpc"`），
    `symbol_id` 为实现方法（如 `(Handler).Submit`），`di_register_file/line`
    是真实注册调用位置。
  - 节点级 `field_writes`（U04y）：每项 `id`、`semantics=assignment_only`、
    `file/offset`、`operator`、`target{type_id,source_file,source_line,path,
    root?}`、`value`（仅右操作数来源）、`expression`、`result_slot`、
    `guard`、`scheduled`；超预算项只带 `stop.code=field_write_budget`。
  - 节点级 `write_condition`（result{call_id,slot}+when_true/when_false =
    error_nil|error_non_nil）、`row_count_condition`（rows_gt_zero|
    rows_le_zero|rows_ge_zero|rows_lt_zero|rows_eq_zero|rows_ne_zero）、
    `transaction_condition`（result{call_id,operation,slot}）、
    `value_origins`/`result_origins`（return 槽来源）、`method_path`（外部方法
    owner 链）。
- **rule list**：数组——RelationRule[]

## 历史（为何有本文档）

Q235-5 `query context` 直接 marshal domain.CodeEntity/Fact/FunctionFieldSummary/
TraceRow——这些类型当时无 json tag，输出 Go 默认 camelCase
（`ID`/`FilePath`/`SourceID`/`AccessKind`...），与其余命令的 snake_case
不一致，机器消费（Agent/MCP）易错。Q243：domain 全部输出类型统一加
snake_case tag；`internal/cli/json_contract_test.go` 固化契约（禁止
camelCase 键出现）。server /api 的 NodeJSON/EdgeJSON（camelCase
`fullPath`/`funcName`）是前端私有契约，不在本契约内——前端专有，不承诺。

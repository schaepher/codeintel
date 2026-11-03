# todo28 交付 Checklist（输入：`todo28.txt`，基线 `origin/main` 8da4ef9）

规则：按编号独立验收；未完成项保留原因与剩余范围，不用"主要功能完成"代表整项。
生产链路一律 **源码 → init → CLI**（除明确允许的存储/解析单测）。

## 状态总表

| # | 事项 | 状态 | 证据 / 剩余范围 |
| --- | --- | --- | --- |
| T01–T14 | 调用分析/续查增量 | **已在前一轮交付（todo27）** | `docs/todo27-checklist.md` 逐项证据（`integration/todo27_t0*_test.go`）；todo28 的 E-T01…E-T14 示例为兼容基线，前后输出一致 |
| T18 | 回归/兼容声明/文档 | **部分**（既有回归保持） | 本轮 U04 相关的 adapter 超时/分批/边图/包缓存回归未改动；剩余"本轮新增生产路径的契约同步"随 U04 各子项持续更新 |
| T19 | 通用运行时事实边界（boundary.kind/code/needs） | **待办** | 需在 `runtime_evidence.go` 加 `SeqRuntimeBoundary`（missing_fact/analysis_boundary/ambiguous_fact + code/needs），JSON/文本/Mermaid 同源；缺快照时只对准确 `database/sql` 身份与字面量 SELECT 发现缺口 |
| U04a | 持久化诊断属于实际 DB 写入调用 | **已完成（本轮）** | `seqPersistenceSQLArg/seqPersistenceSQLText`（DB/Tx Exec 参数 0、DB/Tx/Conn ExecContext 参数 1；Stmt.Exec/自定义同名/日志/缺 SymbolID 不产生诊断）；`action_seq_persistence_identity_test.go`（矩阵 + E-U04a 原文 `<nil>`） |
| U04b | SQL 写入结构化证据 | **已完成（本轮）** | `action_seq_storage*.go`：表/列/谓词来自 vitess AST，实参零基绑定（不含 context/SQL，保留调用处表达式原文），字面量原文；资源声明只认准确包级无初始化 `var X *sql.DB`；`sql_shape_unproven`/`query_not_proven`/`argument_count_unproven` 不保留部分表列；删除字符串切分；`integration/todo28_u04b_test.go`（UPDATE 编号/字符串内逗号/局部资源/动态 SQL/数量不符/日志） |
| U04c | 标量/派生记录键来源 | **已完成（本轮）** | `action_seq_scalar*.go`：`seqEnv.scalars`（按 AST 对象 + 持续逃逸）；入口参数→parameter（symbol=入口/零基/repo）、被调形参沿用调用点捕获来源、字面量规范化、有界 concat；覆盖/compound/incdec/range/取址/闭包写者失效；分支/循环合并；`integration/todo28_u04c_test.go`（Entry、Escape/Capture、Branch/Same、Shadow、Distinct、Compound/Loop，depth1+续查与 depth12 同口径） |
| U04d | 写入资源绑定与读写共同失效 | **待办** | 需复用 `runtimeAnalysis/rtEval`：`storage.resource{status,source_id,symbol,snapshot_hash}`（bound/analysis_not_visited/binding_missing/binding_mutated/ambiguous_resource）；跨包重绑定、取址、闭包写者、局部遮蔽、for/switch init、未知调用效果、Scan 目标副作用；读取资源证据返回拷贝 |
| U04e | 读取选择器与符号键来源（storage_read） | **待办** | 复用 `parseRuntimeSelect` 的形态拒绝 + `seqSQLBinder`：DB/Tx/Conn 的 Query/QueryRow 及 Context 版本；`storage_read{table,columns,predicates,resource_declaration,guard,outcome,boundary}`；SQL 拼接折叠上限 8 层/64KiB；Mermaid 独立渲染读取边界（不改 Stop.Code） |
| U04f | XORM 终端操作与显式构建器 | **待办** | `evidence.orm{framework,operation,terminal arguments,guard,outcome,boundary}`；只按准确包/方法身份；Table/Where 链 owner 核对；`predicate_coverage=partial`；`orm_state_unproven` |
| U04g | ORM 模型声明与字段源码 | **待办** | `orm.models[]`（argument/symbol_id/source/fields/tag/embedded，`model_mapping_unproven`）；按静态声明类型关联（形参/变量/字面量/new/单结果构造器），接口与 any 返回不借具体对象 |
| U04h | SQL 写入错误返回槽与条件 | **待办** | `storage.error_result{call_id,slot=1,...}` + `write_condition`（error_nil/error_non_nil）；`_,err:=`/`=`/`var _,err=` 绑定、别名/形参透传、覆盖/逃逸/遮蔽失效；outcome 仍 unproven |
| U04i | XORM 错误槽与签名源码核验 | **待办** | 复用 U04h 结果来源；`orm.error_result.signature_source`；离线 `go list` 核验 ImportPath/目录/GoFiles/vendor 与遮蔽 |
| U04j | XORM 影响行数槽（slot 0） | **待办** | `orm.row_count_result{write,slot:0}` + `row_count_condition`（rows_gt_zero/rows_le_zero 等） |
| U04k | 返回槽来源传播 | **待办** | 实际展开的 return `value_origins` → 调用节点 `result_origins`；元组转发/具名裸 return；未知分支不丢弃 |
| U04l | 错误守卫下的返回槽等价 | **待办** | `if err != nil { return err }; return nil` 等路径约束证明同一写入错误槽 |
| U04m | RowsAffected 关联原始写入 | **待办** | `sql_result` 来源 + `(Result).RowsAffected` 身份；计数读取错误与事务提交分别保持边界 |
| U04n | 标准 SQL 事务身份与独立错误槽 | **待办** | Begin/BeginTx 来源、Tx 方法关联、`transaction{origin,action,guard,outcome}`、begin/commit/rollback 各自错误槽 |
| U04o | 外部递归定位保持主仓模块图 | **待办** | 外部依赖递归的 `go list` cwd 固定为请求 RepoAbs；不借依赖自身 replace |
| U04p | 外部包同包函数续接 | **待办** | 外部函数体内同包（同/跨文件）函数调用按选中 GoFiles 解析并接入展开/续查 |
| U04q | 外部方法声明 owner / 嵌入路径 | **待办** | `evidence.method_path` + `external-promoted-method`；最近嵌入层选择；菱形/遮蔽/接口字段保持 unknown |
| U04r | 外部类型声明文件的 receiver 字段 | **待办** | 按结构体声明文件的 import 解析字段类型；缺字段/泛型/循环保持未知 |
| U04s | 显式匿名字段访问 + 标准库方法终端 | **待办** | `tx.Tx.Commit()` 从匿名字段声明得到 leaf.Tx；`standard-method-declaration` + 事务 `transaction_origin_unproven` |
| U04t | 导入包变量 receiver | **待办** | `state.Shared.Work()` 读准确 VAR 声明（显式类型）；接口变量 unknown_receiver；局部遮蔽不借包变量 |
| U04u | 包变量省略类型时恢复静态类型 | **待办** | 字面量/&字面量/别名/准确包级函数返回签名（多槽）；接口返回保持未知 |
| U04v | 请求结构体字段读取来源 | **待办** | `field_read{id,root,path,type_id,source_file/source_line,read_file/read_offset}`；重复读取独立 ID；方法 getter 恢复调用边 |
| U04w | 结构体字面量构造来源 | **待办** | `struct_literal{type_id,fields,initialization_only}`；观察 ID/预算（64 字段/深度 8/256 观察） |
| U04x | 整值复制构造祖先 + 函数列表返回来源 | **待办** | `struct_copy{construction_ancestry_only}`；按实际函数列表逐项环境；go/defer 调度上下文 |
| U04y | 字段赋值观察与求值顺序 | **待办** | `field_writes[]`（assign 节点 + `assignment_only`）；RHS 先于下一 RHS；compound/incdec operator；field_write_budget |
| U06b | 候选截断与两种续查入口 | **待办** | 候选后代与 `candidate_budget` 均 `candidate=true`；两个续查入口都不登记前沿/不消耗预算 |
| U06c | 外部接口缺失类型节点时保留实例边界 | **待办** | 外部声明类型缺失不算具体类型；接口别名/命名类型链有界解析；具体构造器正例保留 |
| U06d | 已索引 vendor 源码的包身份校验 | **待办** | `SymbolSource` 的 vendor 边界（最近 go.mod、modules.txt、离线 go list 选中文件）；反例全拒绝 |
| U07 | 条件工厂的调用参与者与图可用性 | **待办** | 工厂字段/单例 factory 子调用的 Actor/Type 非空；JSON/Mermaid 同一 receiver；HTTP 真实 handler 回归 |

## 本轮已执行验证

```sh
scripts/verify.sh --quick                      # 全量非 race：通过（action 18.8s / cli 34.8s）
go test -tags integration ./integration -run 'TestU04|TestU06' -count=1   # 通过
```

## 已知边界（本轮未扩展）

- `storage` 只描述语句结构与实参绑定：写入成功、记录身份、资源身份与跨入口
  顺序都未证明（`outcome` 恒为 `unproven`）。
- `origin` 只描述源码来源：不代表真实 DB 值，也不证明另一个入口的参数与本
  入口相等；未知时省略而不是猜。
- 闭包**写入**才使标量槽持续逃逸（只读捕获不失效）——保守口径写在这里，
  后续若要求"任意捕获即逃逸"需同步 U04c 的验收。

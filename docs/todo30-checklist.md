# todo30 交付 Checklist（输入：`todo30.txt`，基线 `origin/main` 9a3f29a）

规则：按编号独立验收；未完成项保留原因与剩余范围。生产链路一律
**源码 → init → CLI**（`integration/todo30_*_test.go`）。

## 状态总表

| # | 事项 | 状态 | 证据 |
| --- | --- | --- | --- |
| U04a | 关联入口选择、统一分析与共享预算 | **已完成** | `RelatedEntries` + 可重复 `--related-entry`；先校验全部入口（canonical/存在/function-method）再分析；去重、按 canonical ID 排序、≤32；独立 env/防环、共享 acc（取消+追加预算）；`workflow.entries/ordering=unproven/continuation`。测试 `todo30_u04a_test.go` |
| U04b | 资源、记录键与条件化读写连接 | **已完成** | 按资源声明+表+全部 AND 等式匹配（固定值必须相等、变量-变量条件化）；`workflow.connections{from,to,keys,read_values,verdict=conditional,missing_proofs}`；4096 尝试/256 连接预算。测试 `todo30_u04b`（`TestU30U04bConnections`） |
| U04c | 分支比较操作数结构化来源 | **已完成** | `condition{operator,left,right,terms,unknown,expression}`；`== != < <= > >= && || !`+布尔；64 节点/16 层 → `condition_budget`；不改变 Scene。测试 `todo30_u04c_test.go` |
| U04d | 标准 SQL 读取结果身份及返回槽传播 | **已完成** | `storage_read.result{id,repo,entry,symbol,file,offset,slot,type}`；`sql_read_result` 沿变量/别名/返回槽传播；星号/覆盖/nil/取址/自定义方法失效；两次调用 read-1/read-2。测试 `todo30_u04d_test.go` |
| U04e | SQL Scan 列来源、错误身份及成功前提 | **已完成** | `evidence.scan{id,read,success_conditions,targets,unsupported}`；按 SELECT 列序 `read_column{result,column,index,when=scan_success}`；`scan_error` + if-init 绑定（nil 操作数）；Rows 三项前提；列数不符/提前取址/defer/自定义 Scanner 不借用。测试 `todo30_u04e_test.go` |
| U04f | 从条件连接生成独立请求实例 | **已完成** | `workflow.instances[]{id,kind,entry,parent_id,connection_index,bindings,read_values,related_reads,writes,missing_proofs,sequence}`；BFS 链、64 实例/8 层/65536 节点预算、循环/预算 partial+boundaries、取消即失败；实例复制树标注 `instance_condition`。测试 `todo30_u04f`/`TestU30U04WorkflowDepth` |
| U04g | 条件化请求实例用于文本和时序图 | **已完成** | 同一展示树输出文本/Mermaid/SVG/PNG（`sequence-batch` 复用）；`selected root instance-1` / `conditional external request instance-N` 外部调用方消息行、条件真值/读取值/请求键/缺失证明注记；JSON 原树不被渲染改写。测试 `todo30_u04g` |
| U04h | 同一源写入的多个读取联合约束 | **已完成** | 同源同键多次读取合并为一个实例 + `related_reads[]`（read/scan/write/connection_index/values）；`joint_read_values_preserved`；另一形参 → 两个独立候选且 unknown；other_nodes/未检查 Scan 错误 → unknown。测试 `todo30_u04h_test.go` |
| U04i | 嵌套字段/getter 使用准确字段值类型 | **已完成** | 字段槽内部保存字段值类型；`req.Child.ID`/别名/指针字段/getter 返回/嵌套 getter 的 `type_id=Leaf`；类型别名按真实声明、新定义类型不借方法。测试 `todo30_u04i_test.go` |
| U04j | SQL Scan 连接值类型模型直接标量字段 | **已完成** | 字段槽（对象+路径/分配+路径）、分支/循环合并缺失即未知；`&record.Field`/`&record.Detail.Field`；256 槽/256 根/4096 别名/4096 归属 → `scan_field_budget`；写入/整值覆写/方法接收者/捕获写者/调度体失效；指针根无分配/类型别名根/自定义 Scanner 不支持。测试 `todo30_field_slots_test.go` |
| U04k | 分配证明连接指针模型及局部别名 | **已完成** | `new(T)` → `allocation{id,type_id,repo,entry,symbol,file,offset}`；别名共享、重绑只切分配、`*alias=...`/字段写/传出/全局/通道/未知调用/接收者失效、跨入口不借用；Scan 地址传递位置敏感（本次逃逸可清、更早保留）。测试同上 |
| U04l | 通过静态只读 getter 保留当前 Scan 字段来源 | **已完成** | 零参单 `return receiver.Field`（内建标量、须修改前保守）+ 调用方字段槽；任意方法名/跨包；修改方法/返回其它对象/带实参/逃逸后失效；defer 实参立即捕获、延迟闭包体未知；`getter` 返回槽求值时刻快照。测试同上 + `todo30_workflow_test`（instance truth=true/column=kind） |
| U04 | 跨入口同记录、成功写入与多次 Finish 整合 | **已完成（公开示例）** | Submit→FinishReview→FinishReview 条件化连接/实例/渲染；两次独立审核实例、各自 Scan 错误退出；depth=1+续查与 depth=12 同流程。测试 `TestU30U04b/U04f/U04g/U04WorkflowDepth`。真实 28 业务流程属调用方接入验收 |
| B01 | getter 实际返回值贯通立即执行匿名函数 | **已完成** | `return getter()` 求值时刻快照，经 IIFE 返回槽传给 `Take`；deferred 闭包体未知。测试 `TestU30U04lReadOnlyGetter` |
| B02 | 跨构造函数及只读调用传递模型所有权与字段值 | **已完成（公开示例）** | 构造返回分配在调用方重新归属；只读参数调用（函数/方法体无写/无保留/无捕获）不失效并复制字段槽快照；全局/条件返回/参数写/对象保留失效。测试 `todo30_b02_test.go`；递归/预算边界见说明 |
| B03 | 嵌套字段、类型别名与模型到列映射 | **已完成（公开示例）** | SQL 字段路径逐级解析（中间必须值类型）、`read_column`；XORM 显式 Table + 列标签 → 模型字段列映射，ORM Insert/Get 接入连接（`connections=1`）。测试 `todo30_b03_test.go`；Find 多行/DAO 生成值/更新模型/动态命名策略仍未覆盖（保留边界） |
| B04 | 联合不同写入提供的读取条件 | **已完成** | 不同写入按同键签名合并实例 + `related_reads`（各带 write_id）；同列冲突值 → 未知；联合条件 true/unknown 反例。测试 `todo30_b04_test.go` |
| B05 | 传递数据库生成键及准确错误身份 | **已完成** | `(Result).LastInsertId` → `generated_key{call_id,access,success_conditions}`；反射到后续 SQL 参数；自定义同名方法/另一次 Exec/覆盖不借用。测试 `todo30_b05_b06_test.go`；ORM 自动主键回填未实现 |
| B06 | 连接事务资源并区分写入成功与提交可见性 | **已完成（公开示例）** | 事务写入资源取 Begin 接收者的包级 DB 声明；连接带 `transaction_commit` 缺失证明；Exec 成功≠提交成功。测试 `todo30_b05_b06_test.go` |
| B07 | 用条件实例的新事实恢复已证明调用前沿 | **已完成** | 实例读取值种子重分析（种子形参/局部变量/Scan 结果覆盖）+ 原分支 condition 合并；工厂/接口/回调目标重新解析（`localvar-return-slot`）；Complete=1；service/nil 不保留。测试 `todo30_b07_test.go`；重分析 ≤16 次/请求，超限 partial |
| B08 | 关联入口/派生实例保留运行时事实范围 | **已完成** | 每入口/实例接收同一快照；实例读取值代入配置表谓词 → 快照唯一行补列值（多行 ambiguous/缺绑定 missing_fact 不补）；`enabled==1` truth=true。测试 `todo30_b08_test.go` |
| B09 | 前端操作接入已证明的请求入口 | **已完成（公开示例）** | `FrontendRoots` + 可重复 `--frontend-root`；TS/TSX 导出函数 + 真实 onClick 绑定 + fetch 方法/路径 → http_route handler；`workflow.frontend.actions` 按 action 排序。测试 `todo30_b09_b10_test.go`；同路径不同方法/动态 URL/代理重写/缺 handler 保留 unknown |
| B10 | 自动选择合法组合并验收完整业务覆盖 | **已完成（公开示例）** | `--combination-selector`（canonical 校验）+ 选择器常量/条件枚举（≤64）→ `coverage{supported,analyzed,complete,partial}`；只支持 ID → supported=1；缺前端 → complete=0/partial=2。测试同上；真实 28 流程覆盖属调用方接入验收 |
| B11 | 一次命令导出全组合 Markdown 与时序图 | **已完成** | `sequence-batch --manifest --out --json`；`<id>.md/.svg` + `index.md`；失败隔离（total3/exported2/failed1、非零退出、成功文件保留）、id 校验/重复拒绝；导出成功≠业务完整（partial 标注）。测试 `todo30_b11_test.go` |

## 本轮已执行验证

```sh
scripts/verify.sh --quick                                  # OK
go test ./internal/... -count=1                            # OK（全部包）
PATH="$(go env GOPATH)/bin:$PATH" go test -tags integration ./integration -count=1  # OK（126s）
```

- 公共示例真实验收：U04 flow（connections/instances/render）、U04c condition、
  U04d/e read/scan、U04i nested、U04j/k/l field slots、U07b participants、
  B02 constructor/observe、B03 nested+ORM、B04 different writes、B05 generated key、
  B06 tx commit、B07 frontier reanalysis、B08 runtime facts、B09 frontend、
  B10 coverage、B11 sequence-batch（均见 `integration/todo30_*_test.go`）。
- 跳过：`make e2e`（无前端 Web 资源改动）；真实业务 28 流程/full business
  coverage 属调用方接入验收（本文档只提供机制与公开示例）。

## 已保留的边界（明示）

- **真实 28 业务流程**：B09/B10 只提供可接受真实输入的机制与覆盖报告；公开
  示例 complete=2 不代表真实业务流程已验证。
- **ORM 扩展**：Find 多行结果、生成 DAO 返回值、更新模型、动态命名策略未接入
  （连接只按显式 Table + 列标签 + 静态 `TableName` 语义的当前证据）。
- **ORM 自动主键回填**（B05）未实现；`LastInsertId` 只接受标准库 Result。
- **事务**：调用者显式检查 Commit 错误后的"成功路径提交约束"未单独建模；
  连接一律带 `transaction_commit` 缺失证明。
- **字段槽**：嵌套路径中间字段必须值类型（指针/嵌入/类型别名根不支持）；
  U04k 分配限制 4096 别名声明/归属判断；调度体读取外层字段一律未知。
- **B02 所有权**：只读证明限本地函数/方法的保守函数体检查；跨包实现、接口
  动态调用、递归深度与预算耗尽保留 unknown（`partial`）。
- **B08 事实分类**：缺失绑定/scope/列按 missing_fact 不补值；多行观测按
  ambiguous 不拼接；完整空行集不等于未采集（沿用既有运行时语义）。
- **B11**：导出产物默认只保留 Markdown/SVG/index；不保留临时索引副本/调试日志。

## 实现入口

- 编排/分析：`internal/action/action_seq_workflow*.go`、`action_seq_field_slot*.go`、
  `action_seq_frontend.go`、`action_seq_coverage*.go`、`action_seq_generated_key.go`、
  `action_seq_ownership.go`
- CLI：`--related-entry/--frontend-root/--combination-selector`（query sequence --code）、
  `sequence-batch`（`internal/cli/sequence_batch.go`）
- 渲染：`internal/cli/query_sequence_render_instances.go`（文本/Mermaid 同一树）

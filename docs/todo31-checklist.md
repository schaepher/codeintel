# todo31 交付 Checklist（输入：`todo31.txt`，基线 `origin/main` ca64fd1）

规则：按编号独立验收；生产链路 **源码 → init → CLI**（公开示例仓库
`example.com/remaining`）。未覆盖边界明确列出，不用公开示例代替真实 28 流程。

## 状态总表

| # | 事项 | 状态 | 证据 |
| --- | --- | --- | --- |
| U04bR | 拒绝已失效资源绑定并保留 SQL 节点路径 | **已完成** | 入口源码/本地调用链（深度≤4）检测包级资源改写（`Reset(){DB=nil}` → `binding_mutated`），Write/Read 所属入口资源被改写即不匹配；`from.path`/`to.path` 指向入口 sequence 实际节点（`/Nodes/0`）。测试 `todo31_remaining_test.go::TestU31U04bRResourceAndPaths` |
| U04hR | 字段键保持准确身份，不合并不同字段请求 | **已完成** | 字段读取来源（type_id + 字段路径 + 根身份）进入连接分组签名；`req.First`/`req.Second` → 2 连接 / 2 外部请求实例；反例覆盖不同字段/独立 Scan 错误（分组不合并）。测试 `TestU31U04hRFieldKeys` |
| U04fR | 生成前执行实例节点与来源大小上限 | **已完成** | 根/子实例加入结果**前**检查 65536 节点；超限 `instances=0 partial=true [instance_node_budget]`；单值来源序列化 >64KiB → `instance_value_origin` 且不参与读取替换。公开预算测试 `internal/action/remaining_budget_public_test.go`（输出逐字匹配） |
| B02b | 跨包只读调用保留对象与字段列值 | **已完成** | 包限定函数按导入路径解析**仓库内**包（外部依赖不加载源码）；只读证明要求函数体至少读取一次参数字段且无写入/保留/捕获/未知转手；`CrossPackage → observer.Observe` 得到 `read_column=kind`。测试 `TestU31B02bCrossPackage` |
| B03a | 指针嵌套字段/类型别名根/提升字段连接 Scan | **已完成** | 指针中间字段必须由同一对象的 `new(T)`/`&T{}` 初始化（或调用点前赋值）证明；类型别名根按真实声明解析；提升字段唯一命中（歧义拒绝）；指针字段重绑/整值覆写/全局对象清除嵌套槽。测试 `TestU31B03aNestedPointerAliasEmbedded` |
| B03b | ORM 更新模型和多行读取用于条件连接 | **已完成** | Update 模型字面量值 + Where 键接入写入；Find `[]T` 元素模型解析 → 列/读取身份；连接 1 条；模型字段列名按 xorm 标签（`pk autoincr 'id'` / `'kind'`）。测试 `TestU31B03bORMUpdateFind`；链对象重绑/动态命名/DAO 返回等保留边界 |
| B05b | ORM 自动主键回填传播到后续 SQL | **已完成** | `pk autoincr` 模型 + 已证明 Session.Insert：绑定 `record.ID ← generated_key{call_id,backfill_field,insert_success}`；后续 `audits.node_id` 来源 `generated_key`；显式主键/另一次 Insert/覆写不借用。测试 `TestU31B05bORMGeneratedKey` |
| B06b | 显式 Commit 错误检查形成成功路径提交约束 | **已完成** | 源入口 AST 检测 `if err = tx.Commit(); err != nil { return }` + 正常返回 → 连接 `success_conditions=["commit_success"]` 且移除 `transaction_commit` 缺失证明；忽略 Commit 错误/另一事务不借用。测试 `TestU31B06bCommitSuccess` |
| B09b | 通用前端请求包装传播方法/路径/参数 | **已完成** | 前端导出函数 + 实际 onClick 绑定 + 包装链实参位置传播（`request('/requests', id)` → `fetch(path,…)`）；常量方法/路径确定，连接真实注册 handler；同名/动态 URL/缺 handler 保留 unknown。测试 `TestU31B09bFrontendWrapper` |
| B10b | 逐合法组合实际分析并输出场景树 | **已完成** | 选择器参数按 `Supported(p,t,region)` 调用实参映射到入口形参，逐组合重分析；`workflow.scenarios[]{parameters,sequence,partial,reasons}`；ID 场景 `region=="ID"` taken、MY excluded；`coverage.analyzed` 只统计实际完成场景。测试 `TestU31B10bScenarios`；真实 28 流程属调用方接入验收 |
| B11b | 批量导出接入运行时事实/组合/重放身份 | **已完成** | manifest 每流程 `runtimeData/combinationSelector/depth/continueSegments/scene/timeout`；runtimeData 相对 manifest 解析；逐场景导出不复制同一张图；Markdown 写 `replay:` 参数与 runtime sha256；失败隔离/重复 id/非法字段规则保留。测试 `TestU31B11bBatchRuntimeAndScenarios` |

## 本轮验证

```sh
scripts/verify.sh --quick                 # OK
scripts/verify.sh                         # 全量 -race 14 包基线 OK
go test ./internal/... -count=1           # OK
PATH="$(go env GOPATH)/bin:$PATH" \
  go test -tags integration ./integration -count=1   # OK
go test -count=1 -v ./internal/action -run '^TestRemainingWorkflowBudgets$'
# node:  instances=0 partial=true boundaries=[instance_node_budget] error=<nil>
# value: instances=1 partial=true boundaries=[instance_value_origin] error=<nil>
```

公开示例逐条真实 init→CLI 核对：B02b、B03a（Pointer/AliasRoot/Embedded）、
B03b、B05b、B06b、B09b、B10b、B11b、U04bR、U04hR、U04fR。

## 明确边界

- 真实 28 业务流程 / 21–28 旧同步入口（0 次人工 FinishReview）需调用方提供源码后
  验收；本表只交付通用机制与公开示例。
- B03b 未覆盖：链对象重绑后的 ORM 身份、动态命名策略、DAO 包装返回值传播、
  其它 ORM 框架；这些继续保留 unknown，不猜表名/模型。
- B05b 只覆盖已证明 `Session.Insert` + `pk autoincr` 标签；失败路径/未检查错误
  在来源里保留 `insert_success` 前提（仍是条件化来源，不是实际执行证明）。
- B06b 只识别显式 `if err = tx.Commit(); err != nil { return }` 模式；延迟/条件
  Commit、错误被覆盖、Rollback 继续保留 `transaction_commit`。
- B09b 只解析 TS/TSX 导出函数 + 真实事件绑定 + 简单包装实参传播；代理重写、
  动态 URL、缺失源码继续 unknown。
- U04bR 资源状态目前以**源码改写**为主证明（binding_mutated）；未观察到的
  runtime `ambiguous_resource`/`analysis_not_visited` 不升级为连接。
- U04fR 值来源上限按 JSON 序列化大小（>65536 字节），超限不参与替换/连接。

# todo34 交付 Checklist（输入：`todo34.txt`，基线 `origin/main` 8513761）

规则：按编号独立验收；生产链路 **源码 → init → CLI**（公开示例
`example.com/remaining`）。未覆盖边界明确列出，不用公开示例代替真实业务验收。

## 状态总表

| 编号 | 状态 | 证据（要求 → 测试/CLI 输出） |
| --- | --- | --- |
| B06e.1 事务分配 | 完成 | 事务身份 = Begin 调用来源；`if err = tx.Commit()` 的证明只对该来源成立。`TestCommitAllocationIdentity`：`SubmitTx`/`SubmitTxAlias` → `commit_success`；`SubmitReboundTx`/`SubmitAliasRebind` → 保留 `transaction_commit` |
| B06e.2 错误槽 | 完成 | 被检查的错误必须与 `Commit` 的赋值 LHS 同一 `ast.Object`（别名沿赋值链）；`TestCommitErrorSlotIdentity`：`SubmitErrAlias` 正例，`SubmitWrongError`（检查 `errOther`）/`SubmitOverwriteError`（覆盖后再检查）反例 |
| B06e.3 正常出口 | 完成 | 全部正常出口都经过同一次成功检查才成立。`TestCommitExitPaths`：`SubmitConditionalCommit`/`SubmitRollbackOnly`/`SubmitClosureCommit`/`SubmitNoDirectReturn` 均无 `commit_success` |
| B06e.4 跨函数与延迟 | 完成（同包 helper 唯一 return），延迟/外部边界见下 | `TestCommitInterproceduralOrigins`：`openTx()` 返回同一事务 → `SubmitHelperTx` 有 `commit_success`；`SubmitHelperSplit`（另一事务）/`SubmitHelperConflict`（冲突返回）保留 `transaction_commit`；`SubmitClosureCommit`（未执行闭包里的 Commit）不证明 |
| U04bU.1 循环可达性 | 完成 | `TestResourceLoopPaths`：`ReviewZeroLoop`（`for false`）→ 1；`ReviewLoopOnce`（`for true`+break）、`ReviewLoopInit`（Init 执行）、`ReviewLoopPost`（Post 执行）→ 0 |
| U04bU.2 终止路径 | 完成 | `TestResourceTerminatedBranches`：`ReviewEarlyReset`（`if stop{DB=nil;return}`）→ 1；删除 return 的 `ReviewResetNoReturn` → 0 |
| U04bU.3 身份及调用位置 | 完成 | `TestResourceInvocationIdentity`：调用后改写 `ReviewAfterReset`、未执行闭包 `ReviewUncalledClosure`、局部遮蔽 `ReviewShadow` → 1；调用前改写 `ReviewBeforeReset`、执行闭包 `ReviewCalledClosure` → 0（跨文件：`DB` 声明在 flow.go、改写/调用在 extra.go） |
| U04bU.4 失败与预算 | 部分 | `TestResourceFailureContracts`：连接的 `from/to` 仍指向实际证据（file/line/path）；资源失效不升级。预算/`ambiguous_resource`/`snapshot_hash` 缺失路径沿用既有契约（本轮未新增夹具） |
| B03e.1 实际签名槽 | 完成 | `TestORMReadSignatureSlots`：Find 的 `err` → `read_error` 且无 `read_result`；Get 的 `err`（槽 1）→ `read_error`，bool 槽不是 `sql_read_result` |
| B03e.2 错误身份 | 完成 | `TestORMReadErrorIdentity`：直接读取（`ReviewDAO`）→ `read_error`；错误被覆盖（`ReviewOverwrite`）→ 不再是 `read_error` |
| B03e.3 集合及元素 | 完成 | `TestORMReadCollectionIdentity`：`r.Kind` → `read_column.column=kind`；返回另一集合（`ReviewWrong`）→ 无 `read_column` |
| B03e.4 模型与命名 | 部分 | 列名按模型标签解析（`TestORMReadModelIdentity` 复用 `kind`）；动态命名/链重绑/其它 ORM 继续 unknown（既有边界，本轮未新增夹具） |
| B09e.1 默认声明 | 完成 | `TestFrontendDefaultDeclarations`：`default-frontend` → `send POST /requests → SubmitHTTP`，`request_body.id={kind:parameter,parameter:0}`；缺源码/非函数默认导出 → 保留动作 + `unknown=declaration_not_found` |
| B09e.2 文件与导出身份 | 完成 | `TestFrontendExportIdentity`：命名/别名（`click`/`submitClick`）、默认导入、默认值重导出（`export {default} from './a'`）解析到准确声明；同名函数按导入文件（`/alternate`）；默认导出冲突 → `unknown` |
| B09e.3 动态及预算 | 部分 | `TestFrontendUnresolvedActions`：动态 URL → 保留动作 + `unknown`、无 `entry`；递归/深度超限沿用 `seqFrontendResolve` 深度上限（未新增夹具） |
| B09e.4 解构与字段 | 部分 | `TestFrontendBodyFieldIdentity`：`body:{id}` 按形参位置传播（正例）；`body` 来自另一对象/字段覆写（`body-frontend`）→ `request_body` 为空（不借用）。参数解构保持 unknown |
| B10e.1 图中实际动作 | 完成 | `TestWorkflowFrontendDiagram`：图里有 `click`/`submitClick` 参与者、`POST /requests` 消息与 `SubmitHTTP`；缺源码时只给 `目标未知` 说明，不伪造参与者 |
| B10e.2 同一流程事实 | 完成 | `TestWorkflowOutputConsistency`：改用 `same-name` 夹具后 JSON/组合 Markdown/SVG 同时变为 `POST /alternate`，不再出现 `/requests` |
| B10e.3 完整性及共享预算 | 部分 | 预算/续查/取消沿用集合级共享预算（todo29 契约）；本轮未新增"减少预算"夹具 |
| B10e.4 失败及渲染 | 完成 | `TestWorkflowFrontendDiagram` 断言 SVG 非空且与 Markdown 同动作；`TestWorkflowExportFailures`：运行期单项失败仍导出成功项、汇总 `exported=1 failed=1`、exit=1 |
| B11e.1 整数秒范围 | 完成 | `TestB11eTimeoutIntegerBounds*` + `TestTimeoutIntegerBounds`：`1`/`9223372036` 原值生效；`9223372037`、`20000000000`、`0`、`-5`、超 int64 拒绝（先检查范围再乘 `time.Second`） |
| B11e.2 完整 duration | 完成 | 同上：`30s`/`1h30m`/`MaxInt64ns` 生效；`3600oops`/`0s`/`-1s`/`1d`/duration 溢出拒绝；单流程与 batch 共用 `parseTimeoutExact` |
| B11e.3 执行前失败 | 完成 | `TestB11eTimeoutPreflightIsolation` + `TestTimeoutIntegerBounds`：非法 timeout → stderr 一行 `error: flow review timeout 非法 "20000000000"`、stdout 空、exit=1、不创建导出目录（校验在建目录/开库之前）；合法 `30s` 正常导出 |

## 公开示例逐条实测（逐字对照 todo34.txt）

示例目录按 todo34.txt 的 7 个源码块原样生成，`git init` + 一次
`init --workers 1 --progress none` 后执行：

```json
{"connections":1,"conditions":[],"missing":["transaction_commit"]}   # SubmitWrongError
{"connections":1,"conditions":[],"missing":["transaction_commit"]}   # SubmitReboundTx
{"connections":1,"conditions":["commit_success"],"missing":[]}       # SubmitTx（正例控制）
{"connections":1}                                                    # ReviewZeroLoop
{"connections":1}                                                    # ReviewEarlyReset
{"connections":1}                                                    # Review（正例控制）
[{"kind":"read_error","read_result":false}]                          # B03e err 槽
[{"column":"kind"}]                                                  # B03e r.Kind 列
[{"action":"send","method":"POST","path":"/requests","entry":"symbol:go:example.com/remaining:SubmitHTTP","unknown":null}]   # default-frontend
[{"action":"click",...},{"action":"submitClick",...}]                 # alias-frontend（正例控制）
{"has_click":true,"has_submit_click":true,"has_handler":true}        # B10e 组合 Markdown 图块
{"has_handler":true}                                                 # B10e 正例控制
error: flow review timeout 非法 "20000000000"（stdout 空、exit=1、无目录）  # B11e 非法
{"total":1,"exported":1,"failed":0,"files":["review.md","index.md"]} # B11e 合法 30s
```

## 本轮实现要点

- **B06e**：新增 `seqCommitSuccessVars` 数据流分析（`action_seq_workflow_resource_commit_dataflow.go`
  / `..._origins.go`）：事务变量按 Begin 调用来源绑定（含同包 helper 唯一
  return、别名传播、重绑/非事务赋值清槽）；`Commit` 错误槽必须是该次调用的
  赋值 LHS（别名沿赋值链解析，不按 `err` 前缀）；证明按**Commit 调用位置**
  记录，连接侧按写入的 `Origin.ID` 找到该事务的**所有** Commit 调用点，
  全部通过成功检查才给 `commit_success`（同一 helper 调用两次 = 两个事务）。
- **U04bU**：资源事件带可达边界（终止块的改写 `die`），`mutatedAt` 只认能
  到达读取的改写；`for false` 只执行 Init、跳过 Body/Post；Post 参与改写；
  跨文件包级 var 按声明身份/包级名称表判定。
- **B03e**：ORM 读取结果新增 `errSlot`（Find=0 / Get=1）；新增
  `SeqOriginReadError`（`read_error`）来源，沿调用槽/返回槽/别名传播；
  `seqReadResultOriginAt` 不再把 ORM 槽 0 当集合/行。
- **B09e**：新增 `export default function` 声明、默认导入与默认值重导出链
  （深度 ≤8，冲突默认导出 → unknown）；未解析事件保留动作 +
  `declaration_not_found`；重导出桥接文件（无导出函数）也能被导入解析。
- **B10e**：`workflow.frontend` 进入文本/Mermaid/PlantUML/SVG（同一份动作事实）；
  动作参与者 + 请求消息 + 请求体来源 + handler；动作顺序写"不代表真实顺序"。
- **B11e**：`parseTimeoutExact` 整串解析，整数秒在乘 `time.Second` 前检查
  `floor(MaxInt64/time.Second)`；单流程与 batch 共用。

## 明确边界（未在本轮验收）

- **真实 28 个业务流程**：仍需调用方提供实际源码后按 21—28 再其余流程验收；
  本表只交付通用机制与公开示例。
- **B06e.4**：只解析同包 helper 的唯一 `return <Begin/另一 helper>`；延迟/异步
  提交（`defer tx.Commit()`）、跨包 helper、外部源码缺失一律保留
  `transaction_commit`（未新增反例夹具）。
- **U04bU.4**：预算耗尽/`ambiguous_resource`/`analysis_not_visited` 的
  partial 报告与 `source_id`/`snapshot_hash` 一致性沿用既有实现，本轮未新增
  预算夹具。
- **B03e.4**：动态表名/链重绑/未支持框架继续 unknown；本轮只复核标签列名。
- **B09e.3/.4**：递归/深度超限的具体 `unknown` 原因未细化；参数解构
  （`function send({id})`）与字段覆写后的值传播仍为 unknown（保守）。
- **B10e.3**：共享预算/续查预算的"减少预算即 partial"未新增夹具，沿用 todo29
  的集合级预算契约。

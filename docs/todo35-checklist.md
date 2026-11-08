# todo35 交付 Checklist（输入：`todo35.txt`，基线 `origin/main` 52839b9）

规则：按编号独立验收；生产链路 **源码 → init → CLI**（公开示例
`example.com/remaining`）。测试名称与输出逐条映射；只有公开示例通过时
不得写"全部完成"，本表把未覆盖的真实业务验收单列。

## 状态总表

| 编号 | 状态 | 证据（要求 → 测试/CLI 输出） |
| --- | --- | --- |
| B06e.3 正常出口 | 完成 | switch/type-switch/select 分支里的 `return` 计入正常出口；Commit 失败分支返回 `nil` 不构成成功证明。`TestCommitAllNormalExits/positive`（SubmitTx → `commit_success`）；`/negative`（SubmitSwitchBypass、SubmitSwallowCommit → 连接保留 `transaction_commit`） |
| B06e.4 helper 返回槽 | 完成 | helper 槽 0 按局部变量/命名返回值/同包跨文件/跨包解析到同一次 Begin；冲突返回/调用点重绑不证明。`TestCommitLocalAndPackageReturns/positive`：SubmitLocalHelperTx/SubmitFileHelperTx/SubmitNamedFileTx/SubmitCrossPkgTx/SubmitCrossPkgNamedTx 均 `commit_success`；`/negative`：SubmitFileConflict/SubmitHelperRebind 无 `commit_success`，跨包冲突同样不借用 |
| U04bU.1 循环可达性 | 完成 | `break/continue/goto` 后同块语句不可达；switch/select 分支参与遍历。`TestResourceReachableLoopWrites/positive`：ReviewBreakBeforeReset/ReviewContinueReset/ReviewLabeledBreak/ReviewLabeledContinue → 1；`/negative`：ReviewLoopOnce（改写发生在 break 前）→ 0 |
| U04bU.2 panic 终止 | 完成 | 未遮蔽的内建 `panic` 使该分支终止（`die` 边界）；局部同名函数遮蔽后不终止。`TestResourcePanicTermination/positive`：ReviewPanicReset/ReviewEarlyReset → 1；`/negative`：ReviewShadowPanicReset/ReviewPanicNoTerminate → 0 |
| U04bU.3 指针参数 | 完成 | 跨包 `Clear(&DB)` 的 `*handle=nil` 按调用实参回到原资源；只读/改其它资源/读取之后不影响。`TestResourcePointerParameterWrites/positive`：ReviewPointerPeek/ReviewPointerOtherReset/ReviewPointerAfterReset → 1；`/negative`：ReviewPointerReset → 0 |
| U04bU.4 预算 fail-closed | 完成 | 深度/调用预算超限即 `partial` 且不再用旧资源状态生成连接；预算内短链/11 层公开链访问到真实改写。`TestResourceBudgetFailClosed/positive`：ReviewSafeChain → 1 且非 partial、ReviewShallowReset → 0 且无残留预算原因、ReviewDeepReset → 0；`/negative`：ReviewBudgetReset（40 层）→ 0、`partial=true`、`instance_boundaries` 含 `resource_analysis_budget` |
| B03e.4 静态表名 | 完成 | ORM 读取按实际模型接收者的静态 `TableName()` 求表；动态/未知链修饰器/无方法模型不借用。`TestORMStaticModelTableIdentity/positive`：ORMNamedFind → 1、`table=nodes`；`/negative`：ORMNamedFindOther/Dynamic/NoName/Alias → 0。`TestORMModelIdentityVariants/positive` 保留 `kind` 列来源；`/negative` Alias 链 0 |
| B09e.3 有界声明解析 | 完成 | 包装链共享 16 层声明预算（不是示例层数）；递归/超预算/同名不同文件/缺源码保留真实动作 + 明确 unknown。`TestFrontendBoundedWrapperResolution/positive`：deep-frontend `click POST /requests → SubmitHTTP`；`/negative`：cycle-frontend=`declaration_cycle`、budget-frontend=`declaration_budget_exhausted`、same-name=`/alternate` |
| B09e.4 字段/解构来源 | 完成 | `req.id` 与 `{id}`/解构别名/嵌套字段输出 `field_read` + `parameter` + `path`；另一对象/字段覆盖/动态下标保持 unknown。`TestFrontendFieldAndDestructureOrigins/positive`：field-frontend/destructure-frontend/alias-field/nested-field；`/negative`：other-field/mutated-field/index-field 均 unknown |
| B10e.3 覆盖完整性 | 完成 | `workflow.coverage` 以场景 `partial` 为准，complete/partial 互斥；不按组合重置。`TestWorkflowCoverageScenarioConsistency/positive`：depth=1 时 `supported=2 analyzed=2 complete=0 partial=2`、两个场景 partial；`/negative`：complete 不得仍为 2 |
| B10e.4 场景取消 | 完成 | 场景阶段取消传播到单查询与 batch；单查询 stdout 空、exit=1、stderr `error: context canceled`；batch 停止后续流程调度。`TestWorkflowScenarioCancellation/positive` + `/negative`（关闭钩子后正常成功；取消后不导出 `second.md`、不报 exported=1） |
| B11e.3 timeout 存在性 | 完成 | manifest 用保留存在性的原始值解码：缺省走默认值；显式 `""`/null/类型错误/零/负值/后缀/溢出在执行前拒绝（stdout 空、exit=1、不建目录）。CLI 裸 `--timeout`/`--timeout=`/后接其它选项 exit=2、开库前拒绝。`TestTimeoutPresencePreflight/manifest_positive|negative` + `/cli_positive|negative` |

## 公开示例逐条实测（逐字对照 todo35.txt）

示例目录按 todo35.txt 源码原样生成，`git init` + `init --workers 1
--progress none` 后执行：

```json
{"connections":1,"conditions":[],"missing":["transaction_commit"]}   # SubmitSwitchBypass
{"connections":1,"conditions":[],"missing":["transaction_commit"]}   # SubmitSwallowCommit
{"connections":1,"conditions":["commit_success"],"missing":[]}       # SubmitLocalHelperTx
{"connections":1,"conditions":["commit_success"],"missing":[]}       # SubmitTx
{"connections":1}                                                    # ReviewBreakBeforeReset
{"connections":1}                                                    # ReviewPanicReset
{"connections":0}                                                    # ReviewPointerReset
{"connections":0}                                                    # ReviewDeepReset
{"connections":1}                                                    # Review
{"connections":1}                                                    # ORMUpdate→ORMNamedFind
[{"action":"click","method":"POST","path":"/requests","entry":"symbol:go:example.com/remaining:SubmitHTTP","unknown":null,"body":{"id":{"kind":"parameter","parameter":0}}}]        # deep-frontend
[{"action":"send","method":"POST","path":"/requests","entry":"symbol:go:example.com/remaining:SubmitHTTP","unknown":null,"body":{"id":{"kind":"field_read","parameter":0,"path":["id"]}}}] # field-frontend
[{"action":"send","method":"POST","path":"/requests","entry":"symbol:go:example.com/remaining:SubmitHTTP","unknown":null,"body":{"id":{"kind":"field_read","parameter":0,"path":["id"]}}}] # destructure-frontend
{"supported":2,"analyzed":2,"complete":0,"partial":2,"scenario_partial":[true,true]} # B10e
error: flow review timeout 非法 ""（stdout 空、exit=1、无目录）             # B11e manifest ""
error: --timeout 非法（缺少值）（stdout 空、exit=2）                        # B11e 裸 --timeout
```

## 本轮实现要点

- **B06e.3**：`seqCommitAnalyzer.walkStmt` 增加 CaseClause/CommClause；
  Commit 守卫要求失败分支真的传播错误（`return nil`、裸 return 但命名返回值
  不是该错误槽 → 不证明）。
- **B06e.4**：`txCallOrigin` 递归解析同包/跨包 helper 的真实返回槽；
  `seqBareReturnOrigins` 补事务槽；`seqWFResolveTxResource` 对跨包 Begin
  的形参接收者按父调用实参回到入口包级 DB 声明。
- **U04bU**：块遍历在 return/branch/panic 后停止；switch/select 分支体参与；
  `seqStmtTerminates` 识别未遮蔽的内建 panic；指针形参调用帧把 `*handle=nil`
  映射到 `&DB`；资源深度/调用预算超限 → partial + 连接 fail-closed。
- **B03e.4**：`seqORMStaticModelTable` 按模型类型接收者解析唯一静态
  `TableName()`；动态返回/同名其它类型/未知链修饰器不借用；显式 Table 优先。
- **B09e.3/.4**：函数体截取先跨过参数列表（解构参数不再被当函数体）；
  形参模式解析对象解构/别名/嵌套；请求体来源新增 `field_read` + `path`；
  包装链共享 16 层预算并区分 recursion/budget/not-found 原因。
- **B10e**：覆盖统计先看场景自身 partial；`SeqScenarioStageHook` 为确定性
  取消回归提供测试注入点（生产 nil）；取消错误从场景阶段一路返回到查询/
  batch，batch 遇到取消停止后续调度。
- **B11e**：`seqBatchTimeout` 保留原始 JSON 值（缺省/`""`/null/类型错误可
  区分）；CLI `--timeout` 缺值不再被后续 flag 静默吞掉。

## 明确边界（未在本轮验收）

- **真实 28 个业务流程**：仍需实际业务源码按 21—28、再其余流程验收；
  本表只交付通用机制与公开示例。
- **B06e.4 跨包冲突返回**：冲突本身不证明 `commit_success`；若事务资源
  身份因分支不同而无法确定，连接可能不生成（比"保留 transaction_commit"
  更强，不借用任一分支）。测试只断言不得获得成功证明。
- **U04bU.4 预算数值**：深度 32 / 调用 4096 是当前实现上限；超限一律
  fail-closed。没有暴露用户可调预算参数（提高预算需改常量/复用完整事实）。
- **B03e.4 命名求值预算**：TableName 扫描 64 文件上限；超限按 unknown，
  不猜默认表名。未支持 ORM 框架仍不进入本路径。
- **B09e.3 预算**：共享 16 层声明预算；没有按动作独立重置。预算耗尽/
  递归只给 unknown 原因，不展开部分链。
- **B10e.4 取消注入点**：`action.SeqScenarioStageHook` 是测试钩子；生产
  取消仍来自 ctx/timeout。真实长场景的取消延迟未单独度量。
- **B11e.3**：timeout 缺省（字段不出现）走既有默认值；显式空值/非法值
  全部拒绝，数值范围契约沿用 todo33/34。

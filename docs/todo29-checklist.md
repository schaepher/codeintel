# todo29 交付 Checklist（输入：`todo29.txt`，基线 `origin/main` bb4b824）

规则：按编号独立验收；未完成项保留原因与剩余范围，不用"主要功能完成"代表整项。
生产链路一律 **源码 → init → CLI**（除明确允许的存储/解析单测）。

## 状态总表

| # | 事项 | 状态 | 证据 / 剩余范围 |
| --- | --- | --- | --- |
| T09b | 具体注册参数的 RPC 源码续接 | **已完成** | 注册 helper 声明证明（第二参 `<X>Handler` + 真实 `NewHandler(&wrapper{h})`/直传形参）；具体实参类型必须 implements；`wire:Ledger` 服务身份 + `(Handler).Submit`；`integration/todo29_t09b*_test.go`（depth20 / depth1+continue32：server=1、Save=1、Cleanup=1(defer)、Register 位置、工厂/调用顺序；删除真实注册/实参不实现 → 不沿用路由）；旧类型断言回退（`todo27_t09`）保持 |
| T12b | 统一为全请求追加次数预算 | **已完成** | `seqContinueSegments` 每个前沿先占 1 次预算（FIFO）；`meta.continuation.expanded` 与 extra/used 同口径；identity_changed/预算停止不计追加；外部依赖 depth 前沿也续查；`action_seq_t12b_budget_test.go`（预算1/2 实际节点与账本）+ `integration/todo29_runtime_test.go::TestU29T12bBudgetCLI`（真实 init→CLI，1/1/1/3 与 2/2/2/3） |
| T14b | 取址逃逸持续失效及已解析输入兼容 | **已完成** | `rtState.escaped`/`rtGlobal.escapedAt`（位置判定、克隆保留）；`&n`/`&(n)`/`&obj.Field`/`&slice[0]`/包级变量持续失效；已建模 Scan 输出仍成功/失败相关；`RuntimeData any` 接受路径或 `*RuntimeSnapshot`（typed-nil=未配置，冲突单前缀错误）；`action_seq_t14b_escape_test.go` + `TestU29T14bEscapeCLI` |
| T19b | 最接近有效匹配的运行时缺口 | **已完成** | 缺口优先级 scope > columns > rows > observation（顺序无关）；多条有效 → ambiguous；相同规范化 scope 的观测保留（读取端不拼接）——U01 加载拒绝按 T19b 调整；`action_seq_t19b_test.go`（换序/有效/重复）+ `TestU29T19GapCLI` |
| T19c | 无 Context 的 SQL 读取参数位置 | **已完成** | `runtimeQuerySQLArg`（QueryRow/Query 实参 0；Context 版本 1）；接收者仍须是已证明 `database/sql`；Entry/ContextEntry 同一 `scope_not_covered`（`TestU29T19GapCLI`） |
| U04x | 整值复制的别名/覆盖/逐项调用语义 | **已完成** | 分配对象根 + 别名共享（复制前/后）；未知覆写/通过别名覆写/逃逸/条件分支使来源失效；`*alias = *Build("second")` 新来源；函数列表最终祖先 second；`integration/todo29_u04x_test.go`（Entry/AliasBefore/AliasAfter/Unknown/ThroughAlias/Escape/Conditional/Repeated） |
| U04k | 包装函数返回槽传播与冲突失效 | **已完成** | 续查后后序重算 return 槽来源（`return Save()`）；冲突写入不取第一条；命名返回值保留；取址/defer 改写失效；`integration/todo29_u04k_test.go`（ConflictingEntry write_condition=0；Nested depth1/12 = 1/1；Named=1；Escaped/Deferred=0） |
| U04y | 字段写入求值顺序与历史对象来源 | **已完成** | 整值复制后的字段写入（copy.ID=id → parameter；r.ID=copy.ID → field_read）；`SeqFieldRoot` 支持 parameter_object/struct_literal/struct_copy 并保留 `type_id`/`id`；`+=` 只记右操作数；内建转换保留实参来源；`integration/todo29_u04y_test.go` |
| U04 | 跨入口同记录/成功写入/多次 Finish 整合 | **部分** | 已验收 Submit 单入口树的持久化边界（`integration/todo29_u04_test.go`：INSERT 边界可见，不凭静态源码加入 FinishReview/Complete）。**剩余**：通用入口整合机制（记录键/过滤/成功路径/请求身份，跨入口顺序与同记录传参证明、第二次 FinishReview、外部同步非第三次人工 Finish 等）尚未实现；条件化 Mermaid 输出契约未落地。见 `docs/field_trace.md §187`。 |

## 本轮已执行验证

```sh
scripts/verify.sh                 # 全量 -race 14 包基线：OK（exit=0）
scripts/verify.sh --quick         # 提交前：OK
make it / go test -tags integration ./integration -count=1   # 全量：ok（110s，无 FAIL/skip）
```

- 跳过：`make e2e`（本轮无前端改动；todo29 未要求）。
- 说明：新增/修改的集成回归均在 `integration/todo29_*_test.go`；T19b 的
  "重复 scope" 语义从加载拒绝改为读取端 ambiguous（U01 安全目标保留：不拼接）。

## 已知边界（本轮有意保留）

- U04 跨入口条件图未实现（没有请求顺序/同记录传参/第二次 Finish 的证明，
  不把静态调用边升级成跨入口因果）。
- 运行时逃逸不实现指针别名值流：取址后一律 unknown（两分支），不精确求
  `mutate` 后的值。
- T12b 预算是追加次数，不是节点数/内存/字节上限；单段仍可能包含多个分支。
- `runtime-data` 对象入口不能进入 `argv` 重放（无路径时 argv 省略该参数，
  但 `meta.evidence.runtime_hash` 保留快照身份）。

# todo14 验收 Checklist（逐项状态与证据）

执行日期：2026-11-01。约定：R=要求，A=验收；状态 = 已完成 / 部分完成 / 未开始。
**未完成项保留在末尾。**

## 验证入口

```shell
scripts/verify.sh --quick
go test -tags integration ./integration/ -run 'TestTodo14|TestTodo13'
scripts/verify.sh            # 全量 -race（合入前）
```

## 6. P1 持久化/消息边界的同记录身份 — 已完成（当前夹具范围）

| 项 | 状态 | 证据 |
| --- | --- | --- |
| R1/A1 同记录可证明夹具：写入键→消息键→读取过滤键 | 完成 | `Publish`（RecordID="JOB-42" → Send("jobs.created", key)）↔ `Consume`（Receive + `key == "JOB-42"`）→ 4 跳全 corroborated、`identity_partial=false`；`TestTodo14AsyncBoundaryIdentityRealIndex` |
| 逐跳真实文件/行号/键/传递依据 | 完成 | write（赋值行）、persistence_or_queue（Send 调用行+主题+键）、read（Receive 调用行）、filter（比较行）；JSON `chain.boundary` |
| R2/A2 键不一致 | 完成 | 同主题 `JOB-42` vs `JOB-99` → `boundary_key_mismatch` + 两个值/位置，不升级；`TestTodo14AsyncBoundaryMismatchRealIndex` |
| R2/A2 互斥分支不同键 | 完成 | `JOB-A`/`JOB-B` → `boundary_key_ambiguous`，不串成一次生命周期；`TestTodo14AsyncBoundaryBranchedAmbiguous` |
| R2 同名字段/callee@line 指纹不冒充同记录 | 完成 | 无边界证据时不升级；内存/未知边界保留 `persistence_or_queue` unverified + 写入侧位置 |
| A4 identity_partial 与具体边界诊断独立 | 完成 | `identity_partial` + `boundary_*` 诊断分开；范围修正（todo13-5）不影响本判定 |

**边界**：静态证明限定「记录键为字面量、同一消息主题/API 族、读取侧以同一字面量过滤」；非字面量键/动态主题仍 unverified（不声称通用持久化证明）。

## 8. P1 函数级错误汇总不再依赖变量名 — 已完成

| 项 | 状态 | 证据 |
| --- | --- | --- |
| R3 错误变量按名字/类型/构造识别 | 完成 | `isErrorVarAssignment`；`FailureName`/`ErrName` 同结论 |
| A2/A3 汇总与路径一致（含覆盖位置/verdict） | 完成 | 路径 `swallowed` + summary `suspicious=覆盖行`、`unverified`；`TestTodo14ErrorSummaryNameIndependent` |
| A2 无目标与返回槽目标一致 | 完成 | `--target-return #0` 同结论（`targetResultSummary` 聚合 suspicious） |

## 14. P1 结构化 argv 可直接执行 — 已完成

| 项 | 状态 | 证据 |
| --- | --- | --- |
| R1 argv 为原始参数（无 shell 引号） | 完成 | `seqEvidenceArgv` 不再调用 shellQuote；空格/括号/#1 原样 |
| A2 进程 API 直接执行 argv[1:]（非模块目录） | 完成 | 真实二进制 exit=0 且语义一致；`TestTodo13EvidenceIdentityRealCLI` + `todo13_identity_argv_test.go` |
| A2 展示命令仍正确 quoting | 完成 | `seqEvidenceDisplayCommand`；bash/zsh 重放通过 |
| 保留多 scene/focus/输出选项/身份比对 | 完成 | 既有测试保持 |

## 17. P1 统计口径/逐阶段取消/大仓验收 — 已完成（剩余见末尾）

| 项 | 状态 | 证据 |
| --- | --- | --- |
| R1 实际次数/耗时（符号/条件/SQL/派发） | 完成 | `TraceStats`：SQLQueries=GetLatest+CTE、SymbolLookups=实际 GetSymbol、ConditionRows/MS、DispatchCalls/MS；`TestTraceStatsActualCounts` |
| R1 覆盖口径显式 | 完成 | `covered_ops`/`uncovered_ops`；sequence `stats_scope`/`uncovered_ops`；不把行数/入口数当底层次数 |
| R2/A3 缓存边界与重建失效 | 完成 | 容量上限 128 + FIFO 淘汰（`cache_entries/evictions`）；build_id 变化清空；`TestTraceCacheBoundedAndInvalidated` |
| R3/A3 逐阶段取消（条件/派发） | 完成 | `TraceConditionsContext`/`markDispatchCandidatesContext` 逐行检查；测试钩子分别在 after_sql/after_conditions/after_dispatch 取消 → `ErrTraceCanceled`；`TestTraceCancelEachStage` |
| A1/A2 固定大仓矩阵/并发 | 完成 | depth=1/2、focus、未知回调/接口无实现、少结果 value-trace；4×2 并发 p50/max 记录；`TestTodo14MatrixConcurrencyAndStopDelay` |
| A3 停止延迟/资源峰值 | 完成 | sequence 1µs ≈2.3ms、value-trace ≈1.3ms（<2s 宽限）；VmHWM before/after 记录 |

---

## 未完成项汇总

1. **任务 17-R2 剩余**：重复工作减少的 before/after A/B 对照（当前用
   cache 命中数证明避免重复解析，未做同条件关闭缓存的耗时对比）。
2. **任务 6 范围**：非字面量记录键/动态主题/跨表 join 的静态证明仍不做，
   保持 unverified（未声称通用持久化身份能力）。

# todo15 验收 Checklist（逐项状态与证据）

执行日期：2026-11-01。R=要求，A=验收；状态 = 已完成 / 部分完成 / 未开始。

## 6. P1 消息键/过滤路径按实际值身份 — 已完成

| 项 | 状态 | 证据 |
| --- | --- | --- |
| R1/A1 实际写入实例→发送实参→边界 API/主题→接收返回值→过滤谓词 | 完成 | `findSend` 取实际 Send 实参（字段→同实例字面量/直接字面量/变量字面量）；`findRecv` 绑定 Receive 返回值，只认该键参与的 `==/!=`；读取必须位于对应分支；`TestTodo14AsyncBoundaryIdentityRealIndex`（正例） |
| R1 `!=` 分支读取不升级 | 完成 | `boundary_condition_mismatch` + `Boundary.Verdict=excluded`；`TestTodo15BoundaryNotEqualBranchExcluded` |
| R1/A1 实际发送键优先于字段定义 | 完成 | Send 字面量 JOB-99 vs 字段 JOB-42 → `Boundary.Key=JOB-99`、`boundary_key_mismatch`；`TestTodo15BoundaryActualSendKeyWins` |
| R2 无关变量比较 | 完成 | 只认 Receive 返回键；`TestTodo15BoundaryUnrelatedVariable`（缺值依赖诊断） |
| R2 无关调用中的相同常量 | 完成 | 不再从任意调用实参收集过滤字面量；`TestTodo15BoundaryUnrelatedCallConstant`（按真实过滤 JOB-7 报不一致） |
| R2 同字段不同实例 | 完成 | 按实例名取字面量；发送 b.RecordID=JOB-B vs 过滤 JOB-A → mismatch；`TestTodo15BoundaryDifferentInstances` |
| R2 同调用位置不同实参/多次发送 | 完成 | 多 Send 不同键 → `boundary_key_ambiguous`；`TestTodo15BoundaryMultipleSendsAmbiguous` |
| R2 互斥分支多字面量 | 完成 | `boundary_key_ambiguous`（含 JOB-A/JOB-B）；`TestTodo14AsyncBoundaryBranchedAmbiguous` |
| R2 发送键与载荷字段不同 | 完成 | `findSend` 校验键字段实例 == 载荷实例，否则 unverified |
| A4 identity_partial 与具体缺口独立 | 完成 | 证明失败保持 `identity_partial=true`；能证明不匹配时 `excluded/mismatch` 并保留实际值/位置 |
| 同主题/同实例/同键且读取在相等分支的正例 | 完成 | todo14 四跳 corroborated、`identity_partial=false` 保持 |

**范围**：静态证明仍限「键为字面量 + 同主题/API 包身份 + 读取位于相等分支」；
动态键/跨系统 API 保持 unverified。

## 17. P1 重复工作 A/B 与大仓资源/覆盖 — 已完成（保留说明）

| 项 | 状态 | 证据 |
| --- | --- | --- |
| R2/A1 同源/索引/配置/负载缓存开/关 A/B | 完成 | `TestValueTraceCacheAB`：n=6，cache-off wall 7.2ms/sql12/sym6/miss6；cache-on 1.4ms/sql7/sym1/hit5；结果行一致 |
| A1 矩阵语义/诊断与覆盖 | 完成 | `TestTodo15MatrixSemantics`：depth1 vs2 节点数、Use→Impl.Do、CB unresolved；`TestTodo15ValueTraceMatrixAndConcurrency`：fixture 2212 节点/1706 边、unknown/simple value-trace 行数/阶段计数 |
| A2 冷暖/并发 | 完成 | value-trace 冷 sql2/cond2/dispatch1、暖 hit1；4×2 并发独立连接 p50=35ms/max=40ms，行数一致 |
| A3 真实峰值采样 | 完成 | todo16 收尾：探测 GNU time -v / BSD time -l；Linux 27MB；不支持时明确 unavailable（不把 0 当成功） |
| A3 进入工作后的取消 | 完成 | todo16 拆为独立测试 `TestTodo15CancelAfterWork`：5ms 预算在 SQL 后取消（`sql_queries=2`）、delay≈7ms；RSS 不可用不阻断 |
| 2s 预算宽限 | 完成 | 停止延迟远小于 2s；未使用外层 180s |

**说明**：A/B 夹具为单节点字段访问固定索引（用于隔离重复 SQL/符号查找），
不代表大仓绝对耗时；大仓矩阵样本数见测试日志。

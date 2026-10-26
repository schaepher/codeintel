# todo13 验收 Checklist（逐项状态与证据）

执行日期：2026-11-01。约定：R=要求，A=验收；状态 = 已完成 / 部分完成 / 未开始。
**未完成项保留在末尾，不因相关测试通过而删除。**

## 验证入口

```shell
scripts/verify.sh --quick                          # build + vet + 非 race 全量单测
go test -tags integration ./integration/ -run TestTodo13   # 真实索引 → CLI
scripts/verify.sh                                  # 全量 -race 基线（合入前）
```

---

## 3. P0 未知调用改写后场景不使用旧字面量 — 已完成

| 项 | 状态 | 证据 |
| --- | --- | --- |
| R3 取址/别名传入调用使旧定义失效 | 完成 | `applyCallInvalidation` + `litVal.alias`；`TestSceneRewriteByAddressedCall` / `TestSceneRewriteByAliasedCall` |
| R3 深度/扫描上限不静默保留 | 完成 | `litScopes.limited` + `unknown:analysis-depth-limit`；`TestSceneRewriteDepthLimitUnknown` |
| A3 两分支保留 + partial 原因/位置 | 完成 | `SceneFit.Cause/Source`、`meta.scene.partial/causes`；`TestTodo13SceneRewriteRealIndex`（JSON/文本） |
| A3 两层传参来源真实行号（无 def@L0） | 完成 | `litVal.src` 链；`TestSceneRewriteTwoLayerSourceLine` + 集成同名断言 |

## 4. P1 未知实现 Mermaid 保留完整候选 — 已完成

| 项 | 状态 | 证据 |
| --- | --- | --- |
| R2 图内明示未消歧 + 完整候选/依据 | 完成 | 参与者“实现未消歧”+ Note 候选；`TestSeqMermaidUnknownImplCandidates` |
| R2 不展开首个实现 | 完成 | `ImplType=""`、无子节点；同上 |
| A2 JSON/文本/图同一候选身份 | 完成 | `TestTodo13ImplCandidatesRealIndex`（候选逐一对应） |
| A2 指定场景仍唯一选择 | 完成 | 同测试 US 场景 |

## 5. P0 无字段摘要 seed 不退回全包候选 — 已完成

| 项 | 状态 | 证据 |
| --- | --- | --- |
| R1/R2 候选 = seed 调用/值身份；保留真实邻居 | 完成 | `directCalleesOf`/`directCallersOf`；`TestAsyncNoSummarySeedUsesCallIdentity` |
| R3 预算/稳定排序/partial 保持 | 完成 | 原有预算测试 + `candidate_basis`；`TestTodo13AsyncNoSummaryLargeRealIndex` |
| R3 大反例不纳入 Unrelated、体积对照 | 完成 | 2000 同包无关类型：1266B、`candidate_total=1`、2 节点/1 关系（原 ~6.7MB） |
| R4 范围修正不提升因果等级 | 完成 | Entry 对照 2 节点/1 关系、`unverified` |

## 6. P1 跨入口身份闭环与注册证据 — 已完成（todo14 收尾）

| 项 | 状态 | 证据 / 缺口 |
| --- | --- | --- |
| R3 角色依据来自真实注册/绑定 | 完成 | `passes_to` + 注册 API 调用点（文件:行）；`TestAsyncRegistrationEvidenceIsRealBinding`、`TestTodo13AsyncRoleEvidenceRealIndex` |
| R3 名称启发式低等级；ScheduleLookalike 不升级 | 完成 | `asyncRegistrationIndex` 不再看调用方名；同名测试 |
| R3 人工/自动共存不推断次数 | 完成 | 既有 `TestAsyncDualAdvance` 保持 |
| R1 持久化/队列边界 → 同一记录证明 | 完成 | todo14：字面量记录键+同主题+读取同键过滤 → 4 跳 corroborated、`identity_partial=false`（`todo14-checklist.md` 任务 6） |
| R2 同名不同源/同表不同记录/互斥分支 | 部分 | 同名不同源已测（`value_origin`/`name_only`）；其余仍靠 unverified + `persistence_boundary_unverified` 诊断 |
| A1 完整持久化/消息夹具 CLI 验收 | 完成 | `integration/todo14_boundary_test.go`（同键/不一致/互斥分支） |

## 7. P1 截断诊断补预算值/位置/前沿 — 已完成

| 项 | 状态 | 证据 |
| --- | --- | --- |
| R2 已用/上限、停止位置、未展开范围 | 完成 | `EffectsPathBudget`（used/limit/stop_file/stop_line/scope/frontier） |
| R2 前沿 ≠ 遗漏终态数（总数 unknown） | 完成 | `extra_terminals=unknown`；`TestEffectsPathBudgetReport`、`TestEffectsPathBudgetCLI` |
| 保留 partial/提高预算/31 条终态 | 完成 | 高预算 complete + `returns=[30 true]`；差 7 条对照 |

## 8. P1 return 表达式被调写入 + 错误来源值身份 — 已完成

| 项 | 状态 | 证据 |
| --- | --- | --- |
| R1/A1 return 表达式内调用收集写点 | 完成 | ReturnStmt `collectCallSites`；`TestEffectsReturnExpressionCalleeWrites`（writes 0/1/2） |
| R3/A2 错误来源不靠变量名 | 完成 | 错误构造/末槽/赋值顺序；`TestEffectsErrorSourceByValueIdentity`（改名一致） |
| A3 生产索引 CLI（无目标/字段/返回槽） | 完成 | `TestTodo13EffectsReturnCalleeRealIndex` |
| 保留 kind/commit_scope/未知调用 partial | 完成 | 既有测试保持 |

## 9. P1 return 焦点保留跨函数别名写入 — 已完成

| 项 | 状态 | 证据 |
| --- | --- | --- |
| R1/R2 沿别名/实例保留被调写入 | 完成 | `markReturnAliasDeps`；`TestSeqFocusReturnAliasWrites` |
| R2 排除无关 second 写入 | 完成 | 同名测试（焦点仅 1 次 AssignStatus） |
| A2 生产索引 CLI 验收 | 完成 | `TestTodo13FocusReturnAliasRealIndex` |

## 10. P1 未知回调值流不报 complete — 已完成

| 项 | 状态 | 证据 |
| --- | --- | --- |
| R2/A2 未知回调 → partial + 原因/位置 | 完成 | ssa 节点 `call_unknown/call_reason/call_line/call_file`；`TestUnknownCallbackMarkedOnCallResult`、`TestTodo13ValueTraceUnknownCallbackRealIndex` |
| R2 低深度仍标 depth（不混同） | 完成 | 集成测试 `--max-depth 1` |
| 保留常量/phi/跨层值流 | 完成 | Apply 对照追到 applied + complete |

## 13. P1 外部/未知调用分级与降级覆盖 — 已完成

| 项 | 状态 | 证据 |
| --- | --- | --- |
| R1/A1 正常叶子/外部/未知实现/索引缺失可区分 | 完成 | `external_target` / `unknown_implementation` / `index_gap` / `no_calls`；`TestSeqCallClassification`、`TestTodo13CallClassificationRealIndex` |
| R3/A3 具体失败包/文件/错误位置 | 完成 | ssa `error_packages` → `coverage.error_packages` + `degraded_package` |
| R3 目标相关覆盖与隔离说明 | 完成 | `target_coverage`（在/不在失败列表 + 不绝对隔离）；`TestSeqCoverageDiagnostics` |
| 纯 SQL 降级单独诊断 | 完成 | `sql_degraded`（与包失败分开） |
| 保留 focus/scene 投影分离 | 完成 | 既有诊断顺序不变 |

## 14. P1 证据 argv/配置/构建身份 — 已完成

| 项 | 状态 | 证据 |
| --- | --- | --- |
| R1 结构化 argv + 完整规范化请求 | 完成 | `SeqEvidenceRequest`（含 evidence/format/out/filter/有效 stop_packages）；`TestSeqEvidenceIdentityAndCompare` |
| R2 CLI/分析器版本 + 索引构建身份 + 配置身份 | 完成 | `cli_version/analyzer_version/build_id/index_sha/config_hash` |
| R2 同 SHA 重建/配置变化明确报告 | 完成 | `--verify-evidence`；`TestSeqEvidenceIdentityCLI`、`TestTodo13EvidenceIdentityRealCLI` |
| A2 非模块目录原始 argv（空格/括号/#1/输出） | 完成 | 真实二进制 sh/zsh 重放（集成测试） |

## 15. P0 跨仓候选遵守 workspace + 归属恢复 — 已完成

| 项 | 状态 | 证据 |
| --- | --- | --- |
| R3 候选先按 workspace/worktree 限定 | 完成 | `extChainCandidateRepos` 默认 workspace 范围；`TestExtChainCandidateWorkspaceScope` |
| R3 显式扩展可区分 | 完成 | `--all-repos` + `scope/scope_reason/candidate_repos` |
| R2 既有 worktree 官方认领/恢复 | 完成 | `workspace init --dir --repo <main…>`（git worktree 元数据证明；主仓无需先 init）；`TestWorkspaceRecoveryUnregisteredMains` |
| A2 无法证明归属明确失败 | 完成 | `TestWorkspaceRecoveryRejectsUnprovable` |

## 16. P0 不同 proto 包同名服务消歧 — 已完成

| 项 | 状态 | 证据 |
| --- | --- | --- |
| A2/R2 完整服务身份消歧（ci/other） | 完成 | 精确 `service_id` 匹配（proto-identity）；`TestExtChainProtoIdentityDisambiguates` |
| A2 交换顺序/双向查询不变 | 完成 | 同测试 4 组（a/b × 正反候选顺序） |
| A2 缺完整身份 → 候选 + partial | 完成 | `service_identity_ambiguous` + 候选清单；`TestExtChainProtoIdentityAmbiguous` |
| 真实索引跨仓验收 | 完成 | `TestTodo13ProtoIdentityRealIndex`（三仓、真实 init） |
| Submit/Cancel 不互连 | 完成 | 集成测试 CancelA 无 Submit 下游 |

## 17. P1 查询成本与预算 — 已完成（A/B 对照见 todo14）

| 项 | 状态 | 证据 / 缺口 |
| --- | --- | --- |
| R1 阶段次数/耗时（符号/签名/SQL/派发/缓存） | 完成 | `SeqTiming`/`[seq.stage]`；`analysis.sql_* / symbol_* / cache_*`；`TestSeqStageCounters` |
| R2 重复解析优化 + 缓存按索引身份失效 | 完成 | 源码按文件缓存；value-trace 结果缓存键含 `build_id`；`TestValueTraceBudgetStatsAndCache` |
| R3 两类查询可取消/预算 | 完成 | `--timeout` 均支持；1µs 非零退出；`TestTodo13QueryBudgetLargeRealIndex` |
| A1 固定大仓冷/暖分布与覆盖 | 完成 | 9018 节点夹具 3 次 13.5–16ms；todo14 追加并发分布（p50 9ms/max 10ms） |
| A2 depth/focus/未知密集/派发/少结果对照 | 完成 | `TestTodo14MatrixConcurrencyAndStopDelay` 覆盖 depth/focus/未知回调/接口无实现/少结果 |
| A3 取消后资源峰值采样 | 完成 | 1µs 停止延迟≈2.3ms/1.3ms + VmHWM 前后采样（`build_perf.md`） |

---

## 未完成项汇总（接手先看）

1. **任务 17-R2 剩余**：关闭缓存的 before/after 重复工作耗时 A/B（当前以
   命中数证明避免重复解析）。
2. **任务 6 范围**：非字面量记录键/动态主题仍 unverified（未声称通用持久化
   身份能力；见 todo14 checklist 末尾）。

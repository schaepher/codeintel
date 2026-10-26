# todo12 验收 Checklist（逐项状态与证据）

执行日期：2026-10-29。约定：R=要求，A=验收；状态 = 已完成 / 部分完成 / 未开始。
**未完成项保留在本清单末尾，不因相关测试通过而删除。**

## 验证入口

```shell
scripts/verify.sh --quick                         # build + vet + 非 race 全量单测（通过）
go test -tags integration ./integration/ -run TestTodo12   # 真实索引 → CLI（通过）
```

测试命名：单测 `internal/action/*_test.go`、`internal/cli/*_test.go`；
真实索引用例 `integration/todo12_*_test.go`（`make it` 环境，需 scip-go）。

---

## 1. P1 按目标状态字段或返回字段分析副作用 — 已完成（commit 3453751）

| 项 | 状态 | 证据 |
| --- | --- | --- |
| R1 互斥 `--target-field` / `--target-return` | 完成 | `EffectsRequest.TargetSet/TargetField/TargetReturn`；`TestEffectsTargetErrors` |
| R2 类型身份解析、空值/不存在/歧义/越界报错 | 完成 | `resolveEffectsTarget`/`resolveStructNode`/`parseReturnSlot`；`TestEffectsTargetErrors`、`TestEffectsTargetOtherTypeNoWrite` |
| R3 值流/字段摘要依赖、别名与被调写入、未知保守保留 | 完成 | 路径依赖 + `calleeWrites` 归属；`TestEffectsTargetFieldFilters`、`TestEffectsTargetCalleeWrite`、`TestEffectsTargetUnknownTypePartial` |
| R4 条件/位置/原返回槽/场景可达性/提前返回 | 完成 | `TestEffectsTargetReturnSlots`、`TestEffectsTargetSceneReachability`、`TestTodo12EffectsTargetFieldRealIndex` |
| R5 文本/JSON 同步筛选 + analysis.status | 完成 | `query_effects.go` 渲染；`TestTodo12EffectsTargetFieldRealIndex`（text/JSON） |
| R6 无目标保持既有契约 | 完成 | `TestEffectsNoTargetKeepsContract`、`TestTodo12EffectsTargetFieldRealIndex`（A9） |
| A1/A2/A3/A4/A5/A6/A7/A8/A9 | 完成 | 上述测试 + `integration/todo12_effects_test.go` |

## 2. P0 跨仓续接期间保持目标数据库可用 — 已完成（commit a1bac98）

| 项 | 状态 | 证据 |
| --- | --- | --- |
| R1 目标视图所有权到递归结束，关闭一次/不泄漏 | 完成 | `remoteTarget.release` + 调用方在递归后关闭；`TestExtChainTargetViewLifetime`（open/close=1/1） |
| R2 读取失败诊断 + partial；合法无下游分开 | 完成 | `ExtStopQueryFailed`、`ProbeTarget`；`TestExtChainTargetQueryFailure`、`TestExtChainLegalNoDownstream` |
| R3 逐 RPC 方法身份不串线 | 完成 | 既有 + `TestExtChainTargetViewLifetime`（Submit 不含 mail） |
| A1/A2/A3 | 完成（A2 为真实临时模块 CLI 级）；循环/深度沿用既有测试 | `action_ext_chain_lifetime_test.go`、`action_ext_chain_diag_test.go` |

## 3. P0 场景传播使用调用点的到达定义 — 已完成（commit b2b66a4）

| 项 | 状态 | 证据 |
| --- | --- | --- |
| R1 到达定义（覆盖/合流/遮蔽/顺序，不跨函数） | 完成 | `reachingLiteralAt` + `litScopes`；`TestSceneReassignUsesReachingDef`、`TestSceneReachingOrderAndShadowing` |
| R2 Reassign excluded / Conditional unknown | 完成 | `TestSceneConditionalReassignUnknown`、`TestTodo12SceneReachingRealIndex` |
| R3 证据（定义位置/传播依据）、unknown 不回退场景键 | 完成 | `SceneFit.Source`、`sceneArgUnknown`；`TestSceneReachingBranchMerge` |
| R4 局部字面量/两层传参保持，深度限制显示 | 完成 | 既有 `TestSceneFlowTwoLayers` + 新增 |

## 4. P1 场景守卫的实现选择覆盖 else 与未知路径 — 已完成（commit b2b66a4）

| 项 | 状态 | 证据 |
| --- | --- | --- |
| R1 known US → ImplUS，排除 ImplCN，记录取反依据 | 完成 | `reachingImplsAt` + 守卫极性；`TestSceneImplElseBranch`、`TestTodo12SceneImplElseRealIndex` |
| R2 无场景/不能证明 → 候选 + ambiguous（JSON/文本/Mermaid） | 完成 | `reaching-guard-unknown` + 渲染层 ambiguous 标记；`TestSceneImplNoSceneAmbiguous` |
| R3 到达定义、嵌套 else-if、未初始化 | 完成 | `TestSceneImplOverrideAndUninitialized`、`TestSceneImplElseBranch`（else-if US） |

## 5. P0 异步关联以查询种子为范围 — 已完成（commit 5e1f08c）

| 项 | 状态 | 证据 |
| --- | --- | --- |
| R1 节点/关系/候选同一 scoped 集合 | 完成 | 候选只取字段身份/类型前缀/ID 同源/调用链；`TestAsyncScopeExcludesUnrelated` |
| R2 seed 必须在图；无证据明确说明 | 完成 | canonical seed 不存在报错；`no_related_links`；`TestAsyncScopeNoLinks`、`TestTodo12AsyncScopeRealIndex` |
| R3 稳定排序、实际数量、预算、partial | 完成 | `CandidateTotal/CandidateBudget/Partial` + `candidates_truncated`；`TestAsyncScopeCandidateBudget` |
| R4 共享字段/同名 ID 仍 unverified | 完成 | 关联判定未升级；`TestAsyncScopeExcludesUnrelated` |
| A1/A2/A3 | 完成 | 单测 + 真实多包索引 CLI（无关模块/无关联/预算） |

## 6. P1 跨入口值身份与异步因果闭环 — 部分完成（commit b674184）

| 项 | 状态 | 证据 / 缺口 |
| --- | --- | --- |
| R1 写 → 持久化/队列边界 → 读/过滤，逐跳位置/依据 | 部分 | `chains[]` 逐跳 write/persistence_or_queue/read/filter + File/Line；**边界未建模**（列映射不等于同一条记录） |
| R2 有真实路径+值来源+匹配证据才升级因果 | 完成 | `summary_origins` 同指纹 → value_origin/corroborated；不同源 → name_only/unverified；`TestAsyncIdentityDifferentOrigin/SameOriginCorroborated` |
| R3 入口注册证据；名称启发式低等级；不推断固定次数 | 完成 | `RoleEvidence=registration:<caller>`；name-only → unverified；`TestAsyncIdentityChainHops`、真实索引 `ScheduleAdvance` |
| A1 跨入口身份链 | 部分 | `Chains` + 集成断言；持久化/过滤条件仍标 unverified（A4 允许部分） |
| A2 同名不同源/同表不同记录/同调用位置不同参/互斥分支 | 部分 | 同名不同源（origins）已测；同表不同记录/过滤条件明确标未建模；互斥分支沿用 todo11-3 测试 |
| A3 注册证据分类/不推断次数 | 完成 | 注册调用方名称证据 + 现有 dual_advance 无次数断言 |
| A4 task5 范围修正不替代本任务 | 完成 | 独立 `identity_partial` + `persistence_boundary_unverified` 诊断 |

## 7. P0 副作用路径达到预算时不得静默截断 — 已完成（commit 3453751）

| 项 | 状态 | 证据 |
| --- | --- | --- |
| R1 预算/嵌套/循环/switch 进完整性诊断 | 完成 | `pathWalkStats` + `EffectAnalysis`；`TestEffectsPathBudget` |
| R2 稳定保留已有路径 + 预算说明 + 可提高预算 | 完成 | `--path-budget`；`TestEffectsPathBudget`（默认 partial / 64 → 31 条 + 末尾 return） |
| R3 成功/错误/提前返回覆盖可核对，result 不冒充 paths | 完成 | 路径级 result + `missing_return` 由 paths 判定 |

## 8. P1 被调写入及错误来源归入实际执行路径 — 部分完成（commit 4df3a65）

| 项 | 状态 | 证据 / 缺口 |
| --- | --- | --- |
| R1 被调写入按调用条件/位置归属 | 完成 | `pathCalls` + `calleeWrites`；`TestEffectsErrorPathsCalleeWrites` |
| R2 写点区分字段赋值/数据库写/提交范围 | 部分 | 已有 `Origin=ast|callee|summary` + `tx` 覆盖陈述；**ORM/SQL 写调用尚未独立成 kind** |
| R3 返回槽错误定义/覆盖/吞掉/旧错误 | 完成 | `ErrorSource`（returned/wrapped/swallowed/overwritten/stale/nil/unknown）；`TestEffectsErrorSourceStatuses` |
| R4 兼容汇总 + 预算/未知调用 partial；目标筛选复用任务 1 | 完成 | `path_callee_unresolved`；`TestEffectsErrorSourceStatuses`（partial） |

## 9. P1 sequence return/字段焦点闭合必要值来源 — 已完成（commit a3b9416）

| 项 | 状态 | 证据 |
| --- | --- | --- |
| R1 自动保留到达定义/条件/必要调用/返回槽 | 完成 | `focusLinesForFunc` 依赖闭包；`TestSeqFocusReturnClosure`、`TestSeqFocusFieldClosure`、`TestTodo12FocusClosureRealIndex` |
| R2 别名/同名不同类型/遮蔽不串线；未知 partial | 部分 | 按值依赖与栈内作用域，未知不做确定性结论；跨函数别名依赖仍保守（见待办） |
| R3 不存在/歧义报错；合法无写入正常返回 | 完成 | `validateFocusTargets`；`TestSeqFocusValidation`、`TestTodo12FocusValidationRealIndex` |
| R4 无焦点保持既有行为；投影不隐藏截断 | 完成 | 任务 13 后投影诊断保留原诊断 |

## 10. P1 value-trace 从真实写点追到状态常量 — 已完成（commit 7f263ec）

| 项 | 状态 | 证据 |
| --- | --- | --- |
| R1 初始化/条件合流/常量/最终字段写 | 完成 | phi_operand 不再跳过常量；literal 节点（类型+值去重）+ 源码行/条件；`TestPhiOperandConstantSource`、`TestTodo12ValueTraceConstantsRealIndex` |
| R2 深度/缺边/不支持 → partial；字段名与节点 ID 契约分开 | 完成 | `analysis{status,depth,stop}`；字段名锚点明确错误；深度 1 → partial/depth |
| A1 真实索引 trace-backward → value-trace | 完成 | 常量 `"queued"/"rejected"`、phi_operand、rejected 条件 `reject` |
| A2 别名/同名实例/跨层传参/不足深度 | 完成 | 跨层 normalize（argument/returns 边）；其他实例常量不串链；深度 partial |

## 11. P1 Mermaid 保留结构化时序中的必要赋值 — 已完成（commit a3b9416）

| 项 | 状态 | 证据 |
| --- | --- | --- |
| R1 渲染必要 assign（条件/参与者/位置/来源） | 完成 | `query_sequence_render.go` assign → `Note over`；`TestSequenceMermaidKeepsAssign` |
| R2 defer/async/返回终止语义保持；赋值不画调用线 | 完成 | 同一节点切片；测试断言无 `->>` 赋值线、alt/end 配对 |
| A1/A2 | 完成 | 单测 + `Todo12FocusClosureRealIndex`（三格式） |

## 12. P1 多个具名返回值保留独立槽身份 — 已完成（commit b00084f）

| 项 | 状态 | 证据 |
| --- | --- | --- |
| R1 同组多名/混合类型/匿名/裸 return | 完成 | `resultNamesOf` 逐名展开 + 裸 return 槽；`TestCodeSequenceGroupedResultSlots` |
| A1/A2 | 完成 | `TestTodo12NamedResultSlotsRealIndex`；`--target-return reason/#1` 归一化（任务 1 测试） |

## 13. P1 展开停止/焦点过滤/索引降级诊断 — 部分完成（commit 6be5d3d）

| 项 | 状态 | 证据 / 缺口 |
| --- | --- | --- |
| R1 展开终点分类（叶子/外部/未解析/深度/循环/预算） | 完成 | 未解析调用加 `unresolved` stop；`TestSeqDiagUnresolvedCall` |
| R2 原诊断保留 + 投影诊断独立 | 完成 | `focus_projection`/`scene_projection`；`TestSeqDiagFocusKeepsAnalysis`、`TestSeqDiagDepthKeptAcrossFocus` |
| R3 构建保存包/文件/SSA 失败并随查询携带 | 部分 | 已透传 build status/SHA/降级统计并给 `index_degraded`（`TestSeqDiagIndexDegraded`）；**包级/类型检查失败明细尚未入库** |

## 14. P1 证据命令可安全重放相同分析 — 已完成（commit 0a9ef59）

| 项 | 状态 | 证据 |
| --- | --- | --- |
| R1 规范化请求（repo/ID/depth/focus/scene/索引版本）+ 结构化 argv | 完成 | `SeqEvidenceRequest` + `shellQuote`；`TestTodo12EvidenceReplay` |
| R2 配置/索引变化可判定；不泄露运行参数 | 完成 | `index_sha`/`stop_packages` 入快照；命令只含查询参数 |
| A1/A2 | 完成 | 非模块目录 bash/zsh 实跑；仓库含空格、方法含括号、多 scene/focus |

## 15. P1 重建后恢复既有 workspace 归属 — 已完成（commit 07078ce）

| 项 | 状态 | 证据 |
| --- | --- | --- |
| R1 clean/reindex/init 元数据契约；不猜归属 | 完成 | clean 默认 tombstone（清构建状态），`--forget` 才注销；`TestWorkspaceAttributionRestoredAfterClean` |
| R2 官方 CLI 认领已有 worktree | 完成 | `workspace init` 认领（git worktree 元数据证明）；`TestWorkspaceInitBasic/Idempotent` |
| R3 跨仓候选遵守 workspace/worktree 身份 | 完成 | 显式 `--dir` 作用域 + 不匹配失败；`TestWorkspaceClaimErrors` |
| A1/A2/A3 | 完成 | 真实 git worktree + clean→init→list；未登记/非 worktree 反例 |

## 16. P1 真实多服务框架 RPC 识别与续接 — 部分完成（commit 06181b1）

| 项 | 状态 | 证据 / 缺口 |
| --- | --- | --- |
| R1 客户端包装/自定义注册/外部生成代码/DI | 完成 | 接收者类型识别 `grpcClientServiceOf`；集成多模块替身 + 自定义注册 + DI |
| R2 方法身份与证据；未识别边界可诊断 | 完成 | `Key=方法名`；同名 Submit 不串服务（单测+集成 ext-chain） |
| A1 grpc-routes/grpc-callers/ext-chain 对照 | 完成 | `TestTodo12GrpcMultiServiceRealIndex`（两服务、Order→HTTP notify） |
| A2 同名服务不同包/同服务两方法/非 RPC 同名/其他 workspace | 部分 | 同服务两方法、同名跨服务、非 RPC 同名已覆盖；**同名服务不同 proto 包冲突消歧保留** |
| A3 识别覆盖与连接生命周期分开 | 完成 | 识别（本任务）+ 生命周期（任务 2）分别测试 |

## 17. P1 控制大仓 sequence 与值追踪查询成本 — 部分完成（commit b58724d）

| 项 | 状态 | 证据 / 缺口 |
| --- | --- | --- |
| R1 分阶段统计/缓存命中/覆盖 | 部分 | sequence meta.timing（resolve/read/walk/render/nodes）；value-trace elapsed_ms；**SQL/查符号/派发细分计数器未做** |
| R2 profile 优化重复解析/查找 | 未开始 | 需固定大仓 profile（承接既有 Q246–Q252d 成果） |
| R3 可取消/预算诊断，超时不返回成功 | 完成 | `query sequence --timeout`：ctx 取消、失败退出、明确“已停止后台展开”；单测 120 层 + 1µs 预算 |
| A1 大仓基准分布 | 未开始 | 需固定源码/索引/配置的大仓与冷/暖/并行分布 |
| A2 depth/focus/未知密集/派发/少结果 value-trace 对照 | 部分 | 语义对照沿用现有测试；预算内不可用现象未做大仓复测 |
| A3 取消/超时退出与资源峰值 | 部分 | 取消测试通过；内存峰值需大仓采样 |

## 后续增量状态（2026-10-30）

- **#13-R3** 已完成：ssa 记录 pkg_total/pkg_with_errors/typecheck_errors →
  build_metadata；类型错误 → degraded；sequence meta.coverage + 
  typecheck_errors/packages_with_errors 诊断（commit f711f43）。
- **#8-R2** 已完成：DBEffect.kind（field_assign/field_write/db_write/
  callee_write）+ commit_scope（begin_commit/outside_or_unknown/none）；
  只有完整 table.column 才证明数据库写（commit 407174d）。
- **#9-R2** 已完成：焦点依赖按类型身份匹配；未知类型保守保留 +
  focus partial + focus_dependency_unresolved（commit 50e03a2）。

---

## 未完成项汇总（接手先看）

1. **任务 6**：持久化/队列/过滤条件建模（同一记录身份）——当前边界
   未建模，链整体 unverified + identity_partial。
2. **任务 16-A2**：同名服务不同 proto 包的冲突消歧（当前按服务名匹配）。
3. **任务 17-R2/A1/A2**：固定大仓的 profile/基准/并行分布与 SQL/查符号
   细分计数器（取消/预算已完成）。

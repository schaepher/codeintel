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

## 6. P1 跨入口值身份与异步因果闭环 — 未开始（保留）

未实现 request.ID → Job.RecordID 的持久化边界身份验证、角色注册证据。
依赖：需要持久化/队列边界建模 + `value_origin` 跨入口链，见待办清单。

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

## 10. P1 value-trace 从真实写点追到状态常量 — 未开始（保留）

未实现 SSA 值身份连接初始化/条件合流/常量（当前 flow 数不足）。需要单独一轮
SSA 值图整改；本轮不动趋势性结论。

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

## 16. P1 真实多服务框架 RPC 识别与续接 — 未开始（保留）

需要中立多服务框架夹具（客户端包装/自定义注册/生成代码/DI 绑定）。任务 2
只修了连接生命周期，不代表识别覆盖修复。

## 17. P1 控制大仓 sequence 与值追踪查询成本 — 未开始（保留）

需要固定源码/索引/配置的大仓基准、冷/暖与并行分布、取消/超时诊断。
本轮不做单次墙钟回归结论。

---

## 未完成项汇总（接手先看）

1. **任务 6**：跨入口值身份闭环（持久化/队列边界、角色注册证据）。
2. **任务 10**：value-trace 追到状态常量（SSA 值身份补链）。
3. **任务 13-R3**：包/文件/类型检查失败明细入库并随查询携带（当前只有
   build status + SQL 降级统计）。
4. **任务 8-R2**：ORM/SQL 写调用与字段赋值/提交范围的独立 kind（当前用
   `origin` + `tx` 陈述）。
5. **任务 9-R2**：跨函数别名改写仍保守（不升级为确定结论，需 partial 标记）。
6. **任务 16**：多服务框架 RPC 识别覆盖。
7. **任务 17**：大仓查询成本基准与取消/超时诊断。

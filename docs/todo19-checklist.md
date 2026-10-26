# todo19 验收 Checklist（逐项状态与证据）

执行日期：2026-11-01。R=要求，A=验收；状态 = 已完成 / 部分完成 / 未开始。

## 6. P1 调用时系统绑定的顺序/分支/别名实例/方法修改 — 已完成

| 项 | 状态 | 证据 |
| --- | --- | --- |
| B1 调用后实例重赋值不倒推 | 完成 | `instanceAt` 只取调用前定义；调用后 `receiver=&Bus{...}` 不参与。`TestTodo19B1ReassignAfterCallKeepsProof`（保持 corroborated，绑定 system-a） |
| B2 条件分支内重赋值 | 完成 | 调用前非直线路径的重赋值 → `instanceAt` 返回 unknown（“条件分支内重赋值”）；整体 unverified，不 corroborated。`TestTodo19B2ConditionalReassignUnverified` |
| B3 旧实例别名修改不作用于新实例 | 完成 | 实例身份 id（声明/赋值位置）比较：alias 捕获旧 id，当前 receiver 为新 id → 忽略旧别名修改；当前 system-b vs sender system-c → mismatch。`TestTodo19B3AliasOldInstanceIgnored` |
| B4 接收者方法修改配置 | 完成 | 未知调用扫描包含 `call.Fun` 接收者（Configure）；调用前对当前实例调用方法 → unverified + 方法/行号。`TestTodo19B4ReceiverMethodModificationUnverified` |
| R2 受影响矩阵 | 完成 | 未改写正例、直接覆盖、初始配置不同、载荷覆盖（todo17/18）与本轮 B1–B4 全部运行并通过；调用前后顺序/条件/别名/方法各自断言 |
| 逐跳/定位 | 完成 | 绑定文本含初始化/重赋值/覆盖/调用行；boundary 保留 API canonical 目标、binding_source；失败给出具体缺口 |

**保留范围**：运行时配置、反射/动态别名、外部 broker 仍无法静态证明 → unverified。

## 7. P1 workspace 认领保留构建元数据 — 已完成

| 项 | 状态 | 证据 |
| --- | --- | --- |
| 已构建 + HEAD 未变 → 认领后仍已构建 | 完成 | `registerClaimedWorktree` 保留 BuildID/LastBuiltAt/HeadCommit；`TestWorkspaceClaimKeepsBuildMetadata`、`TestTodo19WorkspaceClaimKeepsExtChain`（list status 已构建） |
| 未构建仍未构建 | 完成 | `TestWorkspaceClaimKeepsUnbuilt` |
| HEAD 已变 → 仍过期（保留构建 HEAD） | 完成 | 认领后旧 HeadCommit/BuildID 保留，`statusOf=过期`；同名单测 |
| 未登记主仓库恢复分支 | 完成 | `TestWorkspaceRecoveryClaimKeepsBuildMetadata`（BuildID/LastBuiltAt/HeadCommit/workspace 保留） |
| 重复认领/归属不匹配 | 完成 | 既有 todo13-15 测试继续通过；重复认领保留元数据 |
| 两个真实已索引 worktree 的 ext-chain | 完成 | 客户端/服务端 worktree init → 认领前后 ext-chain 均续接到 server worktree、`ClaimImpl.Submit` 与 `claim-target.example.test`；`TestTodo19WorkspaceClaimKeepsExtChain` |

## 8. P1 PlantUML 诊断注释/PNG 失败语义 — 已完成

| 项 | 状态 | 证据 |
| --- | --- | --- |
| Mermaid 注释 → PlantUML 注释 | 完成 | `mermaidSequenceToPlantuml` 将 `%% diag/evidence` 转 `' ...`；多行 detail 压单行；`TestMermaidCommentsConvertedToPlantuml` |
| 最小输入真实 JAR | 完成 | `TestTodo19PlantumlRealRenderer`：PNG 魔数、`.puml` sidecar、`java -jar … -checkonly` 成功；含 focus/scene/evidence 变体 |
| 诊断不静默丢失 | 完成 | puml 保留 `' diag depth …`；Mermaid/JSON 诊断原样存在 |
| 渲染失败不写文本到 .png | 完成 | `.png` 目标失败 → 写同名 `.puml` 文本回退、stderr 明确、exit 非零；不生成 `.png`；`TestPlantumlPngFailureDoesNotWriteTextToPng` |
| 无 JAR/错误 JAR/转换错误可区分 | 完成 | renderer 错误透传（`plantuml 渲染失败`）；无 JAR 报错、非 PNG 目标回退有 warning 与文本扩展；错误不吞成成功 |

---

## 未完成 / 保留

1. 外部真实 broker/运行时配置绑定仍无法静态证明（unverified）。
2. PlantUML 真实 JAR 校验在无 Java/JAR 环境自动跳过（保留待环境验收）。

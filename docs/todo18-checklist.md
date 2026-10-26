# todo18 验收 Checklist（逐项状态与证据）

执行日期：2026-11-01。R=要求，A=验收；状态 = 已完成 / 部分完成 / 未开始。

## 6. P1 调用位置的系统绑定（配置覆盖/重赋值/别名/分支） — 已完成

| 项 | 状态 | 证据 |
| --- | --- | --- |
| R1/A4 调用位置核对绑定，不用初始化快照 | 完成 | `resolveBindingAt`：最新实例定义 + 调用前字段修改；字段字面量覆盖更新签名，未知/条件修改降级 |
| R1 直接覆盖 `receiver.System="system-b"` | 完成 | boundary=mismatch，chain 不 corroborated、`identity_partial=true`；诊断含初始化 L10、覆盖 L20、调用 L21；`TestTodo18FieldOverwriteBeforeReceive` |
| R2 接收者实例重赋值 | 完成 | `receiver=&Bus{...}` → mismatch，绑定显示初始化 L10、重赋值 L20、调用 L21；`TestTodo18ReceiverReassignedBeforeCall` |
| R2 经别名修改配置 | 完成 | `alias := receiver; alias.System=...` → mismatch，别名覆盖行计入；`TestTodo18AliasModificationBeforeCall` |
| R2 条件/未知修改 | 完成 | 条件分支修改 → unverified（“调用时值未知”）；调用前未解析接收者传递 → unverified；`TestTodo18ConditionalModificationUnverified` / `TestTodo18UnknownModificationCallUnverified` |
| 调用后修改不倒推 | 完成 | Receive 之后修改配置仍保持 corroborated；`TestTodo18ModificationAfterCallKeepsProof` |
| 未改写配置/同配置不同实例正例 | 完成 | todo17 `TestTodo17SameConfigDifferentInstancesProven`、todo14/15 Bus 正例保持 |
| 实际 API canonical 身份 | 完成 | `producer_target/consumer_target` + `api_identity`（完整包/类型）；绑定来源/逐跳位置保留 |
| 证明前提统一门槛 | 完成 | corroborated 同时要求：值来源/载荷实例、实际 API 目标、调用时绑定、过滤分支与读取位置 |

**保留范围**：静态源码可识别的字面量绑定修改；运行时配置、动态别名/反射、
外部 broker 连接仍无法证明 → unverified（未声称真实传输身份）。

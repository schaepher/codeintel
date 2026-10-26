# todo17 验收 Checklist（逐项状态与证据）

执行日期：2026-11-01。R=要求，A=验收；状态 = 已完成 / 部分完成 / 未开始。

## 6. P1 载荷覆盖与同类型不同系统身份 — 已完成

| 项 | 状态 | 证据 |
| --- | --- | --- |
| R1/A1 读取按具体位置解析载荷值/到达定义 | 完成 | `validateReceiverRead` 要求读取的 `payloadVar` 到达定义仍是选中 `Receive` 返回槽；`job = Job{}` 后不认旧载荷 |
| R1 覆盖诊断定位 Receive/覆盖/读取 | 完成 | Detail 含覆盖行、Receive 行、读取行；`TestTodo17PayloadOverwrittenAfterReceive` |
| R1 未改写载荷/别名正例不误降级 | 完成 | `TestTodo16ReceiverAliasPositive`、todo14/15 正例保持 |
| R1/A4 实际 API 目标完整包/类型身份 | 完成 | `apiCallTargetIndex`（调用边 canonical ID）→ `(Bus).Send/Receive` + `example.com/boundary.Bus`；调用边缺失 → unverified |
| R1/A4 同类型不同系统配置 | 完成 | 实例绑定解析：`sender=&Bus{System:"system-a"}` vs `receiver=&Bus{System:"system-b"}` → `mismatch`，报告两端绑定与位置；`TestTodo17SameTypeDifferentSystemsMismatch` |
| R2 未知绑定 | 完成 | 不同实例且默认/空配置 → `unverified` “系统/配置绑定未建模”；`TestTodo17UnknownBindingNotProven` |
| R2 可证明相同绑定 | 完成 | 同类型 + 同非空配置字面量（或同一实例声明）→ corroborated；`TestTodo17SameConfigDifferentInstancesProven` |
| R2 不同类型反例保留 | 完成 | BusA/BusB → unverified + 类型缺口；`TestTodo16APIDifferentSystemNotProven` |
| 逐跳证据 | 完成 | boundary 新增 `producer_target/consumer_target`、`producer_binding/consumer_binding/binding_source`；persistence hop 带绑定来源；四跳保持文件/行 |

**保留范围**：静态源码绑定：同一实例声明/同一非空配置字面量/不同类型；外部 broker、
运行时配置、动态绑定仍无法证明 → unverified（未声称真实传输身份）。

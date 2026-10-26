# todo16 验收 Checklist（逐项状态与证据）

执行日期：2026-11-01。R=要求，A=验收；状态 = 已完成 / 部分完成 / 未开始。

## 6. P1 接收侧具体值/实例 + 真实 API 身份 — 已完成

| 项 | 状态 | 证据 |
| --- | --- | --- |
| R1/A1 关联 Receive 返回槽、过滤操作数当前定义、具体被读实例 | 完成 | `reachingBinding`（作用域+最近赋值）、`resolveInstanceRoot`（别名/&/新值）；读取实例必须回到 Receive 载荷 |
| R1 反例：读取另一个实例 | 完成 | `other := Job{}; other.RecordID` → unverified（“读取实例 other 与接收载荷 job 无值/别名关联”）；`TestTodo16ReceiverReadsOtherInstance` |
| R1 反例：过滤键被重赋值 | 完成 | `key = "JOB-42"` → 定位覆盖 Lx 与比较 Ly，不认 Receive 键；`TestTodo16ReceiverKeyOverwritten` |
| R2 反例：接收键被遮蔽 | 完成 | 内层 `key := ...` → 作用域/到达定义识别，unverified 且说明遮蔽；`TestTodo16ReceiverKeyShadowed` |
| R2 反例：不同 Receive 结果/载荷实例 | 完成 | 第二个 Receive 的 key/payload 不混用；`TestTodo16ReceiverDifferentReceiveResult` |
| R2 反例：无关同名字段比较 | 完成 | `Other.RecordID == K` 不作为过滤键（要求 Receive 返回键/载荷字段）；`TestTodo16ReceiverUnrelatedSameNameField` |
| 正例：载荷别名、未改写键、相等分支 | 完成 | `alias := job` + `key == K` → 四跳 corroborated、`identity_partial=false`；`TestTodo16ReceiverAliasPositive` |
| R1/A4 API 身份取实际接收者/系统 | 完成 | 变量→类型映射；生产/消费必须同包且**同接收者类型**（Bus）才可能升级；BusA/BusB → unverified 并报类型缺口；`TestTodo16APIDifferentSystemNotProven` |
| 旧反例保持 | 完成 | todo14/15 的 !=、实际发送键、无关变量/调用、发送实例、多发送/分支歧义全部保留并通过 |
| 无法证明时保守降级 | 完成 | 任一接收侧/API 缺口 → `identity_partial=true` + `boundary_identity_unproven`，不从名字/叶名补造 |

**范围**：API 身份 = 同包 + 同接收者类型 + 受支持的发送/接收方法族（静态 stub/Bus
形态）；尚未接入外部 broker/配置系统绑定，动态键/未知 API 保持 unverified。

## 17. P1 资源采样能力与独立取消验收 — 已完成

| 项 | 状态 | 证据 |
| --- | --- | --- |
| A3 识别采样器/参数能力 | 完成 | `detectTimeSampler`：探测 GNU `time -v`，失败回退 BSD `time -l`；都不支持 → `RSS sampling unavailable` + 原因/platform |
| A3 可采样时报告真实峰值/单位/平台/采样器/进程范围 | 完成 | `TestTodo15TargetRSS`：Linux `/usr/bin/time -v` → 27MB（查询进程，不含测试进程） |
| A3 区分采样器不支持与查询失败 | 完成 | 采样器探测先于查询；查询非零 → Fatal “目标查询失败（不是 unavailable）” |
| A3 不可用不当作 0 | 完成 | 两条 unavailable 分支都不设峰值断言，日志保留待验收 |
| A3 取消验收独立 | 完成 | `TestTodo15CancelAfterWork` 独立运行；5ms 预算在 SQL 后取消（`sql_queries=2`）、delay≈7ms，不依赖 RSS 采样器 |
| 旧 todo14 VmHWM 采样 | 完成 | `VmHWM` 读取失败改为明确 unavailable，不再打印 0 并判定通过 |

---

## 未完成 / 保留

1. 外部真实 broker/存储系统的 API/配置绑定证明（当前仅静态同类型 API 族）。
2. 动态键、非字面量记录键仍 unverified（不声称通用身份能力）。

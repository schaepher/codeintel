# todo36 交付 Checklist（输入：`todo36.txt`，基线 `origin/main` f19a643）

规则：每个编号独立验收；生产链路 **源码 → init → CLI**（公开示例
`example.com/remaining`）。只有主例通过时只能标"部分"；未覆盖边界单列。

## 状态总表

| 编号 | 状态 | 证据（要求 → 测试/CLI 输出） |
| --- | --- | --- |
| U04bU.1 goto 目标可达 | 完成 | `goto` 沿函数级真实标签目标继续；break/continue 仍只停止本块；目标按声明/函数作用域解析，重复/向后/超预算 fail-closed partial。`TestResourceGotoTargetWrites/skip`（ReviewGotoSkip → 1、非 partial）、`/reset`（ReviewGotoReset → 0）；`TestResourceGotoInvocationIdentity/positive`（跨文件 skip/其它资源/读取后 → 1）、`/negative`（跨文件 reset → 0）；`TestResourceGotoBudget/complete`（skip 1/非 partial）、`/exhausted`（70 跳 → 0、partial=true） |
| B03e.4 忽略字段 | 完成 | `xorm:"-"` 字段不产生列来源，也不回退默认列名；忽略字段不参与歧义消解；唯一有效映射继续工作。`TestORMIgnoredFieldOrigins/mapped`（ORMNamedFind → `["kind"]`）、`/ignored`（ORMIgnoredFind → `[]`）；`TestORMColumnMappingAmbiguity/unique`（`["kind"]`）、`/ambiguous`（同列两字段 → `[]`）；`TestORMIgnoredFieldPropagation/positive`（映射字段保留）、`/negative`（helper 返回 + range、全忽略模型 → `[]`，Find 错误槽仍建立） |
| B09e.4 包装字段重绑定 | 完成 | field_read 与 parameter 共用跨调用重绑定：根参数下标随真实实参、路径合并、调用前改写/遮蔽失效、请求后改写不倒推。`TestFrontendWrappedFieldParameters/positive`（click → parameter=1 path=[id]）、`/reordered`（交换形参 → parameter=0）；`TestFrontendWrappedFieldInvalidation/positive`（请求后改写 → 保留 field_read）、`/negative`（clickMutated/整对象/父字段/局部遮蔽 → unknown；动态下标在下一组）；`TestFrontendWrappedFieldPaths/positive`（解构别名多层包装 → parameter=1 path=[id]）、`/negative`（同名不同文件只解析实际导入 `/alternate`） |

## 公开示例逐条实测（逐字对照 todo36.txt）

```json
{"connections":0}   # ReviewGotoReset
{"connections":1}   # ReviewGotoSkip
{"columns":[]}      # ORMIgnoredFind
{"columns":["kind"]}# ORMNamedFind
[{"action":"click","body":{"id":{"kind":"field_read","parameter":1,"path":["id"]}}},{"action":"clickMutated","body":{"id":{"kind":"unknown"}}}]
```

## 本轮实现要点

- **U04bU.1**：`seqResourceFactBuilder` 增加函数级 `gotoFrames`（标签声明 →
  所在语句列表/下标）；`walkStmt` 对 `goto` 调用 `walkGotoTarget` 从目标继续，
  不把 goto 当 break；重复/向后目标、缺失目标、64 跳预算超限均标 partial
  （资源 `mutatedAt` fail-closed，不借用旧状态）。
- **B03e.4**：`seqORMColumnName` 对标签 `-` 返回空；新增
  `seqORMValidColumnBindings` 统一过滤忽略字段与重复列歧义，写入/读取/
  条件/元素来源共用同一映射；全忽略模型仍建立读取身份以保留错误槽。
- **B09e.4**：函数模型记录改写位置与调用位置；`seqFrontendRebaseBody` 同时
  处理 `parameter`/`field_read`，按调用实参绑定根对象并合并路径，再按
  **调用前**的改写/遮蔽使来源失效；直接 fetch 使用 `fetchPos` 区分请求前后。

## 明确边界（未在本轮验收）

- **真实 28 个业务流程**：仍需实际业务源码按 21—28、再其余流程验收；
  公开示例不替代业务验收。
- **goto 控制流**：只沿函数级真实声明继续；跨函数/闭包按短名匹配不允许。
  向后/循环/重复目标与超预算一律 partial + 不生成已证明连接（比精确循环
  分析更保守），未实现任意 goto 图的完整不动点求解。
- **ORM**：静态可证明的 `TableName`/标签/显式 Table 已覆盖；动态表名、
  未支持框架、命名预算不足仍 unknown/partial，不猜来源。
- **前端来源**：改写位置来自函数体文本顺序（请求/调用点之前），不执行
  TypeScript 运行时；预算/递归/缺源码仍按 todo35 的 unknown 原因输出。

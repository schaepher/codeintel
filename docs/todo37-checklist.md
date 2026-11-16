# todo37 交付 Checklist（输入：`todo37.txt`，基线 `origin/main` dfbf4e2）

规则：每个编号独立验收；生产链路 **源码 → init → CLI**（公开示例
`example.com/remaining`）。只有主例通过时只能标"部分"；未覆盖边界单列。

## 状态总表

| 编号 | 状态 | 证据（要求 → 测试/CLI 输出） |
| --- | --- | --- |
| B09e.4 调用方声明绑定 | 完成 | 解构参数 `{profile: selected}` 的实参 `selected` 按调用方 `paramBindings` 恢复为 `parameter=1`、`path=[profile]`，与 `clickPlain` 的 `req.profile` 同源。`TestFrontendCallerDestructureBindings/positive`：click/clickPlain → `field_read parameter=1 path=[profile,id]`、交换形参后 → `parameter=0 path=[profile,id]`；`/negative`：局部同名遮蔽（`caller-destructure-shadow`）、局部另一对象（`caller-destructure-local`）、跨文件同名变量（`caller-destructure-crossfile`）、覆盖父对象（`caller-destructure-parent`）均 `unknown` |
| B09e.4 路径合并 | 完成 | 解构路径 + 实参字段路径 + 被调函数字段路径逐次合并，根位置取真实动作函数形参。`TestFrontendCallerDestructurePaths/positive`：嵌套解构 + 中层包装 → `parameter=1 path=[outer,profile,id]`；`/negative`：传另一子对象 → `path=[other,id]`（不重复/沿用 profile）、动态下标与缺声明 → `unknown` |
| B09e.4 调用位置与改写 | 完成 | 未改写保持精确来源；改写位置取真实调用点（`seqFrontendCall` 的 `pos`）。`TestFrontendCallerDestructureInvalidation/positive`：click、调用之后改写（`caller-destructure-after`）、无关兄弟字段改写（`caller-destructure-sibling`）均为 `field_read parameter=1 path=[profile,id]`；`/negative`：clickMutated（调用前改字段）、覆盖父对象 → `unknown`（不残留 parameter/path） |
| B09e.4 多绑定与作用域 | 完成 | 同一解构参数的多个别名各按自己的声明路径解析；改写判定使用真实实参表达式（不再按 map 遍历挑别名）；跨文件被调函数按导入文件解析。`TestFrontendCallerDestructureIdentity/positive`：clickA → `[profile,id]`、clickB → `[other,id]`，两者 `path=/requests`（send 在 api.ts）；`/negative`：同名不同声明只解析实际导入的 b.ts → `[other,id]`；多别名改写连续 8 次查询均 `unknown` |

## 公开示例逐条实测（逐字对照 todo37.txt）

命令（示例根目录，已验证版本二进制）：

```sh
"$CODEINTEL" query sequence symbol:go:example.com/remaining:SubmitHTTP \
  --repo "$PWD" --code --depth 16 --json \
  --related-entry symbol:go:example.com/remaining:ReviewHTTP \
  --frontend-root caller-destructure |
  jq -c '(.workflow.frontend.actions // [])|map({action,body:.request_body})'
```

实测 stdout（与预期原文逐字一致）：

```json
[{"action":"click","body":{"id":{"kind":"field_read","parameter":1,"path":["profile","id"]}}},{"action":"clickMutated","body":{"id":{"kind":"unknown"}}},{"action":"clickPlain","body":{"id":{"kind":"field_read","parameter":1,"path":["profile","id"]}}}]
```

修改前同命令（基线 `dfbf4e2` 二进制）的原文：

```json
[{"action":"click","body":{"id":{"kind":"unknown"}}},{"action":"clickMutated","body":{"id":{"kind":"unknown"}}},{"action":"clickPlain","body":{"id":{"kind":"field_read","parameter":1,"path":["profile","id"]}}}]
```

## 本轮实现要点

- **`seqFrontendArgOrigin` 改按声明绑定**：签名从 `(arg, params []string)` 改为
  `(arg, caller seqFrontendFunc)`，根对象查 `caller.paramBindings` 得到
  **参数位置 + 解构路径**（`selected` → parameter=1 path=[profile]），局部遮蔽
  （`caller.shadowed`）与未声明根名一律 `unknown`。位置不再按形参字符串匹配，
  也不再只认普通形参。
- **统一来源表示携带调用点表达式**：`seqFrontendBodyOrigin.local` 记录实参在
  调用者函数体里的书写形式（`selected` / `req.profile`），只用于改写判定。
- **改写在真实调用位置应用**：`seqFrontendRebaseBody` 用 **合并前** 的被调路径
  （`calleePath`）拼出 `调用点表达式 + . + 被调路径`，与 `caller.mutations`
  按 `call.pos` 比较——解构别名的 `selected.id` 能命中，且不会拼成
  `selected.profile.id` 这类错误表达式；请求/调用之后的改写不倒推。
- **删掉按 map 遍历取根名的 `seqFrontendParamBaseName`**：多别名时它按 map
  迭代顺序挑一个别名，改写判定因此不确定；现在直接用实参表达式。
- **不新增示例专用规则**：`selected`/`profile`/参数下标 1 都没有特判，来源
  全部来自声明与实参。

## 明确边界（未在本轮验收）

- **真实 28 个业务流程**：仍需实际业务源码按 21—28、再其余流程做业务验收；
  公开示例与夹具不替代业务验收。
- **前端来源解析边界沿用 todo35/36**：动态下标、缺声明、局部复制别名
  （`const copy = selected`）、未声明根名保持 `unknown`；预算耗尽/递归/缺源码
  的 `declaration_budget_exhausted` / `declaration_cycle` /
  `declaration_not_found` 口径未变（`TestFrontendBoundedWrapperResolution` 复跑通过）。
- **goto/动态表名等不在本轮**：todo36 的 goto 保守 partial、动态表名/未支持
  框架保持 unknown 的约定不变。

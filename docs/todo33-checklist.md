# todo33 交付 Checklist（输入：`todo33.txt`，基线 `origin/main` 0a5e6ab）

规则：按编号独立验收；生产链路 **源码 → init → CLI**（公开示例
`example.com/remaining`）。未覆盖边界明确列出，不用公开示例代替真实业务验收。

## 状态总表

| # | 事项 | 状态 | 证据 |
| --- | --- | --- | --- |
| B06d | Commit 证明使用准确事务及实际成功路径 | **已完成** | 事务身份改为**声明偏移**（同一行多声明可区分）；`seqCommitSuccessVars` 路径敏感：Commit 错误检查必须出现在所有正常成功出口之前。`SubmitConditionalTx`（条件 Commit）→ 无 `commit_success`、保留 `transaction_commit`；`SubmitTx` 仍有；另一事务/忽略错误仍拒绝。测试 `TestU33B06dConditionalCommit` |
| U04bT | 资源改写使用声明身份、可达分支和准确包内文件 | **已完成** | 资源赋值目标按 AST `*ast.Object` 包级声明身份判断（局部 `DB:=DB` 不命中）；`if true`/`false` 静态裁剪 Else；包内文件按 basename 映射（子目录包入口不再漏事件）。`ReviewShadow`/`ReviewExcludedElse` → 1，`worker:ReviewBeforeReset` → 0。测试 `TestU33U04bTResourceIdentity` |
| B09d | 前端导入别名和请求体来源 | **已完成** | 导入保留本地名/导出名（`click as submitClick`）；支持相对导入、命名重导出；请求体按 `body:` 对象字段相对当前动作形参来源传播（包装调用实参位置）。`click`/`submitClick` 都连接 `/requests`；`click.request_body.id = {kind:parameter,parameter:0}`。测试 `TestU33B09dFrontendAliasAndBody` |
| B03d | Find 元素来源沿 DAO 返回槽传递到字段条件 | **已完成** | ORM select 生成读取结果身份（列+Go 字段名）；绑定集合变量、沿返回槽传播；`range` 元素字段槽绑定 `read_column`（Find/DAO 包装路径）。`ReviewDAO` 的 `r.Kind` → `read_column.column=kind`。测试 `TestU33B03dFindElementColumn` |
| B10d | 批量 Markdown 导出完整组合流程及可渲染时序图 | **已完成** | 每个组合导出使用完整 `scenario.workflow`（入口/连接/实例）文本 + ` ```plantuml ` 图块；SVG 使用同一渲染树；渲染为空明确失败。场景 Markdown 含 `Review` 与 PlantUML，SVG 产物非空。测试 `TestU33B10dBatchScenarioWorkflow` |
| B11d | timeout 必须完整解析 | **已完成** | batch 与单流程共用 `parseTimeout`（整串 Atoi → ParseDuration，拒绝 `3600oops`/零/负值/溢出）；单流程 `--timeout` 显式非法在开库前拒绝。非法 batch：stderr 一行、stdout 空、exit=1、不建目录。测试 `TestU33B11dTimeoutFullParse` |

## 本轮验证

```sh
scripts/verify.sh --quick              # OK
scripts/verify.sh                      # 全量 -race 14 包基线 OK
go test ./internal/... -count=1        # OK
PATH="$(go env GOPATH)/bin:$PATH" \
  go test -tags integration ./integration -count=1   # OK
```

公开示例逐条真实 init→CLI 核对：B06d（条件提交/同事务）、U04bT（遮蔽/
静态 Else/子目录包）、B09d（别名+请求体）、B03d（DAO Find 元素）、
B10d（组合完整 Markdown+PlantUML+SVG）、B11d（非法 timeout 全串解析）。

## 明确边界

- **真实 28 业务流程**（21–28 旧同步入口 0 次人工 FinishReview）仍需调用方
  实际源码后验收；本表只交付通用机制与公开示例。
- **B06d** 只认入口自身的显式 Commit 错误检查与同一事务**声明**；跨函数返回
  事务、延迟/条件 Commit、Rollback、错误覆盖、嵌套函数 return 继续保留
  `transaction_commit`。
- **U04bT** 条件未知时按可能执行保留改写事件（保守）；资源传播深度 ≤8
  （超限 `resource_analysis_budget`/partial）；外部依赖不改写；
  运行时 `ambiguous_resource`/`analysis_not_visited` 不升级为连接。
- **B09d** 只支持命名导入/别名的相对路径、简单重导出与 `body` 对象字段；
  默认导入、复杂解构、代理重写、动态 URL、缺失源码继续 unknown。
- **B03d** 仅 ORM Find/Get 的模型字段；未知集合行数不解释为一次实际请求；
  集合/字段后续修改、返回另一集合、冲突出口、动态命名、其它 ORM 继续保守。
- **B10d** 逐组合 Markdown/SVG 导出；导出成功不等于业务完整性（partial/
  reasons 保留）。
- **B11d** 单流程与 batch 共用解析契约；运行期单项失败仍保留其它成功文件并
  非零退出。

# todo32 交付 Checklist（输入：`todo32.txt`，基线 `origin/main` 207db19）

规则：按编号独立验收；生产链路 **源码 → init → CLI**（公开示例
`example.com/remaining`）。未覆盖边界明确列出，不拿公开示例代替真实业务验收。

## 状态总表

| # | 事项 | 状态 | 证据 |
| --- | --- | --- | --- |
| B06c | 提交证明绑定实际写入事务及成功路径 | **已完成** | 入口 AST 记录 `if err = tx.Commit(); err != nil { return }` 的**事务变量名**；写入从 AST 记录接收者变量（`TxVar`）；只有同变量 + 正常返回才移除 `transaction_commit`。`SubmitOtherTx`（写 tx、检查 other.Commit）与 `SubmitTxIgnored` 保留缺失证明；`SubmitTx` 仍 `commit_success`。测试 `TestU32B06cCommitSameTransaction` |
| U04bS | 资源失效按执行位置和可达路径 | **已完成** | 资源事件按入口语句偏移/调用点偏移记录（本地/闭包/仓内跨包，深度≤8；超限 partial）；写入/读取用入口树 **path 首段节点**的执行偏移比较，后续改写不倒推；闭包未调用不计，调用前改写/已执行闭包拒绝。`ReviewUnusedReset`/`ReviewAfterReset` → 1，`ReviewBeforeReset`/`ReviewClosureExec` → 0。测试 `TestU32U04bSResourcePosition` |
| B09c | 前端包装调用按文件/导入/声明身份连接 | **已完成** | 函数表按 `file+name` 隔离；事件绑定/调用按当前文件→导入路径解析（相对路径多扩展名）；`identity-frontend` 的 click 只连 a.ts request，z.ts 同名不覆盖。测试 `TestU32B09cFrontendIdentity` |
| B03c | 跨文件模型及 DAO 返回链接入 ORM 连接 | **已完成** | 跨文件包级 `Session *xorm.Session` 解析（Obj==nil 回退包内声明）；DAO 内 Find 保留 table/模型/过滤键；`ORMUpdate → ReviewDAO` 连接 1。测试 `TestU32B03cDAOModel` |
| B10c | 每个组合生成完整关联入口流程集合 | **已完成** | 组合重分析携带 `RelatedEntries`/前端，生成独立 `scenario.workflow`（entries/connections/instances），不复制公共 workflow 且无 JSON 自引用；ID/MY 均 entries=2、connections=1。测试 `TestU32B10cScenarioWorkflows` |
| B11c | 非法批量查询参数在分析/导出前拒绝 | **已完成** | manifest 逐流程预校验 timeout/depth/continueSegments/scene/selector/runtimeData（路径相对 manifest、防越界、JSON 校验），失败时 stderr 1 行、stdout 为空、exit=1、不建目录/不打开索引；合法批量仍导出并写重放身份。测试 `TestU32B11cInvalidManifest` |

## 本轮验证

```sh
scripts/verify.sh --quick              # OK
scripts/verify.sh                      # 全量 -race 14 包基线 OK
go test ./internal/... -count=1        # OK
PATH="$(go env GOPATH)/bin:$PATH" \
  go test -tags integration ./integration -count=1   # OK
```

公开示例逐条真实 init→CLI 核对：B06c（同事务/另一事务/忽略错误）、
U04bS（未执行闭包/调用后改写/调用前改写/已执行闭包）、B09c（同名跨文件）、
B03c（DAO/跨文件模型）、B10c（每组合 workflow）、B11c（非法参数与合法批量）。

## 明确边界

- **真实 28 业务流程**（21–28 旧同步入口 0 次人工 FinishReview）仍需调用方
  实际源码后验收；本表只交付通用机制与公开示例。
- **B06c** 只认入口函数自身的显式 `if err = tx.Commit(); err != nil { return }`
  与同一事务变量；跨函数返回事务、条件/延迟 Commit、Rollback、错误覆盖继续保留
  `transaction_commit`。
- **U04bS** 资源改写事件传播限同包/仓内跨包、深度 ≤8（超限 `partial` +
  `resource_analysis_budget`）；外部依赖/未证明路径不改写；运行时
  `ambiguous_resource`/`analysis_not_visited` 不升级为连接。
- **B09c** 解析 TS/TSX 导出函数、命名导入（相对路径）、简单包装实参；默认导入/
  重导出/代理重写/动态 URL/缺失源码继续 unknown。
- **B03c** 只支持已证明的 xorm Session Insert/Update/Find 与显式 Table/标签；
  DAO 返回模型按实际 Find 读取；返回另一集合、动态命名、未知修饰器、链重绑、
  其它 ORM 框架继续 unknown。
- **B10c** 每个组合重分析共享请求预算；真实业务覆盖/完整体验仍需调用方数据。
- **B11c** 预校验拒绝非法参数（exit=1、无产物）；单流程运行期失败仍按失败隔离
  保留其它成功文件并非零退出。

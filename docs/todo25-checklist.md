# todo25 交付 Checklist（对照 `origin/main` 的 3be86a3）

输入：`todo25.txt`（自动续查剩余边界 + §1–§13）。
规则：按编号独立验收；未完成项保留原因，不用"主要功能完成"代表整项。

## 自动续查的剩余边界

| # | 事项 | 状态 | 实现 / 证据 |
| --- | --- | --- | --- |
| 1 | §4.3 HTTP 已证明 handler 子前沿接入同一续查树 | **核心已完成（本轮）** | `action_seq_http.go` + `action_seq_http_origin.go`：`net/http.Get/Head/Post`（导入别名）+ 静态 URL → 服务端 `http_route`（path/method/handler_id）→ 注册函数**直接语句** `HandleFunc` + `ListenAndServe(<字面量 addr>, nil)` 证明 origin；`--continue-segments` 跨仓续查复用同一前沿/取消/证据/budget。同包不同文件路由 ID 改为 `包:route.<相对路径>@<offset>`（旧每文件自增 route.1 会 UPSERT 合并）。 |
| 2 | §3.4 工厂返回实例 / 条件实现 / 函数列表独立子调用接入续查 | **公开正例 + getter 反例已完成；其余边界保留** | `action_seq_factory_field*.go`：工厂返回实例来源沿 `b.Run→b.Step` 传递（`curRecvOrigin`），`b.Get()` 方法体 `return <receiver>.<field>` 唯一字段 + 工厂 setter 实参类型/switch 守卫生成互斥分支；`TestTodo25FactoryFieldContinuation`（两条 kind 守卫 + One/Two.Work 准确 ID + 各事件一次 + `ambiguous` 不串行）、`TestTodo25FactoryFieldNegative`（getter 返回其他字段不借证据）。**保留**：闭包捕获后修改、未知方法修改、嵌入提升方法/多条提升路径、完整反例矩阵。 |
| 3 | 未验证接口候选 / 外部未证明回调不得自动续查 | **已完成（回归固化）** | `integration/todo25_continuation_test.go::TestTodo25AmbiguousCandidatesNotContinued`：真实 init → CLI，两个候选保留 + `iface_ambiguous`，HitA=0/HitB=0，`segments_used=0`/`budget_stops=0`/无前沿，`candidate_depth<=2`。 |
| 4 | 保存调用点完整上下文/防环祖先/覆写/异步/defer/预算/取消 | **已完成（回归固化）** | `TestTodo25OverwriteAndBudgetRegression`：Sink=1/Wrong=0；预算 1 → Sink=0/budget_stops=1 且停止前沿只计一次；LoopEntry cycle=1/Wrong=0；DeferredEntry Sink=2 且 defer/async 保留。1ns 预算非零退出 + stdout 空沿用 `TestContinueSegmentsRealCLI`。 |

### 本轮 HTTP 验收明细

正例夹具（`integration/todo25_http_test.go`，client/server 双模块 + workspace）：

- depth=12：`Entry → RawPostWithSign → http.Post` 节点证据
  `source=http_server`，`symbol_id=symbol:go:example.com/server:Submit`，
  `reason=http-origin+method+registered-handler`，`remote_repo=<ws>/server`；
  子树含 `server:Persist`。
- depth=2：HTTP 节点仍保留同一 Submit ID，`stop.code=depth`。
- depth=2 + `--continue-segments 16`：跨仓续查接到 `Persist`。
- workspace 外（主仓库查询，无 workspace 归属）：不续接 `server:Submit`，
  保留 `http_target_unproven`。
- 同包不同文件两条注册（`server.go` `/submit`、`extra.go` `/extra`）：真实
  init 后 `query http-routes` 两条均存在。
- 动态 URL：`http_endpoint_unknown`，不进入 handler。

单元反例（`internal/action/action_seq_http_test.go`）：HTTPS/相对地址 →
`http_endpoint_unknown`；端口不符 → `http_target_unproven`；`ListenAndServe`
不在直接语句 → 不证明；两个满足证明的注册 → 两条候选（`http_target_ambiguous`
由调用点转换）。

### 实际执行

```sh
go test ./internal/action -count=1
go test ./internal/infrastructure/ast -run 'TestHTTPRoute' -count=1
PATH=$(go env GOPATH)/bin:$PATH go test -tags integration ./integration -run 'TestTodo25' -count=1
bash scripts/verify.sh --quick
```

结果：以上均 PASS（unit 1.8s；ast 1.6s；todo25 集成 1.46s）。

## 其余 §1–§13

尚未开始，按 todo25.txt 原编号保留（不将新增诊断视为完成）。

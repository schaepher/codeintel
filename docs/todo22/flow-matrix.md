# todo22 §9 流程矩阵最终输出（2026-10-27）

对应的实现与夹具：

| 内容 | 位置 |
| --- | --- |
| 夹具（模块 `example.com/flowmatrix`） | `integration/flowmatrix_*_fixture_test.go` |
| §9.1 八个同步流程（编号 21–28） | `integration/todo22_flow_sync_test.go` + `flowmatrix_sync_fixture_test.go` |
| §9.2 二十个流程形态（编号 01–20） | `integration/todo22_flow_forms_test.go` + `flowmatrix_review_fixture_test.go` / `flowmatrix_host_fixture_test.go` |
| §9.3 JSON↔Mermaid / SVG / 分段验收 | `integration/todo22_flow_matrix_render_test.go` |
| 时序图（SVG，含 RPC/DI/两层装饰器/外部事务回调） | [`flow.svg`](flow.svg) |

## 0. 复现命令

```sh
# 逐项验收（§9.1 16 子测试 + §9.2 25 子测试）
PATH=$(go env GOPATH)/bin:$PATH go test -tags integration ./integration \
  -run 'TestTodo22SyncFlows|TestTodo22FlowForms' -count=1 -v

# JSON ↔ Mermaid 一致 + SVG 产物内容 + 分段连接
PATH=$(go env GOPATH)/bin:$PATH go test -tags integration ./integration \
  -run 'TestTodo22FlowMatrix|TestTodo22SegmentedFlow' -count=1 -v

# 重新生成本目录的交付产物（flow.svg；JSON/Mermaid/文本仅供核对后清理）
CODEINTEL_TODO22_DOCS_OUT="$PWD/docs/todo22" PATH=$(go env GOPATH)/bin:$PATH \
  go test -tags integration ./integration -run 'TestTodo22DocOutputs' -count=1 -v
```

本目录只保留时序图 `flow.svg` 与本文（临时 JSON/Mermaid/中间 `.puml`/调试输出已清理）。

## 1. §9.1 八个同步流程（编号 21–28）

命令（每项两条：Source=7 / Source=19，逐项独立执行）：

```sh
codeintel query sequence 'symbol:go:example.com/flowmatrix:(App).Flow<NN><Op><Src>' \
  --repo <fixture> --code --depth 24 --scene Source=7|19 --json
```

| 编号 | 入口 | 场景 | 已证明连接（关键目标 ID） | 候选/截断/未知 | finish 次数依据 | JSON/图一致性 |
| --- | --- | --- | --- | --- | --- | --- |
| 21 | `(App).Flow21NewEntityA` | Source=7 / 19 | `checksAndTrace`→`SaveTrace`→`ApplyRemote`→`(AccountServer).Apply`（RPC）→`(App).txInsert`→`Execute`/`First`/`Second`→`tx.Run`→DI `(storeAOP).Insert`→`(Store).Insert`→`(DB).ExecContext`→`ApproveTrace` | 装饰器 `process` 循环重赋值：`func_value_unbound`（0 候选）+ 两分支多候选；`MakeFirst` 作变参元素仅标签 | 无审核调用（`FinishReview`=0） | 同一分析树（§9.3 用例覆盖含审核的同款结构） |
| 22 | `(App).Flow22NewChildA` | 同上 | 同上，存储终点 `(Store).InsertChild` | 同上 | 0 | 同上 |
| 23 | `(App).Flow23EditChildA` | 同上 | 同上，存储终点 `(Store).UpdateChild` | 同上 | 0 | 同上 |
| 24 | `(App).Flow24NewEntityB` | 同上 | 同上，`(Store).Insert` | 同上 | 0 | 同上 |
| 25 | `(App).Flow25NewChildB` | 同上 | 同上，`(Store).InsertChild` | 同上 | 0 | 同上 |
| 26 | `(App).Flow26EditChildB` | 同上 | 同上，`(Store).UpdateChild` | 同上 | 0 | 同上 |
| 27 | `(App).Flow27EditEntityA` | 同上 | 同上，`(Store).UpdateEntity` | 同上 | 0 | 同上 |
| 28 | `(App).Flow28EditEntityB` | 同上 | 同上，`(Store).UpdateEntity` | 同上 | 0 | 同上 |

检查清单差异（每项都断言）：Source=7 → `Check1..4` 各 1 次、`Check5..7` 0 次；
Source=19 → `Check1..7` 各 1 次。顺序断言：`SaveTrace < ApplyRemote < 存储终点 <
ApproveTrace`。

## 2. §9.2 二十个流程形态（编号 01–20）

| 编号 | 入口 | 场景 | 关键目标 ID / 已证明连接 | 候选/截断/未知 | finish 次数依据 | JSON/图一致性 |
| --- | --- | --- | --- | --- | --- | --- |
| 01 | `(App).FlowWebNewEntity` | 7 | `(App).webSubmit`→RPC→`txInsert`→DI→`(Store).Insert`→`FinishDataReview`/`FinishServiceReview`→`ApproveTrace` | 同 §9.1 的装饰器多候选 | `FinishReview`×2（资料 101 / 服务 202，各落 `review_data`/`review_service`） | 同一棵树（§9.3 用例） |
| 02 | `(App).FlowWebNewService` | 7 | 同上 + `(Store).SaveService` | 同上 | ×2 | 同上 |
| 03 | `(App).FlowWebNewChild` | 7 | 同上 + `(Store).InsertChild` | 同上 | ×2 | 同上 |
| 04 | `(App).FlowAppNewEntity` | 7 | `(App).appSubmit`（无 `webSubmit`）→共享后端链 | 同上 | ×2 | 同上 |
| 05 | `(App).FlowAppNewService` | 7 | `appSubmit` + `SaveService` | 同上 | ×2 | 同上 |
| 06 | `(App).FlowAppNewChild` | 7 | `appSubmit` + `InsertChild` | 同上 | ×2 | 同上 |
| 07 | `(App).FlowPartnerNewEntity` | 7 / 19 | `(App).partnerSubmit`；A：`AutoApprove(DataNode)`；B：`finishManualReviews` | 来源分派由 scene 裁剪（`if req.Source == SourceA`） | A=0 / B=2 | 同上 |
| 08 | `(App).FlowPartnerNewService` | 7 / 19 | 同上 + `SaveService` | 同上 | A=0 / B=2 | 同上 |
| 09 | `(App).FlowPartnerNewChild` | 7 / 19 | 同上 + `InsertChild` | 同上 | A=0 / B=2 | 同上 |
| 10 | `(App).FlowEditChild` | 7 | `FinishDataReview`→`PersistDataReview` 与 `(Store).UpdateChild` 分别出现 | 同上 | ×1 | 同上 |
| 11 | `(App).FlowAppNewBankAccount` | 7 / 19 | A 自动 / B 人工；`(Store).SaveAccount` 与 `(Store).UpdateEntityState` | 同上 | A=0 / B=2 | 本目录 `flow.svg`（Source=19） |
| 12 | `(App).FlowAppEditEntity` | 7 | `(App).appSubmit` + `FinishServiceReview` + `(Store).UpdateEntity`；不出现 `webSubmit` | 同上 | ×1 | 同上 |
| 13 | `(App).FlowWebEditEntity` | 7 | `(App).webSubmit` + 共享 `FinishServiceReview` + `UpdateEntity`；不出现 `appSubmit` | 同上 | ×1 | 同上 |
| 14 | `(App).FlowHostANewBankAccount` | 7 | `(App).hostAHandler` + 共享 `(App).hostApply` + `SaveAccount`；不出现 B/C handler | 同上 | A=0（自动） | 同上 |
| 15 | `(App).FlowHostBNewBankAccount` | 7 | `hostBHandler` + `hostApply` + 两次人工 | 同上 | ×2 | 同上 |
| 16 | `(App).FlowHostCNewBankAccount` | 7 | `hostCHandler` + `hostApply` + 资料审核 | 同上 | ×1 | 同上 |
| 17 | `(App).FlowPaymentNewEntity` | 7 / 19 | `AutoApprove`（仅 A）+ `FinishDataReview` + `SyncRegistry` | 注册同步是自动步骤 | ×1（A/B 均 1） | 同上 |
| 18 | `(App).FlowHostTransferNewService` | 7 | `hostTransferSubmit`→`FinishDataReview`→`SyncRegistry`→`SaveService`→`ApproveTrace` | 同上 | ×1 | 同上 |
| 19 | `(App).FlowAPINewEntityReview` | 7 | `apiSubmit` + 两次人工 finish | 同上 | ×2 | 同上 |
| 20 | `(App).FlowAPINewChildDirect` | 7 | `apiSubmit`→`InsertChild`；**无任何审核符号** | 无审核配置 | 0 | 同上 |

审核规则的源码/注册/持久化身份：

| 规则 | 事实来源 |
| --- | --- |
| 资料节点人工 finish 一次（node_id=101） | `FinishDataReview` → `FinishReview(DataNode{ID:101})` → `PersistDataReview` → `saveReviewRow("review_data")` |
| 服务节点人工 finish 一次（node_id=202） | `FinishServiceReview` → `FinishReview(ServiceNode{ID:202})` → `PersistServiceReview` → `saveReviewRow("review_service")` |
| 自动审核不调人工 | `autoApproveData` → `AutoApprove(DataNode)`，函数体内无 `FinishReview` |
| 外部注册同步不算第三次 | `SyncRegistry()`（RPC 服务端 `(AccountServer).Apply` 内也会调用），不触发 `FinishReview` |
| 无审核配置 = 0 | `FlowAPINewChildDirect` 只做 `apiSubmit` + `txInsertChild` |

## 3. 最终输出（§9.3）

同一个入口（`(App).FlowAppNewBankAccount`，Source=19）实际生成 JSON 与 Mermaid：

- JSON 调用节点 **100** 条；Mermaid 调用箭头 **99** 条（入口自身不画自调用），
  **逐项顺序相等**（`TestTodo22FlowMatrixJSONMermaid`）。
- 条件结构：Mermaid `alt` 行 **17** 条；JSON 的 branch/loop（带子步骤）标签全部
  出现在 Mermaid 中（含人工/自动审核分支 `alt req.Source == SourceA`）。
- 链路完整性（与渲染分开验收）：同一棵树里出现 RPC 服务端
  `(AccountServer).Apply`、DI/AOP `(storeAOP).Insert`、两层装饰器
  `BeforeFirst`/`BeforeSecond`/`AfterSecond`、外部事务回调 `tx:Run`、
  人工审核 `FinishReview`、主数据 `(Store).SaveAccount`、状态
  `(Store).UpdateEntityState`。
- SVG：`--format svg --out flow.svg` 实测 **205216 字节**，含 `<svg`、上述关键
  节点与条件结构（`alt`/`group`）；渲染成功与后端链路完整分别断言
  （`TestTodo22FlowMatrixSVG`）。

### 分段验收（§1）

| 段 | 根 | 结果 |
| --- | --- | --- |
| 段 1 | `(App).FlowAppNewBankAccount`（Source=19） | 到达 `(App).txSaveAccount` / `(App).txUpdateEntityState`；路径上无 `ambiguous`/`unverified` 证据 |
| 段 2 | `(App).txSaveAccount`（同一场景） | 根 ID 与段 1 终点 **完全一致**；含 `(storeAOP).SaveAccount` → `(Store).SaveAccount` → `(DB).ExecContext` |
| 段 3 | `(App).txUpdateEntityState`（同一场景） | 根 ID 一致；含 `(Store).UpdateEntityState` |

（`TestTodo22SegmentedFlow` 断言"上一段终点 ID == 下一段根 ID"、"场景一致"、
"祖先没有 ambiguous/unverified"。）

### Mermaid 摘要（节选，完整输出由上面的命令生成）

```mermaid
sequenceDiagram
  participant P0 as FlowAppNewBankAccount
  participant P1 as appSubmit
  participant P2 as ApplyRemote
  participant P3 as AccountServiceClient
  alt int32(req.Source) == int32(SourceB)
    P1->>P1: append
  end
  loop range checks
    P1->>P1: check
  end
  P1->>P2: ApplyRemote
  P2->>P3: NewAccountServiceClient
  P2->>P3: Apply
  P3->>P3: SyncRegistry
  P0->>P1: txInsert
  ...
  alt req.Source == SourceA
    P0->>P0: autoApproveData
  else
    P0->>P0: finishManualReviews
  end
  P0->>P0: SaveAccount
  P0->>P0: UpdateEntityState
```

## 4. 回归执行记录（§9.3-4）

| 命令 | 结果 |
| --- | --- |
| `go test ./...` | PASS（全部包） |
| `bash scripts/verify.sh --quick` | PASS |
| `make it`（`-tags integration`，含真实 init） | PASS（约 54s；§9 用例全绿） |
| `go test -race ./internal/action -run 'TestSeq|TestScene' -count=1` | PASS（回调/闭包/场景） |
| `go test -race ./internal/infrastructure/sqlite -run 'TestCalleesContextInterruptsRecursiveSQL|TestWalkEdges' -count=1` | PASS（递归取消） |

跳过的项目：`TestTodo22DocOutputs` 默认 skip（需 `CODEINTEL_TODO22_DOCS_OUT`）；
plantuml/java 缺失时 SVG 用例 skip（本机可用，因此实际执行）。

## 5. 未支持/保留未知（不放宽完成定义）

- 装饰器 `process` 在 `for` 循环里重赋值 → `func_value_unbound`（0 候选）+
  两分支多候选；`MakeFirst(ctx)` 作为变参装饰器元素只有标签（`iface first-of-2`）。
  这些不影响入口→存储→审核→完成的确定性连接，逐项列在上表"候选/截断/未知"列。
- `FinishReview(DataNode)` 的 `node_id`（101/202）是源码常量/持久化行身份；
  静态时序不展开参数值，故以"两个不同调用行 + 不同 persist 函数/行"作为可核对身份。
- 场景裁剪只覆盖同函数内的 `req.Source` 比较（跨函数传 req 的来源分派不裁剪，
  因此 partner 形态把分支放在入口函数内）。

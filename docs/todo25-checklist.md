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

## 其余 §1–§13（历史说明）

早期"尚未开始"的占位说明已被后续批次覆盖；逐项状态以本文件下方各批与
`docs/field_trace.md` 为准。§13 见文末"未完成项"。

## 本轮续做

| 编号 | 状态 | 实现 / 证据 |
| --- | --- | --- |
| §12 async 候选预算按关系强度分配 | **已完成（本轮）** | 候选优先级 `direct_call > field_identity > type_scope > call_path > id_name`，同级 canonical ID 排序；`candidate_total` 保留截断前总数，`candidate_budget=200`，`partial`/截断诊断；`AsyncNode.candidate_reason`；值来源摘要只查 seed + 预算内候选；查询内符号缓存（含失败，每函数最多一次），不跨请求。 |
| §12 大输出 CLI 管道 | **已完成（本轮）** | `runCLIOut`/`runCLIOutErr` 运行期间并发排空 stdout/stderr，避免 200 候选大 JSON 与 pipe 缓冲区互等死锁（实测修复前 200s 超时）。 |
| §12 计数/正例 | **已完成（本轮）** | `integration/todo25_async_budget_test.go`：真实 init，`total>=212`、`budget=200`、`partial=true`、`nodes=201`、ZCaller=`direct_call`、ZConsume=`field_identity`、links 端点闭合、重复查询集合一致。`internal/action/action_async_budget_test.go`：220 弱候选夹具，`GetFunctionFields<=201`、`GetSymbol<=404`（缓存后实测远低于上限），候选 reason 齐全。 |
| §10.1 worktree/submodule 真实构建身份 | **已完成（本轮）** | `integration/todo25_build_identity_test.go`：父仓库 + 真实 `git worktree`（新提交 HEAD）+ 本地文件型 submodule + 无自身 `.git` 的相邻模块；连续 init 两次 + update，registry BuildID = build_metadata BuildID 且非空、两次 init 不同；HeadCommit 分别取 worktree/submodule 自身 HEAD、无 `.git` 模块为空；RegisteredAt 不变。 |
| §10.2 adapter timeout / late emit | **已完成（既有回归）** | `TestAdapterTimeoutDegradesBuild`（A 超时 DeadlineExceeded + B 节点保留 + status/metadata degraded/ErrorMsg 非空）与 `TestEmitAfterAdapterCancelRejected`（实际 FullBuild emit 包装器，取消后 emit 拒绝、节点不落库）。 |
| §10.3-F CPU profile 二次启动 | **已完成（既有回归）** | `cmd/codeintel/cpu_profile_test.go::TestStartCPUProfileFailureKeepsExisting`：第二次启动不停止/不清理既有采样，原 profile 仍可解码。 |
| §11 字段来源预算（跨包单例/构造器→getter） | **已完成（本轮）** | `action_seq_global_var.go`：函数体 `return <pkgVar>` → 包内全部赋值 → writer 形参沿 `init` 调用实参取具体类型（独立 8 层来源预算）；`receiverFieldImpls` 返回全部具体实现，多个写者保留候选 + `iface_ambiguous`，不再取第一个。真实 init：depth=1/8 均为 `(Impl).Run`，depth=8 展开 Sink；另一个实现写者/无 init 写入均不保留唯一 Impl。`integration/todo25_field_budget_test.go`。 |
| §5 枚举类型/跨包常量组合 | **已完成（本轮验收）** | `integration/todo25_scene_test.go`：`type Source int32` + 本包 `SourceA=7` + 跨包 `dep.B=19`/`AliasB` 类型转换；真实 init 后 `--scene Source=7` 只 Check1–4，`Source=19` Check1–7。 |
| §6 alias/const/var/依赖声明源码 | **已完成（核心 + 依赖 + 失败）** | `integration/todo25_source_decl_test.go`：Alias/B/Right 真实 init，整段 const/var 正文 + `selection_line_*`；索引无节点时 CLI 回退 `SymbolSource` 输出 `index_status=not_indexed`；离线 replace 依赖 `location=dependency`、绝对 file、不修改 go.mod/不生成 go.sum；未知符号非零退出且 stdout 为空。 |

## 第五批五项（本轮）

| # | 任务 | 状态 | 证据 |
| --- | --- | --- | --- |
| 1 | §2.5 入站回调上下文防环（独立真实 init 输入） | **完成** | `TestTodo25CycleInboundCallbackContext`：不同 Execute 回调都展开、Sink=1、cycle=0；递归同上下文 cycle=1、Sink=0。 |
| 2 | §4.3 同路径注册冲突保留歧义 | **完成（单元）** | `TestSeqHTTPOriginAmbiguous`：两个满足 origin 证明的注册 → 两条候选；`TestHTTPRouteIDsUniqueAcrossFiles` 保证同包不同文件 ID 不合并。 |
| 3 | §7.5 JSON/Mermaid 同一分析树 | **完成（复验）** | `TestTodo22FlowMatrixJSONMermaid` PASS：调用顺序逐项相等、条件结构存在。 |
| 4 | §9.1 八个同步流程 | **完成（复验）** | `TestTodo22SyncFlows` 16/16 子测试 PASS（Source=7/19 × 8 流程）。 |
| 5 | §9.2 二十个流程形态 | **完成（复验）** | `TestTodo22FlowForms` PASS；`TestTodo22SegmentedFlow` PASS。 |

## 第六批（§2.2–§2.4 核心边界）

| 任务 | 状态 | 证据 |
| --- | --- | --- |
| §2.2 多层装饰 `f = Wrap(f)` 与工厂副作用 | **完成（核心）** | `TestTodo25FuncValueBoundaries/decorate`：Wrap 的 Side 恰执行、Sink 执行、Never 不执行。 |
| §2.3 工厂/列表函数值在调用点展开 | **完成（核心）** | 同一用例：`Wrap` 返回原回调后在 `Use` 调用点展开；未调用函数体不执行。 |
| §2.4 `[]func` + append + range 顺序 / 空列表跨函数转发 | **已完成（本轮补齐分支矩阵）** | 单列表/append/空列表既有用例保持；新增 `seqRangeMultiListsValue`：条件分支 append/覆盖得到多候选列表时，**公共前缀**按确定执行、差异尾部按互斥候选分支展开（分支标 ambiguous），不再整段不展开。`TestSeqFuncValueConditionalAppend`（A/B 一次 + C/D 各一条歧义分支）、`ConditionalOverwrite`（无公共前缀 → A/B 各一条歧义分支）、`ConditionalListBudget`（公共前缀 >16 → 预算诊断）；真实 init 集成 `conditional-append`/`conditional-overwrite` 两例加入 `TestTodo25FuncValueBoundaries`。 |

## 第七批（§8 核心夹具断言）

| 任务 | 状态 | 证据 |
| --- | --- | --- |
| §8 第 8 节夹具逐项断言 | **完成（核心表 + IsRegionX/错误守卫）** | `TestTodo25CoreFixtureAssertions`：真实 init；Source=7 Check1–4=1/Check5–7=0，Source=19 Check1–7=1；Setup 一次；装饰链；Store.Insert→ExecContext；ApproveTrace 恰一次；bypass/非 bypass 两分支保留；Source=7 保留 IsRegionX 条件分支（不按名称猜地区）。 |

## 第八批（§10.3-A…G 保真/生命周期回归）

| 项 | 状态 | 证据 |
| --- | --- | --- |
| A 包缓存分块 round-trip | 完成（复验） | `TestPkgCacheChunkSizeLimit`、`TestPkgCacheChunkedMultiBlock`、`TestPkgCacheChunkIntegrity` PASS |
| B 截断/缺结束标记/旧格式未命中 | 完成（复验） | `TestPkgCacheTruncatedMidStreamRejected`、`TestArtifactCacheRejectsTruncatedAndMissingEnd` PASS |
| C 写缓存事务/回滚/容量/并发隔离 | 完成（复验） | `TestBuildWriteCache*`（Partition/SegmentedEviction/RebuildIsolation/Concurrent）PASS |
| D 路径/投影缓存失效与并发 | 完成（复验） | `TestCallFactsProjectionCacheHitAndInvalidation`、`TestProjectionConcurrentSingleLoad` PASS |
| E 嵌套/元素字段来源 | 完成（既有） | 既有 `fe_nested`/`fe_element` 用例保持通过 |
| F CPU profile 二次启动 | 完成（复验） | `TestStartCPUProfileFailureKeepsExisting` PASS |
| G 路由点查窄索引/幂等迁移 | 完成（复验） | `TestRouteProjectionPlanUsesIndex` PASS |

## 第九批（§7.1–§7.4 既有回归复验）

| 项 | 状态 | 证据 |
| --- | --- | --- |
| §7.1 接口候选缓存按方法身份 | 完成（既有复验） | `TestSeqIfaceImplCachePerRequest`、`TestSeqIfaceCacheMethodIdentity` PASS |
| §7.1 精确邻接查询/拒绝全扫 | 完成（既有） | `TestSeqIfaceExact*` PASS |
| §7.2 投影缓存/查询次数 | 完成（既有复验） | `TestCallFactsProjectionCacheHitAndInvalidation` PASS |
| §7.4 递归 SQL 取消 | 完成（既有复验） | `TestCalleesContextInterruptsRecursiveSQL` PASS |
| §7.3 跨仓同名接口方法/冷暖对照 | **已完成（本轮）** | 接口缓存键加仓库身份（`seqIfaceRepoKey`：`RepoAbs\x00ifaceID`，实现类型与方法级结果都按仓库隔离）；RPC 跨仓解析分支显式清 `ifaceImpls`（此前只清 `ifaceImplTypes`）。`TestSeqIfaceCacheRepoIsolation`：同一请求缓存在两个仓库间切换，同名 `(API).Insert` 各自得到 `StoreA/StoreB`，Missing 不借用；`integration/todo25_iface_repo_test.go`：两仓同名 `API.Insert/SaveService` 真实 init + workspace 外层请求，主仓/workspace 路径都只落到本仓实现，`HitSave` 零执行，冷暖两次查询控制流逐项一致。 |

## 第十批（§4.3 负例）

| 任务 | 状态 | 证据 |
| --- | --- | --- |
| Client.Do / 自定义 facade 不猜测 HTTP 目标 | 完成 | `TestTodo25HTTPEndpointAndWorkspaceBoundaries` 新增 Gateway（`http.NewRequest` + `Client.Do`）与 Facade（普通转发）查询：均不产生 `source=http_server` 节点。 |

## 第十一批（§9.3 全量回归）

| 任务 | 状态 | 结果 |
| --- | --- | --- |
| 全量 `make it`（真实 init/CLI/serve + todo22/24/25 集成套件） | 完成（本轮） | PASS，`integration` 62.677s，exit=0。 |
| §4.2 外部函数列表/ellipsis/参数重写 | **已完成（回归验证 + 固化）** | `TestSeqExternalFuncListBoundary`：外部 `RunAll(fs ...func())`（直接变参）、`RunList([]func())`、调用点 `list...` 展开、本包 `Forward(fs ...func())` 转发到外部变参四条路径元素内部调用各一次；外部实现 `f = func(){Wrong()}` 重写后传入回调 `Sink` 零执行、重写后的 `Wrong` 一次。前置缺口实测为夹具元素函数体为空（无可观察调用），分析路径本身已按外部形参绑定→普通 range 展开。 |

## 第十三批（§3.1/§3.3/§3.4/§4.1/§6/§8 剩余边界，2026-10-28）

| 任务 | 状态 | 证据 |
| --- | --- | --- |
| §3.4 工厂字段嵌入提升/闭包捕获/未知方法 | **完成** | `action_seq_factory_embed.go`：沿嵌入链解析 getter 与字段 writer（多条提升路径 >1 保守放弃）；`seqFactoryCallsWithClosures` 展开工厂内局部闭包（实参类型按形参对象映射）；非已知 writer 的 receiver 方法/实例逃逸 → 不采用字段证据。`TestTodo25FactoryEmbeddedPromotion/ClosureCapture/UnknownMethodRewrite`；`TestSeqFieldOwnerTypesMultiplePromotionPaths`（unit）。 |
| §3.1/§3.3 DI/AOP 剩余边界 | **完成（列出的边界）** | DI 注册扫描支持引导函数内局部闭包被顶层直接调用时递归扫描；字段注解沿嵌入字段在包内查找（多 key 冲突保守未知）；同一容器上的未知方法（带/不带 key 字面量）使注册失效并给 `why`。`TestTodo25DIClosureRegistration/DIEmbeddedFieldAnnotation/DIUnknownMethodRewrite`。AOP bypass（`ctx.Value`）沿用 §130 既有实现。 |
| §4.1 RPC 同行两服务 + 注册实参 DI/类型断言 | **完成** | 线号回退先按静态方法名唯一消歧，多同名仍未知（`TestSeqRPCForwardSameLineDisambiguation`）；AST 注册实参类型断言取来源表达式具体类型（`container.Get().(pb.XHandler)`），`TestTodo25MicroRegisterTypeAssertion`。 |
| §6 多模块包身份/标准库/失败矩阵 | **完成（本轮补齐）** | `TestTodo25SourceMultiModuleIdentity`（两个嵌套 module 同名 package 各自定位；用 A 的 import path 查 B 符号失败）、`TestTodo25SourceStdlibFailure`（标准库明确失败、stdout 空）；symlink 逃逸/路径穿越/缺文件由既有 `action_symbol_source_test.go` 覆盖。 |
| §8 Insert 错误路径后继精确断言 | **完成** | `TestTodo25CoreFixtureAssertions` 新增：`err != nil` 分支内无 `ApproveTrace`；`ApproveTrace` 位于该分支之后且其后仅 return（不是"全树出现一次"）。 |

## 未完成项

| 编号 | 状态 | 原因 / 下一步 |
| --- | --- | --- |
| §13 runtime snapshot（v1/v2、database 读取点、集合/分组） | **未开始（阻塞）** | 本会话交接文档只给出标题与三个短语；`todo25.txt` 不在仓库/临时目录，缺少输入/输出、读取点与分组规则的可断言定义。无法用猜测定义验收（按 DoD 不用"主要功能"替代）。接手方需拿到 §13 原文后按四段落地：① v1/v2 schema 与版本选择 ② database 读取点枚举 ③ 集合/分组规则 ④ 真实 init→CLI/服务化验收。 |

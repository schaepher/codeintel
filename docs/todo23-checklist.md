# todo23 交付 Checklist（对照 `origin/main` 1ffab0d）

输入：`todo23.txt`（14.4-B 剩余子项 + 后端流程自动续接与时序图 §1–§10）。
规则：按编号独立验收；未完成项保留原因，不用“主要功能完成”代表整项。
本轮（2026-10-27 接手）已完成 5 项、部分 1 项，其余按原清单保留。

## 本轮已完成

| 编号 | 事项 | 实现 / 证据 |
| --- | --- | --- |
| §2.5 | 防环身份含 repo/符号/**入站回调上下文** | `seqVisitKey` = repo + symID + `callbackCtx`；函数字面量/具名函数值进入时记录词法身份+绑定站点；`Entry→Execute(f1)→Middle→Execute(f2)→Sink` 两个 Execute 都展开、Sink 一次、0 cycle；`Again(f)` 同上下文递归 depth 16/24/48 均恰好 1 次截断、Sink 0。`action_seq_cycle_context_test.go`；field_trace §136。**部分**：固定预算/超限上下文合并诊断、真实 init→CLI 独立输入仍待补 |
| §4.1-1 | depth=1 前沿保留**方法 canonical ID** | 先解析注册/实现/方法（缺注册/多实现仍未知），再应用 depth；`Evidence.SymbolID=(Impl).Method`，不再用服务节点 ID；`todo22_micro_test.go` depth=1 断言更新；field_trace §137 |
| §7.1 | 接口缓存按仓库/方法身份隔离 | 缓存实现**类型**（`ifaceImplTypes[ifaceID]`）+ 方法级结果（`ifaceImpls[ifaceID\x00method]`）；Insert/SaveService 在同一请求返回各自 `(Store).Method`，Missing 不借用；`TestSeqIfaceCacheMethodIdentity`；field_trace §137。**部分**：跨仓同名反例/冷暖查询次数对照待补 |
| §10.1 | BuildResult / build_metadata / 台账共用 BuildID | `BuildResult.BuildID`；`finishBuild` 用同一 buildID 落库并回填；CLI/MCP 注册与刷新传 `result.BuildID`，HeadCommit 单独；同 HEAD 两次 init BuildID 不同、HeadCommit 相同、RegisteredAt 保留；无自身 `.git` 模块（父仓库下）HeadCommit 为空、BuildID 有效；worktree/submodule 的 `.git` 文件按 self-root 校验。`TestBuildResultExposesBuildID`、`registry_buildid_test.go`；field_trace §137。**未做**：真实 worktree/submodule 用例（代码已按 `--show-toplevel == repo` 判定） |
| §10.2 | adapter timeout / SSA worker 取消边界 | `acquireWorker`：取消前不取令牌；取到后再查 ctx，已取消立即归还；SSA 发射块改用它。init/update 显式 `--adapter-timeout 0|-1s` 在库操作前报错（exit 2）；已有 adapter 超时 degraded + late emit 截断用例继续通过。`worker_acquire_test.go`、`adapter_timeout_init_test.go`；field_trace §137 |
| §10.3-A…G | 既有能力保真/生命周期自动回归 | **审计：多数已有覆盖** — A `TestPkgCacheChunkSizeLimit`/`TestPkgCacheChunkSizeLimit`/round-trip；B `TestPkgCacheTruncatedMidStreamRejected`/`TestArtifactCacheRejectsTruncatedAndMissingEnd`；C `TestBuildWriteCache*`/`TestResolveNodeRefsRollbackDoesNotPublish`；D `TestCallFactsProjectionCacheHitAndInvalidation`/`TestProjectionConcurrentSingleLoad`；E `fe_nested_test.go`/`fe_element_test.go`；G `TestRouteProjectionPlanUsesIndex` + 幂等迁移。**F（CPU profile 二次启动）未覆盖**：仓库当前无 CPU profile 启动入口，保留待办（需要先有该能力再锁语义）。 |

## 本轮未开始（按原文保留）

| 编号 | 事项 | 现状 / 下一步 |
| --- | --- | --- |
| 14.4-B | 短查询临时文件峰值覆盖 | **已完成**：子进程内查询生命周期采样（QueryContext 前后快照 + 250µs 轮询 `/proc/self/fd`，含 ` (deleted)`）；`temp_events` 记录 create/grow/unlink/close；可控夹具 4KB→64KB→unlink 持有→关闭，断言峰值精确 65536；无临时文件短查询为有效 0；全量重跑 66/66 `child_query_poll`、unavailable=0、12 个深样本峰值>0、边数/摘要/exit=0/取消契约全保持（1771.71s）。见 benchmarks/walk-resource 与 field_trace §138 |
| §1 | 夹具/证明边界/实施顺序 | 已有 `example.com/flow`/`tx` 夹具（todo22）；新增 §2.5 独立输入/其它反例待补 |
| §2.1 | 统一捕获接口/对象覆盖表 | todo22 已完成函数值环境；本轮把对象化推广到实现候选（todo22 P1）；§2.1 本身无新增缺口 |
| §2.2 | 嵌套实参工厂/平行赋值/多层转发反例 | 基础能力已有；缺本轮要求的新反例矩阵（同名阴影/平行赋值/多层转发真实 CLI） |
| §2.3 | 返回闭包捕获快照/失效传播 | 部分（todo22）；创建后修改/取址/逃逸正反例实际输出待补 |
| §2.4 | TypeSpec/别名/跨包 Named 列表、路径预算诊断 | **部分完成**：命名函数类型/类型别名/跨包导入（`type Check func()`、`type Alias = Check`、`[]dep.Check`）识别，别名环保守 false；路径 ≤16/每条 ≤32 预算已有。测试 `action_seq_functype_test.go`。**剩余**：更多列表形态反例矩阵、只读空循环后跨函数转发的新增用例 |
| §3.1 | 构造器/全局单例/工厂选择器/字段身份 | 部分（receiver 字段/DI/局部变量）；导入包全局单例、对象身份约束、同行多包调用待补 |
| §3.2 | 候选预算/前沿 ID | todo22 已有 12 候选/两层展开；直接查询接口方法根入口等边界待验 |
| §3.3 | 字段 Inject 精确 container/key/AOP bypass 原实现 | todo22 + 本轮 AOP bypass 场景；框架实际注入契约（注释/tag 分别）待补 |
| §3.4 | 工厂返回实例的 switch/setter/getter 续接 | 未实现（One/Two/Box/Build 夹具与反例矩阵全待做） |
| §4.2 | 外部事务函数列表/ellipsis/重写失效 | 普通回调已实现（todo22）；列表/ellipsis/参数重写失效待补 |
| §4.3 | HTTP origin 跨模块续接 | 未实现（`http_server` 证据、HandleFunc/ListenAndServe 证明、反例矩阵） |
| §5 | 整数转换范围校验/内建遮蔽/短路求值顺序 | **已完成（核心）**：内建整数转换按 big.Int 范围校验（int8(256)/uint8(-1)/int64 溢出 → unknown）；仅 `Ident.Obj==nil` 的未遮蔽内建名才当转换/解包；导入别名被局部变量遮蔽时不当包常量；`unknown || taken` 保持 unknown（未知调用可能改写变量）。跨包常量/字段传播沿用。测试 `action_scene_num_test.go`。**剩余**：枚举类型/字符字面量组合的真实 init→CLI 全覆盖（既有 §8 用例仍通过） |
| §6 | struct/interface/alias/const/var 源码 + 依赖 location | **已完成（核心）**：`matchAnySymbolSourceDecl` 支持函数/方法/struct/interface/type alias/const/var；const/var 输出整块 + `selection_line_*`；type 单 spec 带 `type` 关键字；无图节点 → AST kind + `index_status=not_indexed`；依赖虚拟符号离线 `go list` 定位（location=dependency、绝对 file、不改 go.mod）；CLI 文本/help 同步；旧函数/方法契约与失败矩阵保持（struct 反例已改为正例）。测试：`action_symbol_source_ext_test.go`、`TestSymbolSource*`、`TestTodo22SymbolSourceRealCLI`。**剩余**：多模块包身份错配/标准库/不唯一边界反例矩阵、真实 init→CLI 的 const/var/alias/dependency 全链路（单元+依赖定位已覆盖） |
| §7.2 | context 贯穿 SQL/源码/递归、资源释放 | todo21 已有取消；本轮无新增 |
| §7.3 | 跨文件全局变量/守卫单例/DI 契约缓存、跨仓隔离 | **部分完成**：变量链回溯按 AST 声明对象过滤（同名局部变量不互相借用；缓存键含对象身份），修掉"文件内不同函数同名 worker 互相污染"的缺陷（`TestVarChainRespectsObjectIdentity`）。**剩余**：同包兄弟文件写者/导入全局单例/守卫单例/DI 契约复用与跨仓反例 |
| §8 | 完整脱敏核心夹具独立断言表 | todo22 夹具已有；本轮新增 §2.5/§4.1 等输入需并入 |
| §9.1/§9.2 | 八流程/二十流程包装链补齐 | todo22 已有验收；本轮要求“非空装饰器确定执行证明/包装链无 func_value_unbound”仍待补 |
| §9.3 | 最终输出与回归 | 待 §9.1/§9.2 补齐后重跑 |
| §10.3-A…G | 保真/生命周期自动回归 | 未开始（pkg cache 截断、写缓存事务、路径缓存失效、profile、索引计划等） |

## 验证（本轮实际执行）

- `go test ./internal/action ./internal/cli ./internal/infrastructure/ssa ./internal/orchestrator`：PASS（每个 commit 前）
- `bash scripts/verify.sh --quick`：PASS（pre-commit 自动执行）
- `bash scripts/verify.sh --quick` 增量 + `make it`：待本轮收尾统一重跑（见提交记录）
- 新增/更新用例：`TestCycleCallbackContextDistinct`/`TestCycleSameCallbackContextTruncated`、`TestSeqIfaceCacheMethodIdentity`、`TestBuildResultExposesBuildID`、`TestBuildIDMatchesMetadataAndRegistry`、`TestBuildIDWithoutGit`、`TestAcquireWorker*`、`TestInitUpdateRejectNonPositiveAdapterTimeout`、`TestTodo22MicroRPC`（depth=1 断言更新）

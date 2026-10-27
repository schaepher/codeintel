# todo22 交付 Checklist

对应输入：`todo22.txt`（两块任务）
- **A. 14.4-B**：递归查询资源基准、峰值采集与结果校验
- **B. 后端流程自动续接与时序图生成**（第 1–9 节）

规则：每项独立列出验收点与证据；未完成项保留原因，不用"主要功能完成"代表整项。

---

## A. 14.4-B 递归查询资源基准（已完成，2026-10-27）

### A.1 交付物

| 交付物 | 位置 |
| --- | --- |
| 基准编排 + 夹具 + 样本校验 | `internal/infrastructure/sqlite/repo_walk_bench_test.go` |
| 子进程（每次一查询、独立进程） | `internal/infrastructure/sqlite/repo_walk_bench_child_test.go` |
| 峰值采集（gnu time / `/proc` fd 采样） | `internal/infrastructure/sqlite/repo_walk_bench_proc_test.go` |
| 汇总聚合（summary.json） | `internal/infrastructure/sqlite/repo_walk_bench_summary_test.go` |
| 人读汇总（summary.md） | `internal/infrastructure/sqlite/repo_walk_bench_report_test.go` |
| 生产 SQL 复用（基准不复制 SQL） | `repo_walk.go` 的 `walkDirectQuery` / `walkRecursiveQuery`（`repo_walk_compat_test.go` 的旧递归对照也改用同一构造器） |
| 实测交付记录 + 原始数据 | `docs/benchmarks/walk-resource/README.md` + `samples.jsonl` / `summary.json` / `summary.md` / `run.log` |

### A.2 逐项验收

| # | 要求（todo22 A 节） | 实现 | 证据 |
| --- | --- | --- | --- |
| 1 | 独立子进程运行的递归查询基准，复用现有递归参考 SQL 与生产 SQL | 父测试 `TestWalkEdgesResourceBench`（env 开关）逐样本拉起 `<test binary> -test.run=^TestWalkBenchChild$`；三条 SQL 来自生产构造器（`recursive`=`walkRecursiveQuery(...,false)`、`materialized`=`...true`、`direct_one_hop`=`walkDirectQuery`） | `samples.jsonl` 66 条样本，`variant` 三值；`repo_walk_bench_test.go:benchVariantsFor` |
| 2 | 查询前 RSS、进程峰值 RSS、查询期临时文件峰值采集 + 失败报告 | `rss_before_bytes`=子进程 Query 前 `VmRSS`；`max_rss_bytes`=`/usr/bin/time -v`（父侧），回退 `VmHWM` 并标 `max_rss_source`；`temp_peak_bytes`=父进程 5ms 采 `/proc/<pid>/fd`（含已 unlink 仍打开的临时文件） | 失败分支写 `"unavailable"` + `max_rss_note`/`temp_peak_note`；depth=1 短查询实测即 `unavailable`（原因：查询窗内无采样点） |
| 3 | 结果摘要、条数与取消契约自动校验；逐样本数据 + 对照汇总 | `verifyWalkBenchSamples`（样本齐全/期望边数/每个 (N,depth) 组摘要唯一）；`verifyWalkBenchCancelContract`（depth=1e6 + 25ms → `DeadlineExceeded`、<1s、同连接复用 3 条边）；汇总见 `summary.json`/`summary.md` | 66 样本 exit_code=0；摘要 10 组全部唯一；取消实测 `elapsed=26.2ms`、`reuse_edges=3` |
| 4 | 一条完整运行命令 + 实测交付记录 | 命令与参数在 `docs/benchmarks/walk-resource/README.md` 首节；原始 JSONL 落库 | 本次运行 `1282s`、66 样本、`PASS` |

### A.3 实现要求逐条核对

| # | 要求 | 落地 |
| --- | --- | --- |
| 1 | 每组 ≥3 次、串行；记录 Go/驱动/SQLite 版本、GOMAXPROCS、temp_store、cache_size、机器内存、样本顺序、冷暖 | `REPS` 默认 3；父进程串行 `runBenchSample`；`meta`（`summary.json`）含全部版本/pragma/`MemTotal`；样本有 `order` 与 `sample_class`（`cold_first_query`/`warm_repeat`） |
| 2 | 先建图再让子进程查询；建图/打开/查询/取完结果/关闭分别计时；查询边界含全部 `Rows.Next/Scan` | 夹具由父进程 `SaveBatchStats` 建好并断言 nodes=N/edges=3N；样本字段 `build_ms`（父）/`open_ms`/`query_ms`/`close_ms`（子）；`query_ms` 边界 = `QueryContext`→`scanFacts`完成→`rows.Close()` |
| 3 | 三变体同一构建配置；不用 `-N -l` 调试二进制；不把进程总 RSS 冒充查询增量 | 同一测试二进制、同一 DSN、同一进程启动方式；`rss_before_bytes` 与 `max_rss_bytes` 分开记录，比值口径写在 README「测量边界」 |
| 4 | 记录峰值 RSS、查询前 RSS、临时文件峰值字节与采样间隔；覆盖已 unlink 文件 | `max_rss_bytes`/`rss_before_bytes`/`temp_peak_bytes`/`temp_sample_interval_ms` 每样本都有；fd 目标前缀匹配 TMPDIR（`... (deleted)` 仍 stat 到大小） |
| 5 | 平台无法可靠取得 → `unavailable` + 原因，不写 0；0 必须在确认覆盖查询生命周期后才是有效观测 | `benchMeasure` 的 JSON 值是数字或字符串 `unavailable`；0 只在采样点落在**查询时间窗**（子进程上报 `query_start/end_unix_nano`）时成立，否则记 unavailable 并说明 query_ms < 采样间隔 |
| 6 | 每组按六列排序算摘要；原递归与物化逐项一致 | `benchFactsDigest`（source|target|kind|tool_source|conf|归一化 metadata 排序后 SHA-256）；`verifyWalkBenchSamples` 断言每组只有一个摘要 |
| 7 | 不修改生产 SQL/索引/schema/超时/业务图；样本失败保留现场并标失败 | 生产改动仅**抽出 SQL 构造器**（语句文本不变，`repo_walk_compat_test.go` 与既有 `TestWalkEdges*` 全部通过）；失败样本保留 `run-*`（result/pid/time/child.log）、`error`+`error_dir`，测试 `t.Errorf` 且组内样本数不足会失败——不"跳过即通过" |

### A.4 期望输出对照（全部命中）

| N | depth=1 | depth=2 | depth=8 | depth=32 | depth=128 |
| --- | ---: | ---: | ---: | ---: | ---: |
| 256 | 3 | 12 | 66 | 282 | 768 |
| 4096 | 3 | 12 | 66 | 282 | 1146 |

### A.5 交付命令验证（实际执行结果见本节末）

```sh
go test ./internal/infrastructure/sqlite -run 'TestWalkEdges|TestCallersCalleesDepthAndConfidence|TestCalleesContextInterruptsRecursiveSQL' -count=1 -timeout=30s
go test -race ./internal/infrastructure/sqlite -run 'TestWalkEdges|TestCalleesContextInterruptsRecursiveSQL' -count=10 -timeout=60s
bash scripts/verify.sh --quick
make it
```

| 命令 | 结果 |
| --- | --- |
| 基准本体（`CODEINTEL_WALK_BENCH=1 ... -run '^TestWalkEdgesResourceBench$'`） | PASS，1282s，66 样本 |
| 三条语义回归（`TestWalkEdges*` / `TestCallersCalleesDepthAndConfidence` / `TestCalleesContextInterruptsRecursiveSQL`） | PASS |
| `-race -count=10`（`TestWalkEdges` / `TestCalleesContextInterruptsRecursiveSQL`） | PASS |
| `bash scripts/verify.sh --quick` | PASS |
| `make it` | PASS |

### A.6 明确未验

- 原始 TODO 提到的"约 250 万节点业务仓库 61s CLI 延迟"未复现；本夹具是 SQL
  资源机制夹具（3N 条边），绝对值不可外推。
- 只测 callees 方向、单锚点、calls 边、conf≥0.8；callers/其他方向未纳入本轮矩阵。
- 未 drop OS page cache（无权限），"冷/暖"只表示该库本轮首个查询进程。

---

## B. 后端流程自动续接与时序图生成（进行中）

### B.1 现状核对（2026-10-27，基于代码检索）

| 节 | 事项 | 现状 |
| --- | --- | --- |
| 1 | 夹具/证明边界/实施顺序 | 部分：todo12–21 已有 `example.com/seqbudget` 等真实 init→CLI 集成夹具；todo22 第 8 节的 `example.com/flow` + `example.com/tx` 夹具**未建** |
| 2.1 | 闭包捕获统一接口（`captureSeqClosureValue/Args/CallbackLists`）、函数值解析/到达定义 | **已完成（2026-10-27，B.6）**：三个统一入口 + 绑定元数据（声明对象/上下文/条件/包/receiver/词法文件与 AST）+ 方法值；见 B.6 |
| 2.2 | 函数字面量/IIFE/`defer`/`go`/回调形参绑定/赋值先 RHS 后 LHS | **1/2/3/5 已完成 + 4 已支持普通/具名回调**（见 B.4）；返回函数值（工厂）与具名返回值待 B.5 |
| 2.3 | 工厂返回函数、条件返回、捕获值、证明失效 | **已完成（2026-10-27，B.5）**：按被调函数的 return 表达式解析（具名/字面量/传入回调），多 return → 未消歧多候选；见 B.5 |
| 2.4 | `[]func(...)` 命名函数列表、range 折叠装饰器、预算 16 路径×32 项 | **已完成（2026-10-27，B.5）**：复合字面量/append/变参列表、range 按元素顺序折叠、空列表确定、预算诊断；见 B.5 |
| 5 | 场景推导（整型/枚举/跨包常量/短路/取反） | **同包已完成（B.5 补强）**：整型/浮点/字符字面量、包内常量（含 `Source(7)` 转换）、内建整数转换、`&&`/`||` 短路、`!` 取反；**跨包常量未解析**（保持 unknown） |
| 3.1 | 到达定义/精确目标/同名不同包/行号回退核对 | 部分：`action_seq_dataflow.go`（receiver 字段写入溯源）、`action_seq_targets.go:matchAggregated`（todo20-9）+ 本轮新增**实参位置调用的 AST 对象身份恢复**（`seqASTCallTarget`，不做名称模糊搜索） |
| 3.2 | 接口候选全集 + `candidate_depth`/`candidate_budget` 截断诊断 | **已完成（2026-10-27，B.7）**：多候选不选第一个展开、候选上限 12 + 截断说明；见 B.7 |
| 3.3 | DI/AOP（Bean/Inject 注解、container/key 注册、事务 wrapper 内原实现） | **未实现** |
| 4.1 | go-micro `NewRequest/Call` + 服务注册续接、跨仓 workspace 限定 | **未实现**（仅有 gRPC 方法入口 ID 约定 `GrpcMethodEntryID`） |
| 4.2 | 外部依赖源码按需解析（`go list -e -json -find -mod=readonly`、离线、缓存） | **未实现** |
| 5 | 场景推导（整型/枚举/跨包常量/短路/取反） | **已实现**（`action_scene*.go`，含 `Source=7/19` 与 `int32(...)` 场景测试） |
| 6 | `SymbolSource` + `query symbol --include-source` | **已完成（2026-10-27）**，见 B.3 |
| 7.1 | 接口候选精确邻接查询（不 Expand/不加载全量 implements） | 部分：`InterfaceMethodImpl` 单符号查询已存在；Reader 精确接口查询 + 拒绝全图扫描的包装器测试**未实现** |
| 7.2 | 路由投影走已有索引 + EXPLAIN 计划测试 | 部分：`repo_packages_plan_test.go` 已锁包清单计划；路由投影计划未覆盖 |
| 7.3 | 请求内缓存（工厂来源/字段类型/DI/接口候选/AST） | 部分：`action_seq_*` 有请求级缓存设施（需按本节逐项补齐 DH/工厂/AST） |
| 7.4 | 取消贯穿 SQL/源码定位/递归 | **已实现**（todo21-14.4/14.5，含物化 CTE 与预算记录） |
| 7.5 | JSON/文本/Mermaid/SVG 同源 + 证据/身份可核对 | 部分：todo19/20/21 已做重复调用点/证据/矢量图；统一分析树与 `*_projection` 诊断已实现，新增能力须继续遵守 |
| 8 | 第 8 节完整脱敏夹具与逐项断言表 | 夹具**已建**（`integration/flowfixture_test.go`，逐字照抄 §8 源码，供 §6 与后续 §2/§9 共用）；逐项断言表待 §2–§9 完成 |
| 9.1 | 八个同步流程（SourceA/B × 新建/新增子项/编辑子项/编辑实体） | **未建** |
| 9.2 | 二十个后续流程形态 + 审核 finish 次数规则 | **未建** |
| 9.3 | 最终输出（逐行验收、JSON+Mermaid+SVG、全量回归） | **未开始** |

### B.2 计划（按 todo22 第 1 节顺序，逐项交付、每项独立提交）

1. **第 8 节夹具**（`example.com/flow` + `example.com/tx`，真实 init → CLI 查询）——
   后续所有断言的共同基座。
2. **第 6 节 `SymbolSource`**（自包含、契约明确）：action + CLI opt-in + 失败矩阵测试。
3. **第 2 节闭包/函数值**（2.1 统一上下文 → 2.2 执行位置 → 2.3 工厂返回 → 2.4 列表折叠）。
4. **第 3.2/3.3**（候选预算 + DI/AOP）→ **第 4 节**（go-micro / 外部源码）→ **第 7 节**补齐。
5. **第 9 节**：八个同步流程 → 二十个流程形态 → 最终输出。

每项完成时在本文件追加「实现 / 正反例 / 实测命令与输出」小节，并同步
`docs/field_trace.md`。

### B.3 §6 准确符号源码输出（已完成，2026-10-27）

**契约**：`query symbol <符号> --include-source [--json]` 在原输出的基础上追加

```json
"source": {"symbol_id": "...", "file": "flow.go",
           "line_start": 15, "line_end": 18, "text": "<原始声明全文>"}
```

不加参数时输出契约不变；失败时 CLI 非零退出、stdout 为空（不输出部分 JSON）。

| 要求（§6） | 实现 |
| --- | --- |
| action 层 `SymbolSource` | `internal/action/action_symbol_source.go`（`SymbolSourceRequest{RepoAbs, SymbolID}` → `*SymbolSource`） |
| CLI opt-in 参数 + 原契约不变 | `internal/cli/query.go`（`includeSource`）、`query_flags.go`（`--include-source`）、`query_symbol.go`（JSON 追加 `source`，文本追加"源码: file:start-end" + 全文）；`root.go` 帮助 |
| text 为原始声明全文，行号为 AST 实际位置 | `go/parser` 解析后用 `fset.Position(fd.Pos()/End()).Offset` 直接切源文件字节；**不使用**索引里的 `LineStart`（测试把节点行号写成 999 仍返回正确行号） |
| 按 canonical ID 精确取符号 | 只接受 `symbol:go:<import_path>:<name>`，走 `repo.GetSymbol` 精确查询——**不走** ResolveSymbol 的模糊名称搜索；非 canonical 输入直接失败 |
| 匹配 AST 函数名与 receiver | `matchSymbolSourceDecl`：函数名 + receiver 基名（剥 `*`/`(T)`/`T[P]`/`T[P,Q]`）；匹配不到/多重声明都明确失败 |
| 指针与泛型 receiver | `recvBaseName` 处理 `*T`、`T[P]`、`(T)`；单测覆盖 `(A).Run`/`(B).Run`/`(Box[T]).Get` |
| 校验最近 go.mod 的包身份与嵌套 module | `nearestGoMod` 从文件目录向上找 `go.mod`（到仓库根为止），用 `module` 路径 + 相对目录还原 import path 与 ID 比对 |
| 拒绝仓库外路径及 symlink 逃逸 | `secureSymbolSourceFile`：拒绝绝对路径/`..`，`EvalSymlinks` 后 `filepath.Rel` 必须仍在仓库内 |
| 缺失文件/虚拟节点/不支持种类/解析失败/包或 receiver 不匹配/多重声明分别失败 | 11 条反例矩阵（`TestSymbolSourceFailures`），每条断言错误关键词与"不返回部分结果" |
| 测试同名不同 receiver、陈旧行号、全部失败输入 | `internal/action/action_symbol_source_test.go`（4 正例 + 11 反例） |
| 真实源码 init → CLI（§1 要求） | `integration/todo22_symbol_source_test.go`：真实 `init`（`example.com/flow` + `example.com/tx`）→ `--include-source --json`，断言 §6 期望的 `source` 五字段逐字节相同（15–18 行、原样 4 空格文本）；另断言无参数时无 `source` 键、struct 节点失败时 exit≠0 且 stdout 空 |

**验证命令**

```sh
go test ./internal/action -run 'TestSymbolSource' -count=1
go test -tags integration ./integration -run 'TestTodo22SymbolSourceRealCLI' -count=1 -v
```

两项均 PASS（集成用例 1.34s，含真实 `init`）。

### B.4 §2 第一增量：函数值/闭包/回调执行证明（已完成，2026-10-27）

**范围**（用户确认的语义）：字面量只建值、在**实际调用处**展开；回调按调用点
数据流证明（实参→形参绑定），证据记录绑定/调用位置与依据；确定不执行的不画，
可能执行但证据不足的保留明确 unknown。

| 要求 | 实现 / 证据 |
| --- | --- |
| 2.2-1 字面量只建值、调用处展开；未调用的字面量/回调不产出内部调用 | `collectCalls` 不再穿透 `FuncLit`；`seqArgCallNodes` 也跳过字面量体。测试：`NeverCalled`/`ArgLit` 零节点 |
| 2.2-2 IIFE 执行一次；嵌套实参按 Go 求值顺序 | 调用节点先出实参调用（`seqArgCallNodes`），`seqCallFuncValue` 把 `call.Fun` 为字面量的情形当 IIFE。测试：`Iife` 恰 1 次、`Factory` 节点在 `Use` 之前 |
| 2.2-3 `defer`/`go` 保留延迟/异步语义 | 沿用 `Exec=defer/async`（本轮未改；实参调用在声明处求值，与 Go 一致） |
| 2.2-4 回调实参绑定到实际形参，形参调用处展开；支持参数/返回值/具名返回 | `bindSeqParams`（按位置；未绑定 → 显式 unknown）+ `seqExpandSingleValue`（字面量内联/具名函数走 `codeSeqForSymbol`）+ 闭包环境链（父环境=创建时环境） |
| 2.2-5 赋值先 RHS 后 LHS；调用后赋值不倒推 | `recordSeqAssign` 在调用节点生成**之后**执行；测试：`g = func(){Latest}` 后调用 → 只执行 `Latest`，旧 `Overwritten` 不执行 |
| 条件覆盖多值未消歧 | `IfStmt` 分支各走一份 `seqEnv` 副本并 `mergeBranches` → multi（证据 `ambiguous=true` + candidates），两个候选分别展开；循环 `mergePossible`（体可能 0 次） |
| 阴影/对象身份 | 环境按 `*ast.Object` 索引（同名不同作用域不串） |
| 回调逃逸 | 未解析/外部调用里出现函数值参数（值已知或**声明为 func 类型**）→ 调用节点 `Stop.Detail` 标注"逃逸（无法确定是否/何时被调用）"，不展开 |
| 有调用点但值无法确定 | `seqValueUnknown` → 节点 `Stop.Code=func_value_unbound`、证据 `ambiguous=true`，无子节点 |
| 实参位置调用的准确目标 | 索引里没有 calls 边（适配器只对非实参调用建边）→ `seqASTCallTarget` 按 AST 对象身份/import 路径恢复精确符号（同包或已索引包；外部库保持 `external_target`） |

**验证**

```sh
go test ./internal/action -run 'TestSeqFuncValue' -count=1
go test -tags integration ./integration -run 'TestTodo22' -count=1 -v
```

- 5 个单测：字面量/IIFE/覆盖顺序/条件多候选/求值顺序、回调绑定链
  （Entry→Run→f()→Delivered）、未绑定 unknown、逃逸、具名函数值。
- 真实 CLI（§8 夹具）：`tx.Run` 内 `f(ctx)` 带 `closure` + `callback-binding`
  证据，`process(inner)` 在变参/工厂未落地前是**未消歧多候选**（`ambiguous`），
  `store.Insert` 只作为候选出现——不冒充确定链路。

**未做（下一增量 B.5）**：工厂返回函数（`MakeFirst(ctx)` → `First`）、
条件返回、变参/`[]func` 列表与 range 折叠（`Execute` 的装饰器链）、
预算 16 路径×32 项。这三个能力落地后 §8 的
`tx.Run→BeforeSecond→BeforeFirst→Store.Insert→AfterFirst→AfterSecond` 才能
成为确定链路。

### B.5 §2.3/§2.4 + §5 补强：工厂返回 / 函数列表与折叠 / 场景常量（已完成，2026-10-27）

| 要求 | 实现 / 证据 |
| --- | --- |
| 2.3 工厂返回（具名/字面量/传入回调） | `seqCalleeReturnValues`/`seqValueReturnValues`/`seqReturnValuesOf`：调用结果按被调函数 **return 表达式** 求值（具名函数/字面量/形参传递）；函数值来自实参绑定，闭包捕获被调函数帧的环境 |
| 2.3 条件返回 | 多条 return → `multiSeqValue` 未消歧多候选（证据 `ambiguous` + candidates），两候选分别展开，不任选 |
| 2.3 证明失效 | 未知调用改写/未绑定 → `unknown`（`func_value_unbound`）；不做名称猜测 |
| 2.4 `[]func(){...}` / `append` / 变参 | 复合字面量→列表；`append(列表, …)` 扩展；变参形参收集剩余实参为列表（无实参=确定空列表）；`f(list...)` 摊平 |
| 2.4 range 折叠 | `seqRangeOverFuncList`：按列表顺序逐项展开（迭代上下文绑定元素），`process = wrapper(ctx, process)` 每次迭代用**迭代开始**的值（RHS 先求值）→ 两层装饰器折叠为嵌套闭包 |
| 2.4 空列表 | `var l []func()` 与 0 元素字面量都是**确定空列表**：range 零次、不执行赋值、不污染被包装回调 |
| 2.4 预算 | 元素 >32 → 明确诊断（不截取前几项）；迭代 >16 → 明确诊断；多候选列表（条件覆盖下的多个列表）→ range 不按顺序展开并给出诊断 |
| 5 场景（§8 用例） | `int32(req.Source) == int32(SourceB)` 用常量表 + 内建转换 + 数值比较判定：Source=7→excluded（4 个 check）、Source=19→taken（7 个）；`req.Source == SourceA && IsRegionX()` 在 Source=19 时短路排除、Source=7 时保持 unknown（IsRegionX 无运行时事实） |

**实测（§8 夹具，真实 init → CLI）**

- `Source=7`：`checks` 列表 4 项 → Check1–4 各 1 次，Check5–7 0 次；
  `Source=19`：7 项 → Check1–7 各 1 次（按 `evidence.symbol_id` 计数，不按标签）。
- 装饰器折叠：`process(inner)` 为未消歧多候选，**两条路径分别保留**：
  - bypass：BeforeFirst → f → store.Insert → ExecContext → AfterFirst（无 Second 前后）
  - 非 bypass：BeforeSecond → BeforeFirst → f → store.Insert → ExecContext → AfterFirst → AfterSecond
- `MakeFirst` 的副作用 `Setup` 在执行 `Execute` 前恰一次；工厂返回闭包在创建时**不被执行**。
- 证据：`closure` + `callback-binding-*`/`func-list`/`func-value-multi`。

**验证**

```sh
go test ./internal/action -run 'TestSeqFuncValue|TestScene' -count=1
go test -tags integration ./integration -run 'TestTodo22|TestTodo15' -count=1
bash scripts/verify.sh --quick && make it
```

**未做（下一增量）**：§2.1 的显式统一接口（`captureSeq*` 命名）、方法值（`x.Method`）、跨包常量、§3.3 DI/AOP、§4 RPC/外部源码。

### B.6 §2.1 收口：统一捕获入口 + 绑定元数据 + 方法值（已完成，2026-10-27）

| 要求 | 实现 / 证据 |
| --- | --- |
| 统一 `captureSeqClosureValue` / `captureSeqClosureArgs` / `captureSeqCallbackLists`，所有调用点（普通/外部/未解析分支/测试）都带显式 `*Actions` | `action_seq_capture.go`：三入口即唯一取数路径；`newSeqCallNode`（含接口/字段/局部变量/grpc/外部/未解析分支）、`recordSeqAssign/Decl`、range 元素、工厂 return、逃逸判定全部改走它们（`rg 'seqValueOfExpr\(' ` 仅剩 capture 内部实现） |
| 函数绑定保存词法文件/AST/包/receiver/声明对象/调用上下文/条件 | `seqValue` 增加 `declObj`（声明对象）、`bindFile`/`bindLine`（绑定站点）、`cond`（分支条件）；`ctx`（fset+AST+源码+相对路径）、`pkgPath`、`recvType/recvDecl`、`enclosing` 早已随值保存；`seqStampBinding` 统一盖章（条件也盖到候选上） |
| 按 AST 对象身份区分形参/局部变量/阴影 | 环境 `vals`/`types` 都以 `*ast.Object` 为键；测试：内层 `f := …B` 阴影只执行 B、外层执行 A |
| 临时覆盖表区分"没有覆盖"与"明确未知" | `seqEnv.lookup` 沿闭包父链找"没有覆盖"；覆盖为不支持的值 → 显式 `unknownSeqValue(原因)` 存在当前层，**不会**回退到父/旧绑定；测试：`f = Mutate()` 后 `f()` 为 `func_value_unbound` 且旧字面量的 A 不执行 |
| 方法值 `x.Method`（receiver 身份） | `seqMethodValue`：接收者类型来自**同一声明对象**的类型数据流（`&T{}`/`T{}`/构造器返回值），符号必须真实存在于索引；测试：`f := s.Run; f()` 与 `Use(s.Run)` 都解析到 `(S).Run` 并展开其函数体 |
| 不保留返回 nil 的占位 / 未接入字段 | 值一律非零（unknown 带原因）；req 字段 `env/paramValues/curTgts/curCtx/curRecvType/curRecvDecl/litVisited/tgtsCache/branchCond` 全部接线；自引用工厂用深度预算终止（不再栈溢出） |

**顺带修的坑**：`callReturnTypes` 递归构造器时不递减 depth（`func F() func(){return F()}`
会无限递归 → 栈溢出）——现按预算递减，并加回归 `TestSeqCaptureRecursiveFactory`。

**验证**：`go test ./internal/action -run 'TestSeqCapture' -count=1`（方法值/未覆盖 vs 明确未知/阴影/条件候选带条件/自引用工厂）、`TestSeqFuncValue*`、`TestScene*`、集成 `TestTodo22*`、`verify.sh --quick`、`make it` 全绿。

### B.7 §3.2 接口候选与前沿（已完成，2026-10-27）

| 要求 | 实现 / 证据 |
| --- | --- |
| 单一已证明实现可展开；多实现保留完整候选集合，不选择第一项 | `newSeqCallNode` 接口分支：候选 >1 时**不展开、不设 ImplType/SymbolID**，`Evidence.Candidates` 保留全集 + `ambiguous=true` |
| 未验证候选最多展开两层、最多 12 个实现；输出 `candidate_depth`/`candidate_budget` 截断原因 | `seqMaxIfaceCandidates=12`，`capSeqCandidates` 截断 + `seqIfaceBudget` 文本（`impls=12（候选深度 0=未展开）` / `impls>12（已截断…）`）；`SeqEvidence.CandidateDepth`/`CandidateBudget` 字段 |
| depth=1 时仍输出选定实现 ID | 已证明单实现路径不受 depth 影响（先具体化再展开，depth 只影响是否继续展开） |
| 无法消歧的接口保留未知及候选，不制造具体实现 | 新停止码 `iface_ambiguous`（`SeqStopIfaceAmbiguous`），节点无子节点；场景/到达定义未消歧分支同样标 `candidate_budget` |
| 期望：无证据时两个同名实现在候选里、确定序列中都不出现；有准确构造证据只选对应实现 | 测试 `TestSeqIfaceCandidatesNotExpanded`（OnlyA/OnlyB 都不展开）、`TestSeqIfaceProvenImplExpanded`（`var d Doer = &A{}` → 只展开 A.Do，B.Do 不出现）、`TestSeqIfaceCandidateBudgetTruncation`（14 个候选 → 12 + 截断说明） |

**验证**：`go test ./internal/action -run 'TestSeqIface' -count=1`；全量 `verify.sh --quick` + `make it` 通过。

**注意**：直接 `go test -tags integration ./integration` 时子进程可能找不到 scip-go（要像 `make it` 那样把 `$(go env GOPATH)/bin` 加进 PATH，见 runbook #26）——不是代码回归。

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
| 3.3 | DI/AOP（Bean/Inject 注解、container/key 注册、事务 wrapper 内原实现） | **第一增量已完成（2026-10-27，B.8）**：字段注解/注册证据 + AOP 包装目标；框架专有契约（gof/其他容器）仍未知 |
| 4.1 | go-micro `NewRequest/Call` + 服务注册续接、跨仓 workspace 限定 | **已完成（2026-10-27，B.13）**：客户端桩/接口调用 + 注册身份/实现 + 证据；跨仓按 workspace 候选（默认限定、`--all-repos` 显式扩展） |
| 4.2 | 外部依赖源码按需解析（`go list -e -json -find -mod=readonly`、离线、缓存） | **已完成（2026-10-27，B.9）**：离线只读 go list + 请求内缓存 + 形参重新绑定；见 B.9 |
| 5 | 场景推导（整型/枚举/跨包常量/短路/取反） | **已实现**（`action_scene*.go`，含 `Source=7/19` 与 `int32(...)` 场景测试） |
| 6 | `SymbolSource` + `query symbol --include-source` | **已完成（2026-10-27）**，见 B.3 |
| 7.1 | 接口候选精确邻接查询（不 Expand/不加载全量 implements） | **已完成（2026-10-27，B.10）**：`InterfaceImplsExact`（整数邻接）+ 拒绝模糊/全扫的 Reader 包装器测试 + 冷暖次数对照 |
| 7.2 | 路由投影走已有索引 + EXPLAIN 计划测试 | **已完成（2026-10-27，B.10）**：kind 常量/属性表达式幂等索引 + EXPLAIN 计划锁 + 结果等价 |
| 7.3 | 请求内缓存（工厂来源/字段类型/DI/接口候选/AST） | **部分完成（B.12）**：接口候选（命中与未命中都缓存）、DI 注册表、外部包源码、调用点映射、文件 AST 均已请求内缓存；字段类型/工厂来源未单独加缓存（每次解析走 AST，成本已在解析缓存内） |
| 7.4 | 取消贯穿 SQL/源码定位/递归 | **已实现**（todo21-14.4/14.5，含物化 CTE 与预算记录） |
| 7.5 | JSON/文本/Mermaid/SVG 同源 + 证据/身份可核对 | 部分：todo19/20/21 已做重复调用点/证据/矢量图；统一分析树与 `*_projection` 诊断已实现，新增能力须继续遵守 |
| 8 | 第 8 节完整脱敏夹具与逐项断言表 | 夹具**已建**（`integration/flowfixture_test.go`，逐字照抄 §8 源码，供 §6 与后续 §2/§9 共用）；逐项断言表待 §2–§9 完成 |
| 9.1 | 八个同步流程（SourceA/B × 新建/新增子项/编辑子项/编辑实体） | **已完成（2026-10-27，B.14）**：flowmatrix 夹具 8 个独立入口 × 2 来源逐项验收（检查 4/7、RPC 续接、装饰器/事务、DI/AOP、存储终点、FinishReview=0） |
| 9.2 | 二十个后续流程形态 + 审核 finish 次数规则 | **已完成（2026-10-27，B.15）**：flow_review/flow_host 两组入口 01–20 逐项验收；审核规则（资料/服务各一次、自动审核不调人工、注册同步不算第三次、无配置=0）由源码事实与断言固定 |
| 9.3 | 最终输出（逐行验收、JSON+Mermaid+SVG、全量回归） | **已完成（2026-10-27，B.16）**：`docs/todo22/flow-matrix.md`（28 个编号验收行 + 复现命令 + 回归记录）+ `flow.svg`（含 RPC/DI/两层装饰器/事务回调）+ JSON↔Mermaid/SVG/分段的自动化断言 |

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

### B.8 §3.3 第一增量：DI 字段注入注册证据 + AOP 包装目标（已完成，2026-10-27）

| 要求 | 实现 / 证据 |
| --- | --- |
| 依据真实注入注解解析字段（支持注释形式，不假设只有 struct tag） | `diFieldKey`：字段 doc/行内注释 `// @inject store`、`// inject:store`、`//di:store` 优先；`inject:"key"` / `di:"key"` tag 兜底；都没有再按字段名匹配 |
| 精确 container/key 注册 | `seqDIRegistrations`：扫描同包 `.go` 的 `init()`/`Init()`/`Setup()`/`RegisterDeps`/`Bootstrap` 顶层语句里的 `Register(<字符串字面量 key>, <impl>)` / `Bind(...)`（支持 `c.Register(...)` 方法形态与 init→同包 `RegisterXxx()` 一跳） |
| 显式字段写者的普通数据流结论不可被 DI 猜测覆盖 | 调用点顺序固定：step 1 `receiverFieldImpl`（SSA 字段摘要 → 源码赋值）先；仅当它无结论才进 step 1b DI |
| 按框架契约保留事务 AOP，不跳过 wrapper 直接接业务方法 | 注册值是包装调用（`Register("k", Wrap(&T{}))`）时：目标取**包装器**的返回类型（`rhsTypes`），证据 reason = `di-aop-wrapper`；包装器内部调用原实现由正常展开显示 |
| 输出具体方法 ID、声明类型及 `di-field-registration` 等依据 | `evidence.source=di`、`reason` ∈ {`di-field-registration`,`di-field-annotation`,`di-aop-wrapper`}、`symbol_id`、`decl_type`、`di_register_file`/`di_register_line`（注册站点） |
| 动态 key / 多重注册 / 条件注册 / 非 init 注册 / 未知构造器保持未知 | 动态 key（非字符串字面量）不收集；多重注册 → `func_value_unbound` + "多重注册"；非引导函数注册不收集；`if`/`for` 内的条件注册不收集（顶层语句之外不算执行）；impl 类型无法解析 → 不收集（无证据） |
| 接口结构相容 / 唯一实现 / 类型断言不能代替注册证明 | DI 分支只认注册证据；没有注册就走既有接口派发（候选/unknown），不因"唯一实现"直接具体化 |

**测试**（`internal/action/action_seq_di_test.go`）：注解注册、struct tag 注册、四类证据不足反例（动态 key/多重/非引导/条件）、AOP 包装（目标 `(txStore).Add` + `aop-wrapper` + 展开 `TxBegin`）。

**未做**：框架专有契约（Bean/Inject 注解语法按框架而定、`gof`/其他容器的 `Set/Apply` 形态）、跨包注册（注册点在另一个包）、AOP bypass 条件的场景求解（`ctx.Value("bypass")` 之类运行时事实）。

### B.9 §4.2 外部依赖按需源码解析（已完成，2026-10-27）

| 要求 | 实现 / 证据 |
| --- | --- |
| 按准确包与符号按需解析本地依赖源码（含 go.mod replace / 仓库内嵌套模块） | `seqExternalPackage`：`go list -e -json -find -mod=readonly <importPath>`（Dir=仓库根）→ 解析目录内非测试 `.go`；`seqFindExternalDecl` 按 canonical ID 的**函数名 + receiver** 精确匹配 |
| 只读、离线：不下载、不构建依赖索引、不改 go.mod/go.sum | 环境固定 `GOFLAGS=-mod=readonly`、`GOPROXY=off`、`GOSUMDB=off`、`GONOSUMDB=*`；测试断言查询前后 `go.mod` 字节相同且未生成 `go.sum` |
| 成功与失败都请求内缓存 | `req.extPkgs[importPath]`（含 `err`/`std`），同一请求不重复起子进程 |
| 不模糊名称扫描 | 目标 ID 由 import path + 声明名直接构造（`symbol:go:<pkg>:<name>`），不经 `GetSymbolByName`/`ResolveSymbol` |
| 外部函数进入按其真实形参重新绑定（回调/列表/ellipsis），不沿用调用者同名映射 | `sub.env = newSeqEnv(nil)` + `bindSeqParams(sub.env, fd.Type.Params, captureSeqClosureArgs(...))` |
| 只有外部实现实际调用形参，回调才算已证明执行；未调用/重写/逃逸不执行 | 测试：`ext.Run` → 体 `f(ctx)` → 回调展开；`ext.Store` 只保存形参 → 回调内部调用 0 次 |
| depth=1 保留准确外部目标 + depth 诊断，不为不需要的展开启动 go list | `depth<=1` 时写 `evidence.symbol_id=<外部 ID>` + `stop=depth`，直接返回 |
| 过滤/停止包/深度/递归/取消共用 | 展开走同一 `walkStmts`（过滤/停止包/scene 生效）；调用前后检查 `req.ctx()` 取消 |
| 标准库不作为外部依赖展开 | `go list` 的 `Standard=true` → 不展开，保持 `external_target` 分类（避免 stdlib 内部噪声污染时序图） |

**测试**（`internal/action/action_seq_external_test.go`，夹具用 `replace ../ext` 的本地模块，离线可复现）：回调已执行/未执行两种情形、depth=1 目标保留、缺失包 external_target + go.mod 只读。

**未做**：外部包的**方法调用**（`extT.Method()` 需要 receiver 类型解析）、外部包内部跨包调用的目标解析（外部包没有本仓索引，只有回调/形参路径被证明）、go.work 工作区形态的外部模块。

### B.10 §7.1/§7.2 精确查询与索引计划门禁（已完成，2026-10-27）

| 要求 | 实现 / 证据 |
| --- | --- |
| 接口候选查询用准确 symbol 的**整数邻接索引**，不为一次派发加载全量 implements 边 | 新增 `Repo.InterfaceImplsExact(ifaceID)`：`nodes.id → id_int` 后走 `UNIQUE(source_ref,target_ref,kind)` 的最左前缀（不再 JOIN `edges_v` 视图）；`interfaceImpls` 与 `seqIfaceMethodImpl` 都改用它（后者原先 `GetImplementsEdges()` 全量扫描） |
| 提供 Reader 精确接口查询 | `action.Reader.InterfaceImplsExact`；派发路径不再依赖"全量边 + 内存过滤" |
| 拒绝模糊符号扫描/全图实现扫描的 Reader 包装器 | `strictReader`（`action_seq_iface_exact_test.go`）覆盖 `GetSymbolByName`/`AllSymbolNames`/`Expand`/`AllNodeIDs`/`GetImplementsEdges` 并全部报错；断言派发期间 0 次调用、`InterfaceImplsExact` 有调用 |
| 固定输入下的冷暖耗时与查询次数对照（不能只报缓存命中或退出码） | 同一 canonical 入口跑两次：`cold: queries=7 total=0.634ms` / `warm: queries=7 total=0.500ms`，断言**次数一致**（确定性证据）+ 单次预算 ≤40 |
| §7.2 路由投影走可命中索引；仅加索引不强制 schema 升级/重建业务索引 | `schema.go` 幂等加法：`idx_nodes_grpc_service`（部分索引 `WHERE kind='grpc_service'`）+ `idx_nodes_registers_service`（`json_extract` 表达式索引，因查询无 kind 谓词）；`bulk_load.go` 重建清单同步；**不递增 SchemaVersion** |
| 测试真实 EXPLAIN 查询计划，不靠过滤合法服务名缩小结果 | `repo_routes_plan_test.go`：SQL 与生产共用常量（`grpcServicesSQL`/`registerNodeSQL`），`EXPLAIN QUERY PLAN` 断言命中索引且非 SCAN；同时断言结果等价（`GetGrpcServices`=1 条、`GetRegisterNode` 命中注册函数） |

**未做**：`chainSymbols`（ext-chain 全链 BFS）仍会一次性加载 calls/implements 边——那是"整链"用例而非单次派发，改造成本高、收益待测（记入 §B.1 备注）。

### B.11 §9.3 渲染一致性验收（部分完成，2026-10-27）

`integration/todo22_render_consistency_test.go`（真实 init → CLI，§8 夹具）：

| 要求（§9.3-3） | 实现 / 证据 |
| --- | --- |
| 同一入口实际生成 JSON 和 Mermaid | 同一 canonical 入口 + 同一 `--depth` 分别跑 `--json` 与 `--format mermaid` |
| 检查解析与**箭头顺序** | JSON 先序调用序列与 Mermaid `P->>P: label` 序列**逐项相等**（28 条一致；两条渲染约定显式归一化：入口自身不画自调用箭头、未消歧调用加 `? ` 前缀） |
| 检查条件结构 | JSON 的 branch/loop 标签（带子步骤的）必须出现在 Mermaid 的 `alt`/`loop` 行；并断言关键结构（`alt `、`loop range wrappers`）存在，防"两边都空"的假一致 |
| 流程导出 SVG 并检查解析产物 | `--format svg --out flow.svg` → 产物含 `<svg`、含 `BeforeFirst`/`BeforeSecond`/`AfterSecond`/`store.Insert`、含条件结构（73187 字节实测）；plantuml.jar/java 缺失时 skip |
| 逐编号验收行（入口/场景/命令/目标 ID/证明连接/候选/截断/finish 次数/一致性） | **未做**——依赖 §9.1 八流程与 §9.2 二十形态的夹具（含 RPC/DI） |

**已知范围缩减**：本夹具是"两层装饰器 + 外部事务回调 + 场景检查"，不含 RPC/DI
节点，§9.3 要求的"含 RPC、DI 的流程 SVG"仍待 §4.1/§3.3 夹具补齐后再验收。

### B.12 §7.3 请求内缓存补强：接口实现候选（2026-10-27）

- `req.ifaceImpls[ifaceID]` 缓存**接口实现解析结果**（`chosen` + `candidates` +
  是否命中），首次查询走 `InterfaceMethodImpl` + `InterfaceImplsExact`，后续
  同一请求里的同接口派发直接复用（**未命中/无可证明实现也缓存**——`ErrNotFound`
  是正常未命中，不重复全库发现）。
- 缓存保存的是**原始候选**而不是某次推断结果，chosen 与 candidates 同源
  （避免"首次查一个口径、缓存命中另一个口径"）。
- 已有请求级缓存：DI 注册表（`diRegs`）、外部包源码（`extPkgs`）、函数调用点
  映射（`tgtsCache`）、文件 AST（`fileCache`）、字面量防环（`litVisited`）。
- 测试 `TestSeqIfaceImplCachePerRequest`：同一函数两处 `d.Do()` →
  `InterfaceImplsExact` 只调用 **1 次**，两处调用点仍各自保留 2 个未消歧候选
  （缓存不改变结论）；既有 `ImplType` 数据流具体化测试保持通过（chosen 口径
  未变）。

**未做**：receiver 字段类型与工厂来源的独立缓存（当前每次走 AST 数据流，成本
落在文件解析缓存内；大仓实测收益待量化后再决定是否加）。

### B.13 §4.1 go-micro 服务协议与注册续接（同仓已完成，2026-10-27）

| 要求（§4.1） | 实现 / 证据 |
| --- | --- |
| 从生成客户端 `NewRequest/Call` 及准确 proto 服务身份恢复方法 | `ast_micro.go`：`client.NewRequest(service, endpoint, req)` 前两参精确字符串（字面量/常量；动态拼接保持未知）→ 完整身份 + handler 短名 + 方法名；同函数 `Call` 消费 → `grpc_call` 边（source=桩方法）+ 服务节点 `rpc_protocol=micro` |
| 定位真实 Register 注册及服务实现 | 生成注册函数形态（首参 `server.Server` + 第二参 `<X>Handler` 接口）→ 服务节点（service_name/handler_name/methods）+ `grpc_impl` 边（注册实参类型）；完整注册身份取 `micro.NewService(micro.Name("..."))` 绑定（属性 `rpc_service`）；`registers_service` 打在注册函数节点（同 Setup 多服务不互相覆盖） |
| 在原 RPC 调用点续接服务端子树 | `action_seq_rpc.go`：调用点出站边 → 服务身份（精确注册身份优先）→ 实现（多实现歧义/零实现诊断）→ `(Impl).Method` 精确构造 + 展开；桩源码不展开 |
| 校验完整身份、方法、注册实参、导入包与实际 server 协议 | 协议按调用点可选项类型判定（`...client.CallOption`）；完整身份/注册身份分开保存，**不一致 fail-closed**（`rpc_identity_conflict`）；方法 canonical ID 精确查询，缺失时附注册方法清单；注册实参取 `grpc_impl` 边 |
| 同名方法不同服务不能串接 / 非标准服务名不被优化过滤 | 按服务身份 + handler 名 + 方法解析，不按名字形状过滤；`TestTodo22MicroRPCSameRepo` 两个入口分别只到各自实现；动态服务名不用前缀冒充身份（单测覆盖） |
| 定点查询 Register 节点；未找到是正常未命中并缓存，不反复全库发现 | `GetRegisterNode`/`registers_service` 属性 + `GetRPCServicesByFullIdentity`（`idx_nodes_rpc_service`）/`GetRPCServicesByProtocol`（`idx_nodes_grpc_service`）；`req.rpcSvcs`/`req.rpcByFunc` 命中与未命中都缓存；`TestSeqRPCContinuationAndRequestCache` 断言同源/同服务只查一次 |
| 兼容 native gRPC 不退化 | native 路径（ServiceDesc/RegisterXxxServer/grpc_client 签名）不改；`grpcClientServiceOf` 只为 micro 客户端接口补协议标记（`rpc_protocol=micro`），既有 gRPC 集成用例全绿（`make it`） |
| 保留 repo/服务 ID/构建身份/调用与定义位置 | 证据 `service_id`/`rpc_service`/`rpc_protocol`/`rpc_method`/注册 `file:line`；`--json` 原字段不变（新增可选键） |
| 真实源码夹具 + 反例 + CLI 集成 | `integration/microfixture_test.go`（go-micro stub 模块 replace + 生成形态 pb + 两服务 Apply）；`integration/todo22_micro_test.go`：正例（两入口不串线、depth=1、桩源码不展开）+ 反例（移除注册/改协议/改服务名）全部只断言各自结论 |
| 跨仓 workspace 限定 / 资源关闭覆盖子树 / 跨仓缓存键含仓库身份 | **已完成（2026-10-27，跨仓增量）**：`CodeSequenceRequest.CandidateRepos/Open` + cli `seqCandidateRepos`（默认同一 workspace；无归属=repo-only，`--all-repos` 显式扩展）；起始仓库未找到注册时按候选逐个定点解析；目标仓库子方法用独立 `CodeSequence` 展开（源码/包集合/文件缓存隔离），视图由请求级 `seqRPCResources` 在分析结束时统一关闭（覆盖子树使用期）；防环键 `仓库|方法入口` |
| go-micro 直接 `micro.RegisterHandler` 形态 | 识别根包 `RegisterHandler`，但服务短名只在第二参是 `<X>Handler` 接口时可解析；`new(AccountServer)` 这类具体类型注册保留 `rpc_impl_missing`（不猜） |

验证命令：

```sh
go test ./internal/infrastructure/ast -run 'TestIndexMicro' -count=1
go test ./internal/infrastructure/sqlite -run 'TestRPCForward' -count=1
go test ./internal/action -run 'TestSeqRPC' -count=1
PATH=$(go env GOPATH)/bin:$PATH go test -tags integration ./integration -run 'TestTodo22MicroRPC' -count=1 -v
bash scripts/verify.sh --quick && make it
```

结果：以上全部 PASS（集成用例 0.26s/0.82s；`make it` 44.6s）。

**跨仓增量（同轮提交）**

| 要求 | 实现 / 证据 |
| --- | --- |
| 候选仓库限定显式 workspace 范围 | `seqCandidateRepos`（cli）：起始仓库有 workspace 归属 → 同 workspace 其它已构建仓库；无归属 → **repo-only**（不因全局注册表同名服务改变默认结果）；`--all-repos` 显式全部已注册。范围标签 `Scope` 记录在请求中 |
| 存在注册但实现未知 ≠ 没有注册（跨仓才能继续找） | 只有客户端声明身份（`rpc_service` 为空）→ `rpc_registration_missing`（可继续候选仓库）；有注册身份但无 `grpc_impl` → `rpc_impl_missing`（终止，不跨仓猜） |
| 跨仓子树展开 / 文件缓存 / 包集合以目标仓库为准 | `seqExpandRemoteRPC`：目标仓库独立 `CodeSequence`（`RepoAbs`/`fileCache`/`pkgSet`/`tgtsCache` 全部隔离），只拼回子节点；`RemoteRepo` + 目标 `ServiceID` 写进证据 |
| 资源关闭覆盖全部子树使用期 | `seqRPCResources`（请求级）：候选视图打开一次、请求结束 `defer closeAll()`；嵌套 `CodeSequence` 复用同一资源指针（不重复 defer 关闭）。`TestSeqRPCCrossRepo` 断言 opens=1/closes=1 |
| 防环键含仓库身份 | `rpcVisited` 键 = `req.RepoAbs|方法入口`（跨仓 A→B→A 在第二次回到 A 时命中） |
| 测试 | `internal/action/action_seq_rpc_repo_test.go`：客户端仓 → 候选服务端仓续接（证据/子节点/资源关闭）、无候选范围时 repo-only 明确未命中且不打开任何仓库 |

### B.14 §9.1 八个同步流程（已完成，2026-10-27）

夹具：`integration/flowmatrix_*_fixture_test.go`（module `example.com/flowmatrix`，
go-micro stub + tx 本地 replace；沿用 §8 结构并补 trace/审核/RPC/DI/AOP）。

| 编号 | 入口 | 来源 | 操作 | 存储终点（断言） |
| --- | --- | --- | --- | --- |
| 21 | `(App).Flow21NewEntityA` | A | 新建实体 | `(Store).Insert` |
| 22 | `(App).Flow22NewChildA` | A | 新增子项 | `(Store).InsertChild` |
| 23 | `(App).Flow23EditChildA` | A | 编辑子项 | `(Store).UpdateChild` |
| 24 | `(App).Flow24NewEntityB` | B | 新建实体 | `(Store).Insert` |
| 25 | `(App).Flow25NewChildB` | B | 新增子项 | `(Store).InsertChild` |
| 26 | `(App).Flow26EditChildB` | B | 编辑子项 | `(Store).UpdateChild` |
| 27 | `(App).Flow27EditEntityA` | A | 编辑实体 | `(Store).UpdateEntity` |
| 28 | `(App).Flow28EditEntityB` | B | 编辑实体 | `(Store).UpdateEntity` |

每条要求（§9.1）逐项落地：

| 要求 | 证据 |
| --- | --- |
| 八个独立入口（SourceA/SourceB × 四种操作） | 8 个方法入口，各自独立存储方法；测试 16 个子测试（每流程 × 每来源）独立断言，不合并通过 |
| 沿用第 8 节夹具结构 | 同名 `MakeFirst/First/Second/Execute/Store/重载检查清单`；RPC/DI/AOP/审核为新增事实 |
| 接入 trace 提交 | 每个流程断言 `SaveTrace` 在 `ApplyRemote` 之前 |
| 准确 RPC 注册 | `ApplyRemote` → `(AccountServer).Apply`（`evidence.source=rpc`、`rpc_service=example.flow.AccountService`，§4.1 同款） |
| DI/AOP | `a.Store.Insert` 证据 `source=di`、`reason=di-aop-wrapper`、目标 `(storeAOP).Insert` → `(Store).Insert`（不跳过 wrapper） |
| 非空装饰器 | `MakeFirst` + `Second` 两层（树上 BeforeFirst/BeforeSecond/AfterFirst/AfterSecond 齐全；bypass 两条分支保留） |
| 事务回调 | `tx.Run` 节点 + 回调内存储调用（`ExecContext` 在 `Store.<Op>` 内） |
| 存储 API | 按操作断言不同终点方法（见上表）+ 到达 `s.DB.ExecContext` |
| trace 批准 | `ApproveTrace` 在存储终点之后 |
| SourceA/SourceB 四/七项检查差异 | 每个流程两种来源分别断言 Check1–4 = 1 次、Check5–7 = 0（A）/1（B） |
| 八项人工 FinishReview 次数为零 | 每流程断言 `FinishReview`/`FinishDataReview`/`FinishServiceReview`/`AutoApprove` 均为 0 |
| 完整入口到存储终点（不孤立查询子函数） | 顺序断言 `SaveTrace < ApplyRemote < 存储终点 < ApproveTrace`，全部从入口一次查询得到 |

顺带修正（同一提交）：① 查询侧源码解析改为 `parser.ParseComments`（DI 注释注解
此前静默失效，单测靠字段名兜底）；② 闭包展开的 `curSym`/`curFile` 取"字面量
所在函数/文件"（此前用内联调用者的包，跨包回调时 DI 注册表/包身份全错）。

验证：

```sh
PATH=$(go env GOPATH)/bin:$PATH go test -tags integration ./integration -run 'TestTodo22SyncFlows' -count=1
```

结果：16/16 子测试 PASS（1.6s，含 init）。

### B.15 §9.2 二十个流程形态 + 审核 finish 规则（已完成，2026-10-27）

夹具新增 `flow_review.go`（01–13）与 `flow_host.go`（14–20），沿用共享后端链；
审核规则见 `flowmatrix_core_fixture_test.go`：

| 规则 | 源码/持久化身份 |
| --- | --- |
| 资料节点人工 finish 一次 | `FinishDataReview` → `FinishReview(DataNode{ID:101})` → `PersistDataReview` → `saveReviewRow("review_data")` |
| 服务节点人工 finish 一次 | `FinishServiceReview` → `FinishReview(ServiceNode{ID:202})` → `PersistServiceReview` → `saveReviewRow("review_service")` |
| 共两次且 node_id 不同 | 两个包装各一次调用（不同行），node_id 101/202，持久化行不同 |
| 自动审核不调人工 | `autoApproveData` → `AutoApprove(DataNode)`，体内无 `FinishReview` |
| 外部注册同步不算第三次 | `SyncRegistry()` 是自动步骤（RPC 服务端实现内也调用），不触发 `FinishReview` |
| 无审核配置 = 0 | `FlowAPINewChildDirect`：`apiSubmit` + `txInsertChild`，无任何审核调用 |
| 注册/审核/持久化身份可核对 | 审核完成走 `FinishReview`（唯一人工入口）+ 各自 persist 函数；注册身份见 §4.1 `rpc_service`；持久化身份 = `saveReviewRow` 的 kind 字面量 |

逐编号验收（`TestTodo22FlowForms`，25 子测试 = 20 形态 + 5 个人工分支补充）：

| 编号 | 形态 | 已证明连接 / 关键目标 ID | 候选/截断/未知 | finish 次数依据 |
| --- | --- | --- | --- | --- |
| 01 | Web 新实体 | `(App).webSubmit` → `checksAndTrace` → `ApplyRemote` → `(AccountServer).Apply` → `txInsert` → DI `(storeAOP).Insert` → `(Store).Insert` → `ExecContext` → `ApproveTrace` | `process` 循环重赋值多候选（保留） | `FinishReview`×2 |
| 02 | Web 新服务 | 同上 + `(Store).SaveService` | 同上 | ×2 |
| 03 | Web 新子项 | 同上 + `(Store).InsertChild` | 同上 | ×2 |
| 04 | App 新实体 | `(App).appSubmit`（不出现 `webSubmit`）+ 共享链 | 同上 | ×2 |
| 05 | App 新服务 | `appSubmit` + `SaveService` | 同上 | ×2 |
| 06 | App 新子项 | `appSubmit` + `InsertChild` | 同上 | ×2 |
| 07 | Partner App 新实体 | `partnerSubmit` + A：`AutoApprove` / B：`finishManualReviews` | 来源分派由 scene 裁剪 | A=0 / B=2 |
| 08 | Partner App 新服务 | 同上 + `SaveService` | 同上 | A=0 / B=2 |
| 09 | Partner App 新子项 | 同上 + `InsertChild` | 同上 | A=0 / B=2 |
| 10 | 编辑子项 | `FinishDataReview`→`PersistDataReview` 与 `(Store).UpdateChild` 分别出现 | — | ×1 |
| 11 | App 新银行账户 | A 自动 / B 人工；`SaveAccount` 与 `UpdateEntityState` 都证明 | — | A=0 / B=2 |
| 12 | App 编辑主体 | `appSubmit` + `FinishServiceReview` + `UpdateEntity` | 不出现 `webSubmit` | ×1 |
| 13 | Web 编辑主体 | `webSubmit` + 共享 `FinishServiceReview` + `UpdateEntity` | 不出现 `appSubmit` | ×1 |
| 14 | 宿主 A 新银行账户 | `(App).hostAHandler` + 共享 `(App).hostApply` + `SaveAccount`；不出现 B/C handler | — | A=0（自动） |
| 15 | 宿主 B 新银行账户 | `hostBHandler` + `hostApply` + 两次人工 | 不出现 A/C handler | ×2 |
| 16 | 宿主 C 新银行账户 | `hostCHandler` + `hostApply` + 资料审核 | 不出现 A/B handler | ×1 |
| 17 | 支付 App 新实体 | A：`AutoApprove`；`FinishDataReview`；`SyncRegistry` | 注册同步不增加 finish | ×1（A/B 都是 1） |
| 18 | 宿主转入新服务 | `hostTransferSubmit` → `FinishDataReview` → `SyncRegistry` → `SaveService` → `ApproveTrace` | — | ×1 |
| 19 | API 新实体（有审核） | `apiSubmit` + 两次人工 finish | — | ×2 |
| 20 | API 新子项（直接主数据） | `apiSubmit` → `InsertChild`；**不出现**任何审核符号 | 无审核配置 | 0 |

验证：

```sh
PATH=$(go env GOPATH)/bin:$PATH go test -tags integration ./integration -run 'TestTodo22FlowForms|TestTodo22SyncFlows' -count=1
```

结果：`TestTodo22FlowForms` 25/25、`TestTodo22SyncFlows` 16/16 PASS。

### B.16 §9.3 最终输出（已完成，2026-10-27）

| 要求（§9.3） | 交付 / 证据 |
| --- | --- |
| 生产实现、公开夹具、反例矩阵、真实 CLI 集成测试；分析逻辑在 action、cli 只做参数与渲染 | action：`action_seq*`（RPC/DI/闭包/场景）、`repo_rpc`（仓储查询）；cli：`query_sequence*` 渲染；夹具 `integration/flowmatrix_*`；反例分散在 §9.1/§9.2 用例（不出现对方入口/处理器、无审核配置=0、identity 冲突等）与 micro 负例（§4.1） |
| 每个编号一行验收：入口、场景、完整/分段命令、关键目标 ID、已证明连接、候选/截断/未知、finish 次数依据、JSON/图一致性 | `docs/todo22/flow-matrix.md` §1（21–28 八行）+ §2（01–20 二十行）+ §3 分段验收；命令可直接复制执行 |
| 同一入口 JSON + Mermaid；至少一个含 RPC、DI、两层装饰器、外部事务回调的流程生成 SVG，检查解析/条件结构/箭头顺序 | `TestTodo22FlowMatrixJSONMermaid`（调用顺序逐项相等；条件结构覆盖）+ `TestTodo22FlowMatrixSVG`（205216 字节，关键节点与 alt/group）；图片渲染与链路完整分开断言 |
| 执行 `go test ./...`、`verify.sh --quick`、`make it` 及相关回调/取消 race 回归；报告实际执行/失败/跳过 | `docs/todo22/flow-matrix.md` §4：全部 PASS（`go test ./...` 14 包、`make it` 53.5s、action `-race` 回调/场景、sqlite `-race` 递归取消）；跳过项：doc 导出（需 env）、plantuml 缺失时 SVG（本机可用） |
| 保留时序图与承载图的 Markdown；清理临时 JSON/日志/探针/中间图片 | 保留 `docs/todo22/flow.svg` + `docs/todo22/flow-matrix.md`；已删 `flow.json`/`flow.mmd`/`flow.svg.puml`/`flow.txt`；导出入口 `TestTodo22DocOutputs` 保留 |

验证：

```sh
PATH=$(go env GOPATH)/bin:$PATH go test -tags integration ./integration   -run 'TestTodo22SyncFlows|TestTodo22FlowForms|TestTodo22FlowMatrix|TestTodo22SegmentedFlow' -count=1
go test ./... && bash scripts/verify.sh --quick && make it
```

### B.17 P1/P2 收口（2026-10-27 接手）

| # | 事项 | 状态 |
| --- | --- | --- |
| P1-a | ext-chain 全链 BFS 不再一次性加载 calls/implements 边 | **已完成**：`chainSymbols` 逐节点 `GetOutgoingFactsByKinds` + `InterfaceImplsExact` + 精确 `GetSymbol`；`chainGrpcHTTPDiag` 同步收口；本仓库实测 0.14s/72.7MB → 0.07s/31.1MB、产物逐字节相同；门槛测试拒绝全量扫描（`action_chain_symbols_exact_test.go`）；见 field_trace §127 |
| P1-d | AOP bypass 的运行时场景求解 | **已完成**：识别 `ctx.Value(<key>) ==/!= nil`，场景键 = context key（存在性布尔）；无 `--scene` 键/动态 key 保持 unknown；单测矩阵 + 真实时序裁剪（`action_scene_ctx_test.go`）；见 field_trace §130 |
| P1-c | 场景值沿实参/结构体字段跨函数传播 | **已完成**：`litScopes` 增加 `req.Source` 字段键（字面量/字段赋值/整体覆盖/取址失效），`resolveSceneArgFields` + `paramFieldArgs` 把字段值传播到被调函数的 `param.Field`；实参已知值优先于 `--scene`，未知字段不回退；三跳测试覆盖正例 + 未知反例（`action_scene_field_prop_test.go`）；见 field_trace §129 |
| P1-b | 场景判定的跨包常量 | **已完成**：`seqConstTable` 按 import 别名按需解析目标包 const（离线 `go list` + 请求内缓存，失败/标准库也缓存）；默认/别名 import、内建转换、未声明常量反例全覆盖（`action_scene_const_test.go`）；常量算术/dot-import 仍 unknown；见 field_trace §128 |

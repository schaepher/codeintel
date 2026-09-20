# AGENTS.md — 面向 AI 代理的开发指南

本文件供 AI 编码代理（Claude Code、Codex、Cursor 等）在修改本仓库前阅读。

## 文档导航

- [`docs/DoD.md`](docs/DoD.md)：交付验收清单（10 条）+ 五轴自检——**每个
  改动交付前逐项自查**（Q235）
- [`docs/runbook.md`](docs/runbook.md)：故障模式速查表（/tmp 满、WAL、
  端口冲突、schema 迁移等 14 条，Q235）
- [`docs/troubleshooting-tree.md`](docs/troubleshooting-tree.md)：**事后树**——
  排障思路树（五步定位法 + 症状分支，怎么找到问题；Q242–Q244 沉淀）
- [`docs/prevention-tree.md`](docs/prevention-tree.md)：**事前树**——
  防错思路树（五层预防，怎么让问题不发生；由事后树反推）
- `docs/design-q235.md` 已归档：Q235 六项借鉴设计已实施并落档
  field_trace.md §64–§66（设计文档已删除）
- [`docs/self-analysis.md`](docs/self-analysis.md) 文末「待办与已知不足」：
  **待办清单**（P0–P2 + 各轮新增，接手前先看）
- [`docs/field_trace.md`](docs/field_trace.md)：逐 Q 实现记录（§63 起为
  Q234/Q235/Q238 系列；§77 = Q238 全局注册表 + worktree/workspace，
  design-q238.md 已归档；§81 = 排障树脚本化 + 防忘机制 Q245）

## 项目一句话

`codeintel` 是一个 Go 代码库智能索引系统：对 Go module 仓库做静态分析，
产出 SQLite 代码图（`.codeintel/codeintel.db`），通过 CLI 提供符号与调用关系查询；
**业务 wiki 生成（`codeintel wiki`，AI 增量补缺）是核心能力之一**——
自举循环记录在 [`docs/self-analysis.md`](docs/self-analysis.md)（R23 起
AI 补缺、R26 ask 问答、R27 对话界面与 Q&A 收集、R28 go2o 缺口清零）；
**待办清单在文末「待办与已知不足」**（P0–P2 + 各轮新增区，接手前先看）。
设计权威是 [`docs/TD.md`](docs/TD.md)（v2.0，22 个决策点均已确认）；
**图数据模型总表（节点/边/属性/置信度/表结构）见 [`docs/data-model.md`](docs/data-model.md)**——
理解本项目优先读它，再读 field_trace.md 各功能 §。

## 分层架构（R89）：cli 薄壳 / action 业务逻辑 / 渲染留 cli

**设计原则**：`internal/cli` 里命令的**具体逻辑**（查询/分析/聚合/流程
编排）应放 `internal/action`；cli 只做三件事——① 解析命令行参数 ②
构造 action.Request 转发给 action ③ 输出格式化（含渲染）。

```
internal/cli        internal/action            internal/infrastructure
┌──────────────┐    ┌──────────────────┐      ┌──────────────────┐
│ 参数解析      │ →  │ 命令业务逻辑       │  →   │ sqlite/ast/ssa/  │
│ request 构造  │    │ （返回结构化结果）  │      │ git 等实现        │
│ 输出/渲染     │ ←  │                  │      └──────────────────┘
└──────────────┘    └──────────────────┘
```

- **action 层**：每个命令一个 `(a *Actions) Xxx(req XxxRequest) (T, error)`
  方法——类型化参数（Request struct）+ 结构化返回值；不依赖
  flag/os.Stdout/渲染；可被 CLI/serve/MCP 复用
- **cli 层**：`cmdXxx` 只做参数解析 → 调 action → 格式化输出
  （文本/JSON/mermaid）；**渲染（wiki HTML/MD 拼装、mermaid 文本
  生成）留在 cli**——展示层，action 不产 HTML
- **迁移节奏**：分批迁移（先纯查询类命令），每轮迁移后跑全量测试；
  已完成迁移清单在 `docs/self-analysis.md` 轮次记录
- 数据模型/查询实现（infrastructure/sqlite 等）不动——只搬"命令逻辑"

## 强制流程（Q235-2，借鉴 GitNexus impact-before-edit）

1. **改符号前影响分析**：修改任何被其他符号引用的符号（函数/方法/
   类型/字段/包级变量；跨包、被调用者多者必查）前，先跑
   `codeintel query impact <sym> --repo <path>`（本仓库未索引时用
   codegraph_impact）
2. **影响面明示**：影响评估为 HIGH/CRITICAL（调用者 ≥10 或跨模块/
   跨包边界）的变更，须在回复中明示影响面与回归策略（哪些测试/
   验证覆盖）
3. **UNKNOWN 视为未解决**：符号未索引 / 索引陈旧 → 先
   `codeintel update --repo <path>` 或 `init` 补索引，不得直接改

4. **提交前验证（防忘机制，Q242–Q244 沉淀）**：commit 前必须通过
   `scripts/verify.sh --quick`（build + vet + 非 race 全量单测）——
   本仓库已装 pre-commit hook（`scripts/install-precommit.sh`）自动
   执行，失败拒绝提交；hook 先跑 `scripts/check-file-size.sh`
   （staged Go 文件 >300 行拒绝，提示用 dev-line-limit skill 拆分——
   §85）；全量基线（-race 逐包）手动跑 `scripts/verify.sh`。
   `scripts/dbdiag.sh`（sqlite 库健康诊断；Q254 起加 `--size` 体量/冗余索引
   候选 + `--plans` 查询计划对比 + `--self-test`）、`scripts/assert_replace.py`
   （带断言替换，杜绝静默失败）详见事前树 prevention-tree.md。

支撑：`.claude/hooks/impact-check.sh`（PreToolUse 非阻断提醒，只提示
不拒绝；仓库已索引/未索引两种情况各一行）；`.claude/hooks/verify-remind.sh`
（PostToolUse 非阻断提醒，改 Go 文件后提示跑 verify.sh——未装
pre-commit 场景的兜底）。

## Q254c/d 整数代理键 + 写库流水线教训（2026-09-20）

- **外键不能引用 `rowid`**：`REFERENCES nodes(rowid)` → `foreign key mismatch`
  （父键必须是**命名列**且有唯一索引）。要整数代理键就必须显式
  `id_int INTEGER PRIMARY KEY`（rowid 别名，不额外占索引）+ `id TEXT UNIQUE`
  ——存储与"id TEXT PRIMARY KEY"等价，故父表侧不涨体积。
- **`edges_v` 兼容视图是"零改动冷路径"的关键**：物理表换整数列后，给
  `source_id`/`target_id` 建 JOIN 视图，27 处 `FROM edges` 里 16 处冷路径
  一行不用改；只有热路径（邻接表/路径/递归 CTE）必须改整数空间——canonical
  ID 只在**边界**解析（起点/终点点查 + 结果回填），别在全表扫描里搬 145B 字符串。
- **`INDEXED BY` 是视图的死结**：视图不能加索引提示——用
  `INDEXED BY <idx>` 锁索引的查询（value-trace 递归 CTE）必须改整数列直查。
- **门槛脚本必须断言 dump 非空**：`detcheck.sh` 的 dump 用基表 `source_id`
  （列已删）→ sqlite3 报错但未检查退出码 → 两侧空集、报告"边 OK（共 0/0）"
  ——**假绿**。`--size/--plans` 之外，任何"取数→比较"的门槛都要
  `|| exit 2` + `[ -s file ]`。
- **schema 类型变更的迁移路径要顺手**：`reindex` 在文档里是"删旧库绕过 schema
  检查"，实现却只调 init → 旧库直接报错。检测到 schema 不匹配就删库重建
  （图数据是派生数据；打印"配置表 relation_rules 会丢"）。
- **"批量 INSERT 一定更快"要先量**：多行 VALUES(200) 实测 flush 1.8–2.3s vs
  预编译逐行 0.5–1.6s（节点 ON CONFLICT 子句巨大 → 长语句每 chunk 重新
  解析/计划）。按 Q221 纪律回滚，数据留档 §104。
- **先量瓶颈再决定架构**：Stage 2 后 flush 只占构建 5–12%（计算占大头），
  故"两阶段导入/drop 索引"不成立 → 降级为观察项（触发条件写进待办）。

## Q254b stale 判定 + 两个操作纪律教训（2026-09-20）

- **stale/新鲜度判定要看"内容指纹"，不要看 git 状态行数**：原 `staleInfo` 按
  `git status --porcelain` 行数统计 → 文档/脚本等**不进索引**的文件也算"未
  索引"；且"改文件→reindex（脏着建）"这个正常闭环被误判过期。
  修法：①变更集合只取会进索引的（`.go`/`go.mod`/`go.work`）②构建前把工作区
  脏文件的 `path→内容 sha256` 存进 `build_metadata.worktree_fingerprint`，
  查询时比对（差异分类计数：已改/新增/删除）。
- **指纹口径必须构建前后稳定**：第一版用 `detectChangedGoFiles`（含"索引
  commit 落后 HEAD"的提交差异）→ 构建前多出提交差异文件、构建后索引追上只剩
  工作区脏文件 → **集合漂移，指纹永不匹配**。故拆 `dirtyGoFiles`（只相对 HEAD）
  做指纹口径，提交差异交给 SHA 比较（§102）。
- **`write` 工具会静默覆盖同名文件**：本轮 `write internal/cli/stale_test.go`
  覆盖了已存在的 157 行测试文件（含 `seedGitRepo` —— 三个测试文件引用它），
  直接打挂包编译。**写文件前先 `ls`/`read` 确认路径不存在**；被覆盖的用
  `git checkout HEAD -- <file>` 恢复，新内容另起文件名。
- **绝不用 `git checkout <file>` / `git restore` 撤销小实验**：该命令恢复的是
  **HEAD 版本**，会连带丢弃该文件全部未提交改动（本轮 `update_detect.go` 新增
  的两个函数就是这么没的）。撤探针要用**针对性编辑**（精确删除自己加的那几
  行），或先 `git stash`。

## Q254 Stage 1 库维护教训（2026-09-20）

- **删索引的迁移不要递增 SchemaVersion**：删索引不改表结构，用 Open 时幂等
  `DROP INDEX`（`dropRedundantEdgeIndexes`）即可——递增版本会逼用户重建 13GB 库。
- **`INDEXED BY <索引名>` 是硬依赖**：删索引前必须全局搜 `INDEXED BY`
  （`repo_value_trace*.go` 三处锁定了被删索引名）；被删的显式索引可换成
  等效索引，隐式自动索引也能被 INDEXED BY 引用（`INDEXED BY sqlite_autoindex_edges_1`
  实测可行）。
- **VACUUM 是无条件执行的反面教材**：`DROP TABLE`+重建后 `freelist≈0`，
  VACUUM 回收不到任何页却重写整库（13GB/7 分钟 + 需同等临时空间）。
  判定要**两个条件同时满足**（freelist 达阈值 + 磁盘够 1.2×库大小），并把
  跳过原因打给用户；`CODEINTEL_VACUUM_MIN_MB` 留手动逃生口。
- **构建期关外键要有等价性证据**：同一二进制 FK-on/FK-off 两次全量构建产物
  必须完全一致（本次 go2o：117998 节点/136028 边/484 跳过边 逐项相同）——
  这类"语义等价 + 省一次重试路径"的改动，对照实验比推理可靠。
- **`pragma_foreign_key_check` 的返回行也要先 Close 再写**（同连接单写者死锁，
  `DropDanglingEdges` 与 `wal_checkpoint` 各踩一次）。
- **度量夹具必须含 `.git`**：§95 的链完整性基线是在去 `.git` 的副本上量的，
  少 git 适配器的 1 个 COMMIT 节点 + 525 条 modified_by 边 → 被误判成回归。
  基线口径要在记录时写清（本次已用 `chaincheck --update` 刷新）。
- **pragma 分"库内持久"与"连接级"**：`journal_mode`/`auto_vacuum`/`page_size`
  持久；`cache_size`/`synchronous`/`foreign_keys`/`journal_size_limit`/
  `wal_autocheckpoint` 只在连接上生效（诊断脚本必须分开打印，否则误判）。

## Q253 构建进度条（2026-09-20）

- **契约放 domain、渲染放 internal/progress**：`domain.Progress`
  （`Begin/Advance/End/Finish`）由 orchestrator 与适配器调用；TTY/逐行/静默
  是换实现。**耗时由调用方传入**（各调用方本来就算好了 `time.Since(start)`）
  ——实现不必自记时钟，测试可传固定值。
- **`--progress plain` 必须与改动前的逐行格式逐字一致**（`[index] 步骤 X（1.2s）`；
  SSA 子步骤保 `[index] ssa 步骤 X（41ms, heap 170MB/189MB inuse）`）——heap
  统计是 Q247/Q248 内存排查依据，所以 SSA 在 plain 注入时**换回自己的保底
  实现**（`Adapter.SetProgress` 的类型判断），只有 TTY 才共用同一渲染器。
- **单活动行 vs 并行适配器**：本仓库 scip/ast/git/ssa 并行，多行 live 区要
  光标上移 + 逐行清理，且 `packages.PrintErrors`/警告也写 stderr 会打乱；
  改成"最新步骤占活动行、其余按完成顺序打印静态行"最简单且诚实。
- **无真实分母不画条**：scip/git/flush 只有"进行中 + 已用时"；SSA 发射循环
  用模块函数总数（真实分母）。假进度比没进度更糟。
- **进度只写 stderr**（stdout 是结果/`--json` 契约）；验收要分两部分看
  （`2>stderr` 抓进度、stdout 只剩余结果）。

## Q252f 静默退化根治教训（2026-09-19）

- **`ok` 不等于"跑了"**：`go test` 的 `ok` 不区分"执行通过"与"全部 skip"。
  `internal/orchestrator` 的 e2e 单测靠 `exec.LookPath("scip-go")` 决定跑/跳
  ——scip-go 不在 PATH 时整批集成型单测静默跳过，此前多轮"13 包全绿"实为跳过。
  **门槛脚本必须显式定位并打印外部工具**（`scripts/verify.sh` / `make test`
  已自动把 `$(go env GOPATH)/bin` 加入 PATH，verify 头部打印 scip-go 路径）。
- **静默退化第二次命中同一模式**（Q246 benchmark → Q252f 测试）：
  `Repository` 少 `ModuleDirs` → `loadPackages` 循环体不进 → AST/SSA 全空跑，
  但 scip/git 兜出 209 节点/1 边、`status=success`，断言"非空"照样过。
  **守卫写在唯一入口**（`loadPackages`：ModuleDirs 空报错 + 全量构建零包报错；
  增量构建的"无变更包"合法，不能报错），别指望每个调用方都记得填字段。
- **"偶发红/重跑即绿"先怀疑"少跑了什么"**：那次 `verify.sh --quick` 三次红
  的真身是这条长期失败（详情被 `| tail` 截断，见 runbook #22）。

## Q252e SSA 阶段整改教训（2026-09-19）

- **"某阶段慢"的直觉要用阶段拆分先证伪**：go2o 的串行 `sp.Build()` 实测只占
  **429ms**（"15 分钟不出 build progress 是逐包串行导致"的假设不成立——
  那是内存压力/换页下所有阶段一起慢）。补 **2 个 stage 标记**后一眼看清：
  该段 2.43s 里真正的大头是 `ssautil.AllFunctions` 的**排序**（比较器现算
  `fn.String()` = O(n log n) 次字符串构造 → 1.77s；预计算 key 后 0.59s，
  **3.0×**）。并行建图只贡献 0.11s（318ms vs 429ms）。
- **"采样占比 0.13% 所以不用修"可能是采样阶段不对**：那次 profile 采的是
  发射阶段，排序在更早的 `newFuncSnapshot`——**先补阶段标记再采样**。
- **照抄上游并行结构但要收紧两个维度**：go/ssa `Program.Build` 的信号量模式
  可直接用，但 ①只对**模块包**生效（`prog.Build()` 连传递依赖图一起建）
  ②上界用 `--workers` 而非 `GOMAXPROCS`（并发度换内存）。并发原语抽成
  `runBounded` 后能用**确定性单测**（3 个任务必须同时在跑才放行）锁住语义，
  不靠计时断言。
- **并发度只该影响速度**：`TestBuildDeterminismAcrossWorkers`（workers=1 与 4
  产物逐条一致）是新增并发路径的必备门槛。

## Q246 构建性能整改教训（2026-09-19）

- **基准工具自身要验证**：`benchmarks/bench_test.go` 曾因 Repository 少了
  `ModuleDirs`，`loadPackages` 一个包都不 Load —— AST/SSA 适配器 0-1ms
  空跑、只有 scip/git 出产物，却一直输出"N 节点/耗时"看着正常（
  `-tags benchmark` 不在 `go build ./...` 覆盖内，编译错误也没暴露）。
  **看到"某适配器耗时 ≈0ms"先怀疑它没干活**，用产物数/日志交叉验证
- **峰值内存要采样式量**：`stage()` 里 `runtime.ReadMemStats.HeapAlloc`
  是**未 GC 的含垃圾值**，会高估；判定 OOM/换页用 `/proc/self/status`
  的 `VmHWM`（进程峰值 RSS）或 `/usr/bin/time -v` 的 Max RSS（父+子进程）。
  另注意后者被 scip-go 子进程主导，进程内 bench 才能反映本进程
- **换页/卡死的信号看上下文切换**：`/usr/bin/time -v` 的 minor page
  faults 与 involuntary context switches 数量级（本次整改：556 万 → 137 万、
  21.6 万 → 1.2 万）比 wall time 更能暴露内存压力
- **接口候选枚举按调用点重复全量扫描是隐性大头**：`implMethodsFor` 被
  `cf_call.go` 按**调用点**调用（不是按接口去重），每次重扫全部模块内包
  scope + `types.NewPointer`。**"每调用点重算全量枚举"模式一律池化 + memo**
  （同 Q221 的 dispatchRegs/regHits；池化版本的正确性用"旧实现留测试做
  golden"的方式锁定）
- **只为写盘而留在内存的全量产物是峰值来源**：包级缓存产物按包增量落盘
  （最后一块到达即写并释放），别等到构建尾部统一写
- **SQLite 写路径小改动收益可观且零语义风险**：删无用生成列索引、
  去 `AUTOINCREMENT`、表达式索引改部分索引、`cache_size` 上调——
  8000 行插入 143ms → 109ms（约 1.4x，allocs 不变）。但
  `synchronous(OFF)` / 构建期关外键 / `temp_store(MEMORY)` 是**语义/安全
  取舍**，不随手加（悬挂边语义、WAL 损坏风险）

## Q247–Q252b 构建期整改 + make it 修复教训（2026-09-19）

- **Mode/契约与注释不符是隐形内存黑洞**：`loadPackages` 注释写"依赖走
  fast 模式"，Mode 里却带 `packages.NeedDeps` → go/packages 把**整个传递
  依赖图的 AST 也解析并常驻**（go2o：774 reachable 包全带 AST / 3718
  语法文件 / 存活堆 955MB；去掉后 137 包 / 148MB）。**改 Mode 前先读
  依赖信息契约，并把契约写成测试**（`TestLoadPackagesDependencyContract`：
  模块包必须有 Syntax/TypesInfo，依赖必须有 Types、不得有 Syntax）
- **"释放 XX"这类清理要验证是不是空操作**：紧随其后的"释放依赖 AST"
  只对**返回切片里**的非模块包置 nil，而返回切片全是模块包 → 恒空操作，
  却让日志看着像"已做内存治理"。**删掉假动作，把约束写进契约**
- **内存热点用峰值 profile，不用退出时 profile**：进程退出时产物已释放，
  看不到峰值构成。`CODEINTEL_MEM_PROFILE=<file>`（ssa/memprofile.go）
  在阶段边界只在刷新峰值时落盘（GC 后写）。它一次就抓出 `sourceLine`
  占 25% 分配 / 66% 峰值 live（每函数重读重切整个源文件）——第三轮的
  头号目标
- **小内存机器自动兜底**：`CODEINTEL_MEMLIMIT` / 自动
  （`MemTotal<4GiB` → `min(1.5GiB,55%)`）；`GOMEMLIMIT` 已设则不覆盖。
  构建命令早期日志用 zap 会是 noop（logging.Setup 之前）——**关键信息
  要同时写 stderr**
- **`.tmp` 是本项目 TMPDIR，扫描仓库时必须排除**（`DiscoverModules`）：
  并发跑测试时临时 module 会被当成待索引 module（实测 `chdir … no such
  file or directory`）
- **bench 数字要看 RSS 峰值**：`make bench` 现在同时给 20ms 采样
  HeapAlloc 与 VmHWM；`/usr/bin/time -v` 的 Max RSS 含 scip-go 子进程
  （进程内 bench 才反映本进程）
- **Q248：每函数/每节点一份的缓存 = 千份重复 I/O**：`sourceLine` 行缓存
  建在 per-function 的 extractor 上 → go2o 1.2 万函数各自 `os.ReadFile` +
  `strings.Split` 整份源文件（占全部分配 25% / 峰值 live 66%，全仓源码才
  41MB）。**"每函数新建对象里带 I/O/解析缓存"一律提到 Index/构建级共享**
  （RWMutex + 读盘在锁外 + 负缓存），语义等价用"与直接读逐字比对"测试锁定。
  修后 in-process 峰值 RSS 中位 1961MB → 1271MB、CLI wall -56%
- **产物差异必须与"同二进制两次运行"对照才能定性**：Q248 跨版本 nodes
  归一化差异 1954 行、摘要 124 行，对照（同二进制两次）1890 / 122 行
  ——同量级才是"既有不确定度"；此前只比边集合（2 行）会低估噪声
- **验收数字要多次交错测量取中位**：Q247 单次 1464MB 是幸运样本，实际
  1464–2091MB；单点数字容易把"未达标"记成"差 4.6%"

- **Q249：全程序扫描类 API 只调一次**：`ssautil.AllFunctions(prog)` 原本
  在构建期被调 6 处（各自全程序走函数体+指令操作数建 map，累计 213MB），
  且 `summary_dynsql` 还按函数物化一份 `[]*ssa.Function`。**同一 prog 上的
  "全程序扫描"结果一律 Index 级快照共享**（快照一次安全性写在
  func_snapshot.go 注释：模块函数在 Build 后齐备、AllFunctions 自身的
  MethodValue 包装在首次调用即建出）。三轮累计：go2o CLI 51.4s/2.32GB →
  16.4s/1.09GB
- **profile 是"下一步做什么"的唯一依据**：每轮修完复测——alloc 4284 →
  3175 → 2027MB 逐轮下降；当前峰值 inuse 最高单项仅 9.7%（类型检查），
  剩余最大项是包缓存 JSON 序列化（15.6%，换格式属中等风险，需单独一轮）

- **Q250 图确定性：先用"同二进制双跑"分层定位**——① `workers=1` 双跑
  仍差 → **与并发写序无关，是 map 迭代顺序**（Go 按 map 实例随机）；
  ② ID 集合差异 vs **内容**差异要分开统计（本轮：ID 集合 0 差异，内容
  差异来自 kind/行号/属性）；③ properties 的 **JSON 键序**必须归一化后比
  再判定（sqlite json_patch / json v2 序列化顺序，无查询语义）。
  权威口径 `scripts/detcheck.sh <repo> [workers]`（大仓双跑四类产物比对）；
  CI 里的 fixture 双构建门槛**敏感度有限**（实测对某些回归不报红）
- **"先到先得"逻辑一律要查 map 迭代**：槽位名认领（`#t0` vs `#t0@行号`）、
  间接写传播工作列表（`indirect[caller][key]` 存在即跳过）——凡"谁先来谁
  占位/谁先来谁生效"的地方，迭代顺序就是输出的一部分。修法：按内容确定键
  排序（`ssa/order.go`）或改成**内容决定**（最早行号/字典序最小）
- **多写者属性/列的合并要"确定赢家"**：`nodes` 的 `func_id`/`ssa_op`/
  `type_string`/`origin_kind` 取字典序最小、`kind` 取更具体者、
  `file_path`/`line_start` 取非空且更小者（原先是首个/末个写者赢）。
  但**别动 `function_field_summary` 的 REPLACE**（Q215：新分析覆盖陈旧行）
  ——确定性要放在**发射端**选代表，改 SQL 层会破坏增量重建语义
  （既有测试 `TestSummaryReplace` 会红）
- **多 module 仓库的 SSA 位置曾经是错的（Q251 已修）**：每个 module 各一次
  `packages.Load` → 各自 `token.FileSet`，而 SSA Program 只有第一个包的
  Fset → 非首模块 `token.Pos` 解析出行号错乱（实测 461 行文件报 498 行）、
  `file_path` 为空（增量按文件删除会漏）。修法见下条（共享 Fset）
- **`gofmt -w <目录>` 会改到无关文件**：本仓库并非全局 gofmt-clean，整目录
  格式化会把别人的文件带进提交——只格式化自己改动的文件

- **Q251 多 module 必须共享一个 `token.FileSet`**：`packages.Config.Fset`
  可跨多次 `packages.Load` 传入同一个——否则各 module 一个 Fset，而 SSA
  Program 只用 `initial[0].Fset`（`ssautil/load.go`），非首模块的
  `token.Pos` 解析出行号错乱（461 行文件报 498 行）、`relPath` 失败导致
  `file_path` 为空（增量按文件删除匹配不到 → 陈旧节点残留）。SCIP 侧同理：
  scip-go 在 module 目录下跑，`document.RelativePath` 是**模块相对**路径，
  必须 `path.Join(moduleDir, rel)` 归一到仓库根（否则 `file:` 节点 ID
  跨模块碰撞）
- **缓存键覆盖不到的地方用版本号兜底**：`analyzerVersionHash` 只哈希
  `ssa/*.go`——改 orchestrator/scip 里影响产物内容（位置/路径）的语义时，
  必须递增 `pkgCacheFormat`，否则旧缓存把旧语义重放回来（Q251 实测：
  改完 reindex 无效，清 `.codeintel/cache` 才生效）
- **事实变了就改推荐**：Q251 设计时我推荐"28 处调用点按包 Fset 解析"，
  核查 x/tools 源码后发现 `Config.Fset` 存在 → 改成 3 行的共享 Fset 方案

- **Q252 包缓存：真凶是"收集语义不一致"而非序列化格式**：暖构建（缓存全
  命中）曾静默少 43 边 / 1313 摘要——埋点对比 `a.fd` 条目数即可定位：
  缓存收集用**覆盖**（`blkFD[owner] = fd`）而全局 map 用**追加合并**
  （`mergeFuncData`），闭包归外层函数时同一 owner 多次贡献 → 缓存只留最后
  一份。**同一份数据的多条产出路径必须共用同一合并语义**
- **缓存/重放类改动要加"冷 vs 暖"对照**：只测"两次冷构建一致"（确定性）
  测不出重放丢内容——`scripts/detcheck.sh` 现跑三次（冷1/冷2/暖3），
  分别验确定性与保真度；单测 `TestPkgCacheReplayFidelity` 同目录构建两次
- **序列化格式换型先量分配**：JSON→gob 实测 CPU -24~30%、体积 -16%，但
  **分配 +60%**（gob 更快但更费内存）——"换格式必然三降"是错的，用
  `BenchmarkPkgCacheSerialization` 这类对照基准决定，别凭直觉

- **Q252b 长期红灯的集成套件必须尽快修**：`make it` 6 个失败挂了近一个月，
  每轮改动只能"人肉比对失败集合"，本轮一查**其中 3 个是真 bug**（跨函数值流
  链断、渲染静默截断）——"失败集合一致"最终会掩盖真回归
- **同一语义的多条发射路径必须共用命名/合并口径**（本类问题已两次命中）：
  Q252 是缓存收集用覆盖而全局 map 用追加；Q252b 是同一 SSA 值在 alias pass
  （`v.Name()` → `#t0`）与 fe（`instancePath`/Q235-7 → `#*pp.T`）下**两个节点**，
  alias 边与 argument/returns 边分裂 → `query path`/`value-trace` 跨函数链断。
  修法：抽 `aliasSlot()` 单一口径两侧共用 + 回归测试断言"alias 端点 ==
  argument 端点"。**排查手法**：同一函数内同类型值节点数 > 期望即分裂
- **测试 fixture 必须与识别规则同步演进**：R29 把 grpc 注册识别改成按签名
  （`grpc.ServiceRegistrar`/`*grpc.Server`/调用 `RegisterService`）后，用
  `func RegisterX(s any, impl XServer)` 简化形态的 fixture 变成**不可能通过**
  ——fixture 要贴近真实形态（protoc 生成代码）；自包含靠**本地 stub module +
  `replace`**（grpc 也能这么造，不联网），别为了省依赖写假形态
- **渲染层"分组/优化"改动要覆盖全部深度**：分层渲染把 `depth>=3` 丢进未渲染
  桶 = 跨函数链在用户可见输出里静默消失；集成测试要断言"链上应出现的节点名
  + 边类型"，不能只断言首尾
- **Q252c 静默断链需要专门门槛**：Q252b 的两个真 bug（同一 SSA 值分裂成
  两个节点、`GetPath` 把 `maxDepth` 当节点预算）都**不会让既有断言变红**，
  只在用户查询时表现为"链断了"。`scripts/chaincheck.sh --repo <已索引仓库>`
  （= `integration/chain_gate_test.go`）把"图上存在的路径查询端找不找得到"
  变成数字：**一跳对（argument/returns）0 容忍必须 100%**，多跳抽样
  （**只取 ≥2 跳**——浅对测不出 BFS 扩展预算类 bug）/alias 连通数/分裂候选
  作基线项（`scripts/baselines/chain-<label>.json`）。改动图构建后须**先
  reindex 再跑**（脚本不建索引）。门槛自身用变异验证过会咬人（回退修复 →
  多跳 96/100 → FAIL）
- **`GetPath` 的 maxDepth 是路径深度不是节点预算**（Q252c 修的坑）：原
  `len(parent) <= maxDepth` 让 BFS 在发现面变宽时提前停止 → 可达的两点报
  "无路径"。写有界搜索时**限制扩展的必须是与语义相符的量**（深度），
  规模兜底另设常量（`maxPathVisited`）
- **Q252d "每调用全量扫表建 map"要按（build_id × 视图）惰性缓存**：
  `GetPath` 每次调用都扫边集建邻接表（ana 4.5 万边 77ms/次、go2o ~110ms，
  BFS 本身占比很小）——链完整性门槛 2 万对抽样因此要 35.7s。
  修法：`edge_graph.go` 按 **build_id + 视图**（`dataflow`/`calls`）缓存
  邻接表、**按视图惰性加载**（不做全边集缓存——否则单条 CLI 查询从 77ms
  涨到 114ms）、构建在锁外写锁内二次检查。命中后 81µs/次（≈950×），
  门槛 35.7s → **1.7s**（抽样还翻了 2.7 倍），Max RSS 无可见增长。
  **要点**：缓存原始产物（边）只依赖 build_id，**不要**引入版本常量
  （那是缓存"推断结果"才需要的，如 relationsAlgoVersion）
- **手工插 `build_metadata` 的测试要给全列**：`GetLatest` 直接 Scan
  `duration_ms` / `error_message`（不 COALESCE），NULL → `currentBuildID()`
  返回空 → 依赖 build_id 的缓存（relations 图、邻接表）**静默退化为每次
  现算**（Q252d 测试第一版就是这么红的）
- **按 commit 二分旧回归很便宜**：`runCLI` 是进程内调 `cli.Main`，`git worktree
  add <commit>` 后在该 worktree 直接 `go test -tags integration` 即可——把"哪个
  改动弄红的"从猜测变成二分


## 常用命令

```shell
go build ./...                      # 编译
go test ./...                       # 测试（需要 scip-go 在 PATH 或 go bin）
go build -o codeintel ./cmd/codeintel
# 对任意 Go 仓库构建索引并查询：
#   codeintel init --repo <path>          # 全量构建（--progress auto|plain|none：
#                                         #   Q253 进度条，只写 stderr；plain=逐行给 agent/日志）
#   codeintel update --repo <path>        # 增量更新（同样支持 --progress）
#   codeintel reindex --repo <path>       # 重建（删旧库绕过 schema 检查 + init）
#   codeintel query symbol|callers|callees|impact|table|grpc-routes|http-routes|cli-routes|external-deps ...
#   codeintel wiki --yaml wiki.yaml [--diagram plantuml|mermaid] [--max-entries N]
#                                          # 业务 wiki 生成（--ai 增量补缺写回 yaml 标注 # AI 初稿；
#                                          # 流程页 gRPC 服务子页按领域分目录）
#   codeintel domains --yaml wiki.yaml     # AI 业务域分析（R34/R38：事实包→agent 读文件→写回
#                                          # domains 区块含 services——重跑整体替换）
#   codeintel ask "<问题>"                # 项目上下文问答（无参数进入 REPL 多轮追问）
#   codeintel mcp --repo <path>           # stdio MCP server（Agent 接入）
# scripts/detcheck.sh <repo> [workers]     # 确定性 + 缓存保真度双（三）跑比对
# scripts/chaincheck.sh --repo <已索引仓库> --label go2o
#                                          # 跨函数链完整性（0 容忍 + 基线，§95）
# 完整命令清单见 codeintel-cli skill（~/.claude/skills/codeintel-cli/SKILL.md）
```

## 项目自举（Q236/Q237）：本仓库已建索引

分析/修改/搜索**本仓库自身**代码时，优先用 codeintel 查询（用户明确要求，
2026-08-22）——符号定义/签名/调用者/值流/影响面等**结构问题**用
`./codeintel query symbol|callers|callees|value-trace|context|impact`；
grep 仅用于字面文本（字符串/注释/日志）。

注意：
- **--repo 缺省 = 当前工作目录（Q237）**：在仓库内直接跑 `./codeintel
  query ...` 即可，无需 `--repo`；跨仓库传 `--repo <path>` 或已注册
  短名（Q238：`codeintel list` 查看；多命中报候选；文档不硬编码本
  仓库路径——目录可能被重命名）
- 本仓库 `.codeintel/` 已 reindex（2026-08-22，含最新分析逻辑）；逻辑变更后
  `update` 按 git diff 判断（工作区干净会跳过）——用 `reindex` 确保新逻辑生效
- 改 `assets/web/` 前端后须重新 `go build -o codeintel ./cmd/codeintel`
  （go:embed 打包，旧二进制嵌旧页面；用 `strings codeintel | grep Q230` 验证）

## 解析新项目的配置（目标仓库根目录的可选 YAML）

全部可选（不配置也能 init），配置后 `init`/`reindex` 生效；示例见本仓库根
`*.example.yaml`（复制为目标仓库根目录的对应文件名）：

| 文件 | 作用 | 何时配置 |
|---|---|---|
| `field-summary.yaml` | 外部函数/接口调用的读写语义（→ 表.列 虚拟节点） | query table/relations 缺表或缺写入方（自研 ORM/DAO、非 GORM 框架） |
| `modules.yaml` | 多模块 monorepo 模块划分（模块图） | 多 go.mod 或需模块级调用图 |
| `routes.yaml` | HTTP 路由人工表（模块图 http 边） | 需 HTTP 调用识别 |

关键点：
- `field-summary.yaml` 两类条目：`func`（静态调用：`orm_write`/`orm_read`/
  `reads`/`writes`/`reads_all`/`writes_all`/`param_index`）与 `iface`
  （接口方法动态 invoke：`method` + `kind`（write/read/filter/sql）+
  `obj_arg`/`where_arg`/`id_arg`/`sql_write`）。内置已覆盖 gof 全家
  （fw.Repository 两路径、db.Connector 原生 SQL、db/orm.Orm.Get、
  orm.Save）；其他框架（xorm/sqlx/自研接口）用 iface 自定义
- SQL 摘要支持 `?` 与 `$N` 占位符、多行 WHERE、ORDER BY 剥离
- 表名优先取实体 `TableName()`（否则类型名 snake_case）
- 改配置后须重新 `init`/`reindex`（增量 update 不含配置变更）

## 架构与目录

六边形架构：`internal/domain`（内核，零外部依赖）通过 Port 接口与适配器解耦。

```
internal/domain/          领域模型：CodeEntity/Fact/CanonicalID、IndexerPort/CodeRepository 端口
internal/canonicalizer/   Canonical ID 生成、SCIP symbol 解析（FromScipSymbol）
internal/logging/         ctx ↔ *zap.Logger + OpenTelemetry 链路追踪初始化。
                          Setup() 建 development logger（debug 级，stdout）与
                          stdouttrace 导出器；FromContext 在 span context 有效时
                          附加 trace_id/span_id 字段（entrylog 注入的日志用）
internal/orchestrator/    全量构建编排：并行适配器、独立超时 10min、分批 1000 条事务、降级报告
internal/infrastructure/
  scip/                   调用 scip-go 生成 SCIP 索引 → 符号节点 + IMPLEMENTS 边（conf 1.0）
  ast/                    go/packages AST 分析 → CALLS + IMPORTS 边（conf 0.8）+
                          服务入口标记（serves_http / serves_grpc）
  git/                    git log → COMMIT 节点 + MODIFIED_BY 边（conf 1.0）
  sqlite/                 nodes/edges/build_metadata 仓储；SaveBatchStats 分批提交；
                          GetRoots / Expand（图探索）
internal/server/          HTTP API：/api/roots（顶层入口）、/api/expand（点击展开）、
                          /wiki/ wiki 网页版（cli 注入，P2b）
internal/cli/             init / update / serve / query / wiki（--ai 增量补缺、--with-qa）/
                          mcp / ask（REPL 交互问答）/ before / trace / batch /
                          export / precompute relations / rule / workspace / list 命令
assets/web/               AntV G6 v5 前端（go:embed 嵌入；index.html + app.js）
scripts/entrylog/         AST 日志注入工具（见下）
```

## 链路追踪（OpenTelemetry）

- 入口（main）创建 root span（`codeintel.main`），ctx 贯穿 `cli.Main` → cmdInit/cmdServe
- `logging.FromContext(ctx)` 从 ctx 提取 span context，日志自动带 trace_id/span_id
- serve 的 Server 持有带 span 的 ctx，handler 日志同链路
- **坑**：`os.Exit` 不执行 defer——span.End 与 tp.Shutdown 必须在 os.Exit 前显式调用，
  否则 span 不导出；`zap.NewDevelopment()` 返回双值需 `zap.Must` 包裹
- 导出器为 stdout（PrettyPrint）；生产环境需替换为 OTLP 等

## entrylog 日志注入工具

`go run ./scripts/entrylog -dir <module 根>`：为所有顶层函数/方法注入

```go
logger := zap.L()                 // 无 ctx 参数
logger := logging.FromContext(ctx) // 有 ctx 参数（从 ctx 取 logger，缺失回退全局）
logger.Debug("enter <name>")
defer logger.Debug("exit <name>")
```

- 幂等（已注入跳过）、函数内有 logger 标识符时跳过、排除 _test.go
- 必须跳过 `internal/logging`（FromContext 注入自身会无限递归）与 `scripts/`
- **实现要点**：AST 只读定位 + 纯文本插入（format.Node 全量重写会把游离注释
  重排进调用表达式中间——踩过坑）；单行函数体 `{ return x }` 需拆行
  （Lbrace 后插入 + Rbrace 前补换行）；import 缺失时文本补入

## 设计考虑：用户视角 × AI 视角（每次设计必过，2026-08-23 定）

任何功能设计（新命令 / 新 MCP 工具 / 新前端 / 新接口 / 新配置）动手
前，必须过这两个视角，并在回复中明示各自价值（用户明确要求）：
前两轮批次（#211-221 / #227-233）就是这两视角清单的落地；新设计超出
已知缺口时，先说明属于哪个视角的哪个缺口再动手。

**新功能/新设计先 interview（grilling skill，强制）**：用户提出新功能
或新设计时，先调用 `grilling` skill（~/.claude/skills/grilling/，
设计树 + frontier 轮询 + 每问带推荐答案）对用户逐轮访谈，直到用户
确认共享理解再动手；纯 bug 修复/机械改动不需 full interview，但涉及
行为/接口选择时仍先问。

**交付前以用户视角通读产物（2026-08-23 用户批评后定）**：任何生成物/
渲染输出（wiki HTML、文档、报告）交付前，必须像目标用户一样通读
实际内容——不只看结构断言（标记/标签存在），要逐项检查内容质量
（字段名无噪音、职责非空、无断裂链接、无空区块刷屏）。wiki 的具体
检查清单见 wiki skill「交付前检查清单」。

**1. 普通程序员用户视角**——他拿到这个功能解决什么问题？
- 学习成本：命令名/参数/默认值是否自然；入口是否好找（CLI 顶层
  命令 / serve 首屏 / 编辑器）
- 出错体验：错误信息是否可操作（相似名提示、引导下一步）
- 心智模型：改完代码查询应「即查即新」（索引自动更新闭环）；
  查询结果能跳回代码（file:line）
- 安装/分发：一条命令装上（#227 一键脚本）

**2. AI Agent 视角**——Agent 用它减少多少次往返？
- 契约化：--json snake_case（#219）、输出 schema 自描述（MCP
  outputSchema，勿让 Agent 猜字段）
- 确定性：输出排序稳定（#218），同输入同输出
- 新鲜度：stale 标注可见（#217）+ 写工具自愈（#228）
- 输入友好：参数少、默认合理、支持批量（#221）与多仓库（#232）
- 概览/定位：陌生仓库先 roots/repo_summary（#229）、报错栈
  file:line → 符号（#229）、最近变化可见

## 关键设计决策（修改前必读）

这些是已确认的约定，改它们先与用户确认：

1. **Canonical ID**：`symbol:go:<import_path>:<name>`。方法名统一 `(T).method`，
   值/指针接收者不区分（与 scip-go 的输出一致）。文件节点 `file:<relpath>`，
   提交节点 `commit:<sha>`，包节点名取路径末段。
2. **置信度体系**：SCIP=1.0、CodeGraph(AST)=0.8、Git=1.0。CLI 查询阈值
   **0.8**（不要改回 0.85——TD.md 决策 10 的 0.85 与 5.1 表的 CodeGraph=0.8 矛盾，
   0.85 会把全部调用边过滤掉，这是已确认的偏差）。
3. **同边合并**：edges 有 UNIQUE(source_id, target_id, kind)，UPSERT 保留最高置信度；
   节点按 id UPSERT 合并 properties（json_patch），SCIP 写入的 kind/行号不被覆盖。
4. **外键约束**：edge 端点节点必须存在；不存在的边（如 Git 追踪到未索引文件）
   在 SaveBatchStats 中静默跳过并计数，不中断构建。
   **initializes 边**：`&T{}`/`T{}`/`new(T)` 产生 调用者→struct 的
   initializes 边（conf 0.8），仅 module 内 struct（Underlying 为
   *types.Struct 才建，排除 map/slice 复合字面量）。
5. **SCIP 引用边未实现**：scip-go 的定义 occurrence 只覆盖符号名（不含函数体），
   引用无法归属到调用者，因此没有 REFERENCES 边；引用类查询依赖 AST 的 CALLS 边。
6. **签名来源**：SCIP v0.7.1 协议不输出 signature，签名由 AST 适配器用
   `types.ObjectString` 生成（含接收者）。
7. **降级矩阵**（TD.md 9.2）：scip 失败 → 构建 failed；其他适配器失败 → degraded；
   MCP 工具永不抛错是未来 MCP 层的约定。
8. **scip-go 输出格式**：`-o <file> -q` 写文件（stdout 会混入进度日志）；
   occurrence range 为 3 值单行 `[line, start_char, end_char]`；
   子包的完整路径在 Namespace descriptor（反引号），`Package.Name` 只有 module 名。
9. **服务入口标记**（图探索顶层）：AST 适配器检测函数是否调用 net/http 或
   google.golang.org/grpc 包（含方法调用，如 `srv.ListenAndServe()`——注意方法选择器
   在 `info.Selections` 而非 `info.Uses`，本实现用 Uses 解析 Sel 已验证可行），
   写入节点 properties `serves_http` / `serves_grpc`。**坑**：外部包调用点不建 CALLS 边，
   标记 fires 时必须立即 emit 节点，否则节点永远不带标记。
10. **图探索 API**：`GetRoots` 返回 main 入口 + 服务入口（排除 `_test.go` 文件与
    `<pkg>.test:` 包）；`Expand` 返回双向 calls/implements/imports/initializes 直接邻居（上限 500 边）。
    前端在 assets/web/（G6 v5 UMD，CDN 引入），通过 addNodeData/addEdgeData 增量渲染，
    节点复用去重由前端 seen 集合保证。
    **G6 v5 布局坑**（playwright 实测）：`draw()` 不触发布局，增量数据必须显式
    `graph.layout()`；force 布局不处理孤立节点与增量新节点（位置留空会堆在原点）——
    addNode 时必须预置网格初始位置（style.x/y，固定 4 列避免 sqrt 回绕重叠）。
    位置读取用 `getNodeData(id).style.x/y`，`getData()` 读不到布局位置。
    坐标转换 API 参数为数组：`getElementPosition(id)` / `getClientByCanvas([x,y])`
    返回 `[x,y,z]`，传对象会得到 null。
    **交互**：单击显示信息，双击展开/收起。收起实现要点：expandedMap 记录每次
    展开新增的 nodes 与 edges（**已存在的邻居边也要记录**，否则收起删不掉）；
    收起用 setData 全量重建（G6 v5 removeEdgeData/removeNodeData 增量删除在批处理
    时引用已删节点报 "Node not found"）；展开令牌（expandToken）使收起时飞行中的
    展开回调失效，防止已删节点复活。
    **选中染色坑**（2026-08-13 实测）：选中切换必须先更新 selectedId 再调用
    setElementState——该 API 异步绘制（内部 await element.draw），样式函数在绘制
    时才求值（读闭包 selectedId）；顺序颠倒时旧染色的异步绘制后完成覆盖新染色，
    大图中快速点击稳定复现（14 节点图 13/14 次出错），表现为切换节点后边色不重置、
    点空白才恢复。节点标签为两行：`dir/basename` + 符号名（nodeLabel）。
    **剪枝规则**：展开节点时同向剪枝（pruneSiblings + rowClass）——只移除与展开
    节点同侧的兄弟（展开 callee 保留 caller 顶行，反之亦然）；已展开兄弟保留；
    rowClass 方向分类与三行布局一致（calls/initializes 出=down、implements/
    imports 出=up、其余=mid）。曾为"移除全部兄弟"，会把唯一顶行 caller 剪掉
    导致链路断头（用户反馈后修正）。
    **展开过滤**：有父展开时"过滤其他父"只拦 calls 入边（潜在 caller），
    且**按方向区分**——down/mid 类展开过滤（链式干净），up 类（caller）
    展开不过滤（展示调用方，链向上延伸，如展开 cmdInit 显示 Main）；
    has_method/implements/initializes 等入边是关联必须展示（否则双击
    接收者展开不出方法）。**收起顺序坑**：collectCollapse 须先递归回收
    整棵子树记录再判孤儿——边回收边判断会把连到后处理兄弟新增边的边
    误判为"有其他边"而残留节点。
    **树布局**：relayoutTree 方向感知分层——**箭头始终向下**：isUp(parent,
    child) 判断 child 是否通过任意关系（calls/has_method/implements 等）
    指向 parent（child 是 source）→ child 在上一行，否则下一行；每行
    水平居中。三行布局/rowClass/tail 定位同一原则（source 在上、target
    在下）。入口节点首次选择显式置于画布正中（addNode 网格位置在左上角，
    force 不移动孤立节点）。
    **增量布局**：展开时 relayoutTree(root, prevY)——已有节点保持 y，
    新节点行插值；prevY 必须在 addNode 前收集（否则新节点网格位置被当
    已有位置）；updateNodeData 无返回（非 promise），fitView 须 setTimeout
    等动画完成（否则包围盒按旧位置算，缩放无效）；收起走全量重排。
    **无记录节点定位**：不在展开树中的节点（剪枝后作为邻居重新出现的
    父）按边定位——calls 出边（该节点是 caller）在相邻节点上一行、其余
    下一行，保证箭头始终向下；无法定位才追加最后一行（曾一律丢最后行
    导致父节点掉到底部、箭头朝上）。
    **全量重排兜底**：relayoutTree 无 prevY（收起/全量）时按 minD 偏移
    分层（startY + (d-minD)*rowGap）——曾写坏为全部行落 startY=80，
    收起后所有节点堆一行、箭头全向上（用 audit 探针：每步检查所有
    calls 边 source.y < target.y）。
    **边方向修正**：BFS/tail 后对所有边做 source<target 循环修正（共享
    节点方向冲突）；修正触发（depthChanged）时 prevY 错位，须整树按新
    深度干净分层（否则 rowY[0] 被旧位置节点占据导致同层）；行超画布
    底部也要 fitView。**剪枝隐藏可配置**：hideKinds（默认 {calls}，
    localStorage codeintel.hideKinds）——只隐藏"同侧且属于勾选关系"
    的兄弟（edgeKind 检查）。
    **信息栏**：右侧常驻 320px 侧边栏（#sidepanel），单击节点复用
    /api/expand 渲染分组信息（基本/文档注释/提交/关系按类型）。**坑**：
    G6 v5 给容器设内联 position:relative 会覆盖样式表的 absolute——
    侧边布局必须用外层 wrapper（#main-area 绝对定位 right:320px，
    容器 100% 填充），否则 right 不生效且节点会被面板遮挡不可点。
    **方法线 has_method**：AST 适配器 emitMethodReceiver 建立
    has_method 边（接收者类型 → 方法，方向为用户确认的"由接收者指向
    方法"；曾为 method→receiver 的 has_receiver，2026-08-13 反转并更名，
    重建需 clean 清库否则旧边残留）；轻量节点模式同 createObject；
    前端边虚线 [5,2] 标注"方法"，信息栏按视角拆分（struct 出边=方法
    （N）、方法入边=接收者（N））。**坑**：三行布局中间行单个节点
    （如接收者）会落在中心节点正上方（placeRow 单个节点 start=cx）——
    须 offsetSingle 偏移到中心右侧。
    **implements 方向**：SCIP 适配器 is_implementation 关系输出
    接口 → 实现者（2026-08-13 反转，用户确认"接口指向实现"）；
    前端三行布局/rowClass 的 implements 分类对调（接口出边=下行、
    实现者入边=上行），信息栏按视角拆分（接口=实现者（N）、
    实现者=实现（N））。重建需 clean 清库。
    **嵌套调用**：参数位置的调用（isArgCall 判定）不建 calls——
    handleNestedArg 递归建 passes_result（"持有返回参数"）：A(B(C()))
    → A→B、B→C；callee 的非调用实参（函数引用）走 argFuncRef 持有
    参数（passes_to）。
    **接口整体节点**：接口方法（desc[2] 为 Term）不建独立节点——
    canonicalizer 标记 IsInterfaceMethod；SCIP 适配器跳过节点与方法级
    implements；AST 适配器 isInterfaceMethod（接收者类型是接口）跳过
    调用边与节点。重建需 clean 清库。
    **节点配色**：KIND_COLOR 每种类型独立色（函数蓝/方法青/结构体绿/
    接口紫/包橙/文件灰/提交深灰/对象薄荷绿）。
    **源码弹窗**：函数/方法节点信息栏 Source Code 按钮 → /api/source。
    后端按需读文件 + go/parser 提取声明区间（LineStart 精确 → 行范围
    → 名称三级匹配，容忍文件修改后行号漂移）；仅 function/method，
    路径解析须验证仍在仓库根内（防目录穿越）。前端 highlight.js
    （CDN github.min.css 主题）Go 高亮，hljs 未加载时降级 textContent。
11. **serve 运维坑**：`serve` 打开的是 .codeintel/codeintel.db；重建索引
    （rm -rf .codeintel 或 init 清库）会留下持有已删除文件句柄的旧 serve 进程，
    表现为 API 返回旧数据。改库后须重启 serve。
12. **服务→领域归属（R38 用户定案）**：wiki.yaml domains.services
    （AI 归纳 + 人工确认）优先；方法调用链涉及包投票兜底（近似）；
    无匹配落「其他」目录。投票会被基础设施兜底域污染——显式配置
    才是正解。
13. **agent 子进程 cwd = 目标仓库根（R38 用户定案）**：claude/codex
    对 cwd 项目内文件 Read 免权限弹窗——domains 事实包（仓库
    .codeintel/ 内）由 agent 读取；ask/wiki --ai/domains 全链路注入，
    新增 AI 调用场景必须传仓库 abs。
14. **流程页结构（R37/R38）**：main 入口节（保留）+ HTTP 路由入口节
    （handler_id 展开/同 handler 去重/resolver 分组）+ gRPC 服务入口
    节（每服务独立子页、按领域分目录、方法级 (Impl).Method 展开）；
    每节/每页入口上限折叠（--max-entries 默认 15）。

## 开发操作坑（踩过，勿重蹈）

- **Bash 工具输出捕获管道坏（2026-08-24）**：bash 本身完全正常（命令
  实际都能执行，rc=0），是 Bash 工具的输出捕获管道坏了——工具层拿不到
  stdout，表现为空输出 / exit 1 / "Stream closed" 三类症状混杂。处理：
  **命令输出重定向到文件（`cmd > tmp/x.txt 2>&1`）+ Read 读取**；不要因
  exit 1 误判命令失败——重定向后文件有内容即命令已执行。判断命令真实
  结果以文件内容为准（可 echo 标记位）。
- **改代码后忘 go build 用旧二进制验证（2026-08-24 R37）**：reindex/
  wiki 等验证跑的是旧二进制——"去重没生效"误判浪费一轮。改完代码
  先 build 再实测（命令速查的验证顺序：test → build → 实测）。
- **agent 子进程权限模型 = cwd 项目白名单（2026-08-25 R38）**：
  claude -p 对 cwd 项目外文件 Read 弹窗无人应答即拒绝（domains 事实
  包在 go2o/.codeintel/ 读不了 → AI 分析失败）。已根治：agentRunner
  注入 dir——子进程 cwd = 目标仓库根（ask/wiki --ai/domains 全链路）。
  新增 AI 调用场景记住传仓库 abs。
- **domains 重跑两坑（2026-08-25 R38）**：① yaml domains 新旧并存
  （setDomain 按名追加——重跑前必须 clearDomains，整体替换语义）；
  ② 任务加重后超时 240s 不够（读 30KB 事实包 + 归纳 services）——
  domains 分析 360s。
- **plantuml 边 label 语法（2026-08-25 R38）**：`A -->|6| B` 必须转
  `A --> B : 6`——`--> 6 : B` 会把 6 当目标节点名（PNG 渲染出数字
  节点，线标签变长符号 ID）。mermaidToPlantuml 的 graph 转换。
- **服务→领域归属**：yaml domains.services（AI 归纳+人工确认）是
  正解；方法调用链投票兜底会被"基础设施兜底域"污染（go2o 全部 infra
  包归平台系统域 → 投票天然偏向它）——兜底只当近似。
- **`pkill -f "codeintel-e2e serve"` 会匹配自身自杀**（2026-08-14 复发两次）。
  清理 e2e 进程用 `pkill -x codeintel-e2e`（精确进程名）；杀完 sleep 0.5 再起新进程。
- **git 命令务必在 codeintel 仓库目录执行**：曾在 `/home/schaepher/Codes/验证仓库`（验证仓库）
  误执行 `git add -A` 把 `.codeintel/` 文件提交进 验证仓库（已撤销）。验证仓库只读不改。
- **schema 无自动迁移**（`PRAGMA user_version=4`）：改动表结构后验证仓库必须
  `codeintel clean` + `init` 重建，否则旧库 schema 不匹配报错或数据形态过时
  （曾因旧库缺 GORM 虚拟节点误判功能未生效）。
- **日志已切文件**：所有带 `--repo` 的命令日志写入 `.codeintel/codeintel.log`，
  stdout 只承载查询结果——排查问题看日志文件，不要从 stdout 找日志。
- **TMPDIR 指向仓库 .tmp/**（R67：不再写 /tmp——tmpfs 配额小；.gitignore
  已排除 .tmp/）。R28 曾因"TMPDIR 在仓库内 → t.TempDir() 在仓库内 →
  git 检测假失败"切到仓库外 .tmp-build——R67 实测（TMPDIR=$PWD/.tmp
  全量 cli 测试 -count=1 通过）问题未复发（当时修复已根治）。Makefile/
  verify.sh 自动设置（目录存在才设——CI 保持默认）；**手动跑 go 命令
  同样带 TMPDIR=$PWD/.tmp**。
- **验证矩阵**：make test（-race 全量，13 包）/ make it（integration，需 scip-go）/
  make e2e（playwright 前端回归，端口 8096，E2E_REPO 指定）——改完代码三件套都要过。
- **工作流约定**（用户明确）：每个功能先写测试再实现（测试先行）；开发中的
  疑问必须先向用户确认（设计树访谈模式）；改完验证后必须 git push。
- **验证形态矩阵**（2026-08-17 XORM 教训）：框架适配/查询类功能，测试必须覆盖
  **真实调用形态**；涉及位置/路径产出的改动必须覆盖**多 module 仓库**
  （嵌套 go.mod 的 file:line 与 file_path，Q251 教训——单 module 仓库
  完全不暴露）——具体类型静态调用（如 *xorm.Session，用 replace 本地模块
  模拟真实包路径）+ 接口动态调用，自建 fixture 只算第一层；缓存/优化必须测
  **读命中闭环**（写入→读取→命中→失效），不只测写入；性能改动带优化前后
  实测对比（数字验收）。integration/fixtureapp 提供固定真实形态代码库，
  适配器改动后须跑它验证（CLI 全管道回归）。
- **schema 与语义变更需重建验证**：适配器改动后验证仓库必须 `init` 全量重建
  （增量 update 只更新变更文件，可能残留旧形态数据）。
- **relations 推断变更必须递增 relationsAlgoVersion**（rg_cache.go，当前 q208）：
  缓存键 = build_id + 版本，改 rg_*.go/relationsFor* 不递增会残留旧缓存
  （Q199 教训；Q205/Q208 各复发一次）。快速验证手段：
  `sqlite3 <repo>/.codeintel/codeintel.db "DELETE FROM relation_candidates"`
  强制重算；缓存存**未过滤全量**（Q208），hops 过滤是读取期行为——窄参数
  查询不会污染缓存，放宽参数始终可见长链。
- **前端截图验证**（2026-08-18 ER 图教训）：playwright 视口宽度必须 ≥ SVG
  宽度（默认 1440 会横向截断——用 2600 或元素级截图）；截图后必须用 PIL
  像素解析验证内容范围与线色（本环境 Read 工具读不了图片）；fullPage 只
  截视口宽；SVG 高度要预留行间隙（绕障线垂直段超出会被裁剪）。

## 已知限制

- **多 go.mod 已支持**（2026-08-15 P2-3）：递归扫描根下所有 go.mod，
  每 module 独立加载/独立 scip-go；go.work 根（无根 go.mod）仍不支持
  （报错提示进入模块目录）
- sqlite-vec 向量表未创建（Semble 未接入）；schema 版本由 PRAGMA user_version=4 管理，
  版本不匹配时报错提示 `codeintel clean` 重建
- 未实现：LLM 摘要、Semble。MCP 的 query 能力经 `codeintel mcp --repo <path>`
  stdio server 暴露（Q243：tools/list + tools/call，Agent 直接调用）；serve
  内嵌 MCP 方案已取消（2026-08-15 Q135 定）——AI 亦可直接用 CLI 查询命令，
  --json 即结构化契约

## 测试

测试先行（先写失败测试再实现）；验证矩阵：make test（-race 全量，13 包）/ make it（-tags integration，需 scip-go）/ make e2e（playwright 前端回归，端口 8096，E2E_REPO 指定）。

**基建速查**（写测试前先读对应包）：
- ssa：`indexFixture(t, files)` → (nodes, facts)；`indexFixtureFull` → (nodes, facts, summaries)——临时 module（`moduleGoMod`）跑完整 Adapter.Index（含别名/摘要/派发），fixture 可加 `external/` 子包与 `field-summary.yaml`；helper `findFieldAccess`/`findSSAValue`（slot 前缀匹配）/`factsFrom`
- sqlite：`newTestRepo` + `save` + `node`；TraceForward 测试的参数节点须补 `type_string`（B2 后起点须类型匹配）
- action：`seedRepo` → (a, dir)；TraceConditions 测试须自行写真实 main.go（seedRepo 只建节点不写文件）
- integration：`go:build integration` 标签；`runCLIOut` 跑 CLI；`scipGoAvailable` 跳过

**断言坑**（踩过）：SSA 临时名 tN/lifting 提升不稳定 → 节点 ID/名称用前缀或通配匹配；fixture 行号数错、断言提前 return 会掩盖失败 → 先收集后判断；依赖 map 迭代顺序的不稳定 bug 用 `-count=10` 复现；alias 边 source 是值节点（funcID#slot），expand 函数节点不返回 alias。
**SQLite 坑**：单连接上外层 rows 迭代中开新 Query 会**挂起死锁**（GetTableColumns 踩过）——先收完外层行、Close 后再开内层查询；Metadata 数字断言注意 int vs float64（emit 用 int，SQLite json_extract 回读是 float）。

- `internal/canonicalizer`：纯单测（SCIP symbol 解析的各种形式）
- `internal/orchestrator`：端到端测试，临时 Go module → FullBuild → 校验图数据
  （需要 scip-go，缺失时自动 skip）

### 验证环境教训

- **WAL 模式 SQLite 构建中强杀会损坏 DB**：reindex/init 跑大仓库（go2o 3 分钟）时用 timeout 强杀 → WAL 未 checkpoint → `database disk image is malformed`（下次操作报错）。验证用**后台运行 + 轮询**（run_in_background），或 timeout 给足余量；损坏后删 db/-wal/-shm 重建。serve 与 reindex 并发操作同一 DB 也要避免。

## Q221 构建期性能教训

- **懒初始化兜底掩盖初始化遗漏**（同模式两次）：`ext.dispatchRegs == nil` /
  `ext.regHits == nil` 本意防 nil，因上层从未初始化变成"每函数全量预处理"
  ——dispatchRegs（每函数全图扫描 305s CPU）与 regHits（每函数遍历注册点
  ~180s）修复后 go2o 构建 5m16s → 15.6s。**每函数新建的 extractor 里
  出现"全量预处理 + nil 兜底"组合时，检查是否该提到 Index 级一次**
- **对象池收益要实测**：GC ~40% CPU 时对象池看似合理，实测零贡献（CPU
  不变）——regHits 修复后 GC 已非瓶颈。收益不足且有 use-after-free
  生命周期风险，回滚。**性能优化以 CPU profile 重采样为准，不靠直觉**
- **SQLite 驱动切换实测否决**：mattn（cgo）vs modernc（纯 Go）批量写
  基准——modernc 慢 38%（cgocall 边界开销 < 纯 Go 翻译引擎执行劣势）。
  性能判断用基准数据，不假设"无 cgo 更快"

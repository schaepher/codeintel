# 构建期性能优化记录（Q221，2026-08-19）

**目标**：降低大项目（go2o，135 包 / 12875 函数）全量构建的时间与内存占用。
**结果**：冷启动 **5m16s → 16.8s（18.8 倍加速）**；CPU 总量 660s → 48s；
峰值 RSS 2.71G（workers=8 + GOGC=40，8 核 7.4G 机器）。缓存命中构建 15s。

---

## 1. 测量：耗时分布

构建管线阶段计时（orchestrator stage + build stage 日志）：

| 阶段 | 耗时（基线 workers=1） | 占比 |
|---|---|---|
| loadPackages（go/packages 加载） | 1.9s | <1% |
| **emitFunction 循环**（SSA 提取） | **5m11s** | **98%** |
| flush（SQLite 分批写库） | <1s | <1% |
| computeAliases / emitDispatches 等 | ~10s | ~2% |

首轮实验：`--workers 8` 冷启动仅 3m27s（1.56 倍加速）——CPU 利用率
User/Wall = 594s/207s ≈ **2.9 核**，暴露并行度不足。

## 2. 优化 1：包间并行（全库函数块池）

**根因**：原实现包循环**串行** + 包内 200 函数分块并行。go2o 135 包
平均 95 函数/包 < 200 分块阈值 → 每包只有 1 块 → 同一时刻仅 1 个
goroutine 在跑（8 核只用了 1 核）。

**改造**（adapter_index.go）：未命中缓存的包全部拆块（块内单包，200
函数/块）进**全局 worker 池**并行；块产物按包收集（pkgCollect），全部
完成后按包保存缓存。阶段划分：A 串行缓存检查 → B 并行块池 → C 串行
保存缓存。

**结果**：3m27s → 2m20s（workers=8）。

## 3. 优化 2：dispatchRegs 初始化 bug（pprof 46% 热点）

**定位**：`CODEINTEL_CPU_PROFILE` 采样一次冷构建，pprof top：

| 函数 | flat | cum | 说明 |
|---|---|---|---|
| collectDispatchRegistrations | 25.5s | **305.5s（46%）** | 动态派发注册收集 |
| ssautil.AllFunctions.func1 | 38.8s | 123.7s | 全程序函数遍历 |
| typesinternal.ForEachElement | 6.5s | 101.0s | 类型遍历 |
| runtime.*（GC 系列） | — | ~250s（38%） | spanClass/scanSpan/scanObject |

**根因**：`Adapter.dispatchRegs` 从未初始化（零值 nil map）→
fieldExtractor 创建时 `dispatchRegs: *dispatchRegs` 解引用复制后
`ext.dispatchRegs == nil` **永远成立** → cf_call.go 懒初始化兜底
`if ext.dispatchRegs == nil { collectDispatchRegistrations(prog, ...) }`
→ **每个函数都触发一次全程序 AllFunctions 扫描**（12875 次 × 全图
遍历 ≈ 305s CPU）。

**修复**：Adapter.Index 级初始化一次（须在 sp.Build() 之后——SSA 指令
已构建，MakeInterface 才存在），各 extractor 共享只读 map。

**结果**：2m20s → **40s**（总加速 7.9 倍）；CPU 637s → 227s（分配
减少连带 GC 开销下降）。

## 3.5 优化 2.5：regHits 预处理 Index 级（pprof 复查再抓 2.9 倍）

修复 dispatchRegs 后重采 pprof：GC/分配仍占 ~60%，业务热点
candidateKey 7%——复查发现**同模式第二处**：`ext.regHits`（Q168
注册命中 O(1) 判定表）也是 extractor 懒构建——每个函数都重新遍历
全部注册点 × 动态类型全部方法 × candidateKey（Type().String()
分配）。修复：Index 级 buildRegHits 一次，extractor 共享只读。

**结果**：adapters 34.9s → 12.0s；CPU 227s → 48s（regHits 重建
贡献 ~180s CPU）。

## 3.6 尝试：对象池（收益不足，已回滚）

pprof 显示 GC 系列 ~40% CPU → 尝试 CodeEntity/Fact sync.Pool：flush
写库后统一 Release 回收（失败边跳过——o.failedEdges 持有同指针待重试；
缓存保存改为浅拷贝收集——原对象被 flush 回收后阶段 C 不得读脏对象），
33 个创建点脚本化转 NewEntity/NewFact（内嵌 IIFE）。

**实测**：12.0s → 10.7s（-1.3s），但 CPU 48.6s vs 48.4s **几乎不变**——
收益全部来自同期 BatchSize 改动（单独验证 10.7s 一致），对象池本身
零贡献（regHits 修复后 GC 已不是瓶颈；浅拷贝收集抵消部分收益）。
**回滚**：生命周期约定（use-after-reuse 风险）+ IIFE 可读性代价不值
8% 且不可复现的收益。保留 BatchSize（10000→20000，cgocall 减半）。

## 3.7 SQLite 驱动切换基准（modernc 纯 Go，实测否决）

动机：pprof cgocall 16%（mattn/go-sqlite3 的 C 调用边界）——切换
modernc.org/sqlite（纯 Go 实现）能否消除边界开销？

**基准**（tmp/bench-sqlite，独立模块）：14 万节点 + 20 万边批量
INSERT OR REPLACE，WAL，事务批 2 万（模拟构建期 flush 形态）：

| 驱动 | nodes 14 万行 | edges 20 万行 | 合计 |
|---|---|---|---|
| mattn（cgo，现状） | 0.65s | 2.50s | **3.15s** |
| modernc（纯 Go） | 1.15s | 3.20s | **4.36s（慢 38%）** |

**结论：不切换**。cgocall 16% 是边界开销上限，但 modernc 是 C→Go
自动翻译实现，SQLite 引擎（B-tree/WAL/排序）执行慢于原生 C——写
路径净慢 38%，读路径（relations 全表扫描/BFS join）通常同样吃亏。
纯 Go 的收益仅工程性（无 cgo/交叉编译便利），非性能。若需消除
cgocall 边界，替代方向：减少写库调用次数（BatchSize 已做）。基准
脚本保留在 tmp/bench-sqlite（gitignore），可复测。

## 4. 优化 3：GOGC 实验

pprof 显示 GC 相关 ~38% CPU（GOGC=40 并行下扫描开销）。实测
（workers=8 冷启动）：

| 配置 | Wall | User CPU | 峰值 RSS |
|---|---|---|---|
| GOGC=40（原默认） | 39.9s | 227s | **2.73G** |
| GOGC=100（实验） | 36.4s（+9%） | 184s | **4.44G（+54%）** |

结论：GOGC=100 时间收益小、内存代价大——**40 是时间/内存平衡点**。
默认保持 40；新增 `CODEINTEL_GOGC` 环境变量可调（内存充足可调高）。

## 5. 默认 workers = min(NumCPU, 8)

原默认 1（串行）。8 核实测冷启动 5m16s → 40s、峰值 RSS ~2.9G 可接受。
上限 8 防大机器内存翻倍；小内存机器可 `--workers 1` 调小。
init / reindex / update 三处默认值统一（defaultBuildWorkers）。

## 6. 最终对比

| 指标 | 基线（workers=1） | 优化后（默认 workers=8） |
|---|---|---|
| 冷启动 Wall | 5m16s | **15.6s（20.2×）** |
| CPU 总量 | ~660s | 48s |
| 峰值 RSS | 2.33G | 2.93G |
| 缓存命中构建 | — | 15s |

## 7. 诊断工具

- `CODEINTEL_CPU_PROFILE=<file>`：构建类命令输出 CPU profile
  （main 内 Start/Stop，os.Exit 前落盘；`go tool pprof <bin> <file>` 分析）
- `CODEINTEL_GOGC=<n>`：覆盖构建期 GOGC（默认 40）
- 构建阶段计时日志：`orchestrator stage` / `build stage`（含 heap_mb）
- `CODEINTEL_SCIP_PATTERNS=off`：强制全量 SCIP（对照增量 patterns 的 A/B 逃生口）
- 日志口径（P1-07/P1-08/P1-13）：`func data`（funcs/calls/arg_paths）、
  `scip artifact cache hit/saved`、`scip incremental patterns`、`flush latency`
  （p50/p95/max，最近秩法）

## 8. 经验教训

1. **懒初始化兜底掩盖初始化遗漏**：`ext.dispatchRegs == nil` / 
   `ext.regHits == nil` 检查本意是防 nil，但因上层从未初始化而变成
   "每个函数都全量预处理"的隐形性能黑洞——同模式出现两次（dispatchRegs
   46%、regHits ~180s CPU），修复后合计贡献构建期 95% 以上收益。
   pprof 是唯一能定位的手段；修复后必须重采确认。
2. **并行度要先于算法优化**：8 核机器 CPU 利用率 2.9 核时，任何
   单线程算法优化都封顶于 ~1 核收益；先让核数满起来。
3. **测量数据驱动决策**：GOGC=100 "看起来更快"的直觉被 RSS +54%
   数据否决；默认 workers 从 1 到 8 由冷启动实测（5m16s→40s）支撑。

---

## 目标仓库全量 GC 采样与优化方向（2026-10-13）

- **采样**：临时副本完整 reindex（排除 `scripts/`、`vendor/`，独立 Go 包缓存）；
  未修改目标仓库原有 `.codeintel` 索引。
- **结果**：耗时 25m16s，status success，产出 2,400,240 符号、7,423,969 边；
  日志仍有部分包的编译/类型错误——success 不代表所有包均完整解析。
- **GC 热点**：运行时 GC 占比约 26–27%；CPU profile 中
  `runtime.tryDeferToSpanScan` 占采样 CPU 43.6%，GC 标记路径累计约 51.7%
  （调用栈包含关系有重叠，不可相加）。
- **存活堆分配热点**：`go/types.(*Checker).recordTypeAndValue` ≈693MB、
  `canonicalizer.GoSymbolID` ≈655MB、`ssa.mergeFuncData` ≈378MB、
  `ssa.buildIdentIndex` ≈208MB；分配热点中 `encoding/gob` 写入累计 ≈28.7GB
  （累计分配 ≠ 存活量/GC 扫描量）。
- **emitFunction 容器估算**：≈17.5 万函数、192.5 万 map、2390 万条目，
  平均 ≈12.4 条/map；最大采样容器 8733 条（抽样外推，非精确计数）。
- **结论边界**：CPU profile 能定位 GC 扫描函数，heap profile 能定位分配
  调用点，但二者不能确定被扫描对象的精确 Go 类型及各类型扫描字节占比；
  不能断言 emitFunction 的 map 是唯一/最大扫描来源。
- **优化思路**：纯值类型（整数数组/切片）底层元素无需扫描（切片头仍含
  指针）；元素含字符串/指针/含指针字段仍需扫描；map 不能仅靠改数组/切片
  消除扫描。先选生命周期短且高频的容器做小范围实验（整数索引/紧凑纯值
  缓冲区替代指针丰富结构），以 GC CPU、扫描堆、峰值 RSS、产物一致性做
  同仓库 A/B 对照。避免无差别池化 map（可能保留桶与引用、延长存活并增加
  后续扫描）。
- **诊断开关**：`CODEINTEL_EMIT_SAMPLE=1` 时每 10 个函数抽样一次
  `fieldExtractor` 容器规模（函数数/map 数/entry 数/最大 entry + 堆/GC），
  见 `internal/infrastructure/ssa/fe_emit.go` 与 `emit_sample.go`。

## 包缓存分块 gob 编码（v4，2026-10-14）

- 问题：v3 整包 gob 消息会在 gob 内部缓冲完整载荷（基准 44.58MB/op），
  大仓缓存写盘时造成大块分配与 GC 触发压力。
- 改法：节点/边/摘要每 200 条一个 gob 消息 + 独立结束标记；`pkgCollect.save`
  直接编码已有块（不再先拼接整包指针切片）。
- 局部基准（三次中位数）：整包 40.26ms / 44.58MB/op → 分块 25.72ms /
  3.18MB/op（分配字节 ≈ -92.9%，局部编码耗时 ≈ -36.1%）。
  **不外推全量构建收益**；字节缓冲本身无需 GC 扫描，减少的是分配压力。
- 正确性：跨块往返、截断/缺结束标记/结束标记带数据/尾随消息拒绝、旧格式
  拒绝均有测试；go2o 冷重建→暖命中产物计数一致（117998/136028/561）。

---

## 重复解析/重复计算治理 + 全量复查（2026-10-14）

按 codeintel-changelog3 的候选清单实测并实现，未实施的记录数据/原因/触发条件。

### 已实现（每项含局部基准）

| 项 | 实现 | 局部测量 |
|---|---|---|
| 函数身份构建级缓存 | `funcSnapshot.identityOf`（含失败结果）；`funcIDOf/FuncIDOfFn` 走缓存 | 消除每调用点重算 canonical ID |
| 静态调用点反向索引 | `funcSnapshot.staticCallSites`（惰性一次）；`resolveParamCandidates` 查索引 | fixture 100 目标：旧按目标重扫 1.52ms/op → 索引 0.62µs/op（≈2450×） |
| Return 操作数缓存 | `funcSnapshot.returnOperandsOf`；cf_resolve/summary_orm/aliasPass 共用 | 同一被调函数跨 extractor 只扫一次 |
| 类型表名缓存 | `funcSnapshot.tableNameCached`（按 `*types.Named`，空结果也缓存） | TableName 解析跨 extractor 只算一次 |
| SQL 解析结果缓存 | `funcSnapshot.sqlParseCached`（按原始 SQL，上限 8192，含失败；统计口径按调用） | 命中复用表/列/关联；AST/启发式计数不因命中失真 |
| 摘要包合并 map 复用 | `pkgCollect.save` 复用首块 map 作合并目标 | 每包少一次整包 map 分配 |

**未采纳（有数据）**：`mergeFuncData` 目标切片预分配——实测与不预分配**中性**
（1079326 vs 1079237 B/op，4803 allocs 相同），按项目纪律回滚为 no-op。

### 大仓 A/B（go2o，同一临时仓库，交错运行）

- 旧版（`214df5f`）warm init：9.93s/949MB、8.21s/931MB；
  新版 warm init：9.25s/935MB、8.18s/937MB。
- **产物逐行全等**（nodes 117998、edges 136028；`diff` = 0 行，含 kind/端点）。
- 结论：这些缓存消除的是局部重复扫描（微基准收益显著），但 go2o warm 构建的
  wall/RSS 由 SCIP/SSA 计算与 I/O 主导，端到端**未测出稳定提速或内存改善**；
  不宣称全量收益。
- 期间抓到并修复一个真回归：`emitGlobalInit` 的轻量 extractor 未设 `funcs`，
  身份缓存对 nil 快照返回空 → 丢 50 个 global-init `ssa_value` 节点 +
  50 条边（117998→117948）。修法：`fieldExtractor.identityOf` 在 `funcs==nil`
  时回退纯 `funcIdentity`，并显式给 `emitGlobalInit` 传快照（§field_trace）。

### 运行参数对照（GOGC，go2o warm init 交错 2 次）

| GOGC | wall | 峰值 RSS |
|---|---|---|
| 40 | 11.82s / 8.41s | 958MB / 898MB |
| 100 | 8.30s / 8.13s | 1103MB / 1103MB |

GOGC=100 更快（中位约 -19%），峰值 RSS 约 +19%——以内存换速度，非算法收益。
（`/usr/bin/time` Max RSS 含 scip-go 子进程，两设置同口径，仅相对可比。）

### 阶段定位（go2o warm init 8.37s）

loadPackages 636ms、scip index 2.12s、ast packages 649ms、ssa 4.49s
（ssaBuild 214ms / funcSnapshot 413ms / dispatchRegs 35ms / emitFunction 2.71s /
finishIndex 1.04s）、runAdapters 6.36s（并行）、flush 1.87s。热点在 SCIP 与
SSA 计算，不在重复扫描。

### 紧凑容器 / 对象复用评估（未采纳，触发条件见下）

- 纯整数切片/数组元素无需 GC 扫描，但切片头仍含指针；字符串、指针、含指针
  字段仍需扫描。要真正免扫描需把字符串/SSA 引用转编号，编号对应表仍有引用，
  不能用 `uintptr` 绕过 GC。
- map 池化可能保留桶与引用、延长存活并增加后续扫描；当前 `CODEINTEL_EMIT_SAMPLE`
  只给抽样外推（不是精确扫描字节），不足以支撑改造。
- **触发条件**：出现 GC CPU 占比回归到 >35%（当前约 26–27%）或扫描堆成为
  阶段瓶颈时，再按“短期高频容器 → 整数索引/紧凑缓冲区”做小范围 A/B，比较
  GC CPU/扫描堆/峰值 RSS/产物一致性。

### SCIP 重复加载评估（保留，不删除）

- `scip-go index` 支持 `<package-patterns>`（可限制到变更包），但无“跳过依赖”
  开关——内部仍 `go list -deps` 完整加载并独立类型检查一遍。
- 符号/引用覆盖是 SCIP 的权威来源，删除或替换会改变图语义；保留该步。
- **后续触发条件**：`update` 中 scip 全量扫描成为瓶颈时，按变更包传
  package-patterns（需验证符号/引用覆盖与内存压力）。

### 内容差异与覆盖限制

- 旧/新版同二进制对照产物逐行全等；跨版本（含分块缓存）此前记录的
  SSA 值名/dispatch_to.register_site/code_snippet 差异属既有同版本不确定性。
- 目标仓库存在部分编译/类型错误包，A/B 以相同口径运行，未声称覆盖全部调用关系。

## todo13-17 查询预算/阶段计数（2026-11，固定中立大仓）

**固定夹具**（脚本化在 `integration/todo13_perf_test.go`）：单 module
`example.com/largeperf`，1 个文件；3 层调用链（`(Handler).Entry → Mid → Leaf`）
+ 800 个无关类型（各 2 个方法，Cache 字段）；索引规模 **9018 节点 / 9618 边**
（构建 590ms，SCIP/SSA 各约 90ms）。查询配置：默认 `--min-conf 1.0`、
`CODEINTEL_SEQ_TIMING=1`，`--depth 3`；本机口径，不外推其他仓库。

| 查询 | 条件 | 样本 | 结果 |
|---|---|---|---|
| `query sequence ... --code --depth 3 --timeout 2s` | 冷/暖（同一索引） | 3 次 | 16.0ms / 13.5ms / 15.9ms，全部 complete |
| `value-trace <write> --max-depth 8` | 冷（Actions 新实例） | 1 次 | 7 行；SQL 173.6ms、符号标注 14.3ms、miss=1 |
| `value-trace <write>` | 暖（同 Actions） | 1 次 | 7 行；cache hit=1、miss=0（0 SQL 遍历） |

**阶段计数可见**：`sequence` 输出 `meta.timing`（`sym_lookups/sym_ms`、
`sql_queries/sql_ms`、`signature_lookups/signature_ms`、`dispatch_lookups/
dispatch_ms`、`cache_hits/cache_misses`）与 stderr `[seq.stage]` 行；
`value-trace` 输出 `analysis.sql_ms/sql_queries/symbol_ms/symbol_lookups/
cache_hits/cache_misses`。源码解析按文件缓存（同文件多函数展开命中，
不再重复读盘/parser.ParseFile）。

**预算语义**：`sequence` 与 `value-trace` 都接受 `--timeout`；`1us` 预算
在声明停止宽限内非零退出并说明“超过查询预算/已取消”，不输出完整成功；
SQL 遍历使用 `QueryContext`（递归 CTE 可真正中止）。value-trace 结果缓存键
含索引 `build_id`——同 SHA 重建后旧正/负缓存不再命中（单测覆盖）。

**未声称**：未测并发查询分布、未测不同 GOGC/内存上限；样本为单文件
800 类型夹具，不代表大型多 module 仓库的最坏情形。既有 `sequence --out`
渲染耗时不在本表（与查询逻辑分离）。

## todo14-17 查询矩阵/并发/资源（2026-11，200 无关类型夹具）

**夹具**：`integration/todo14_perf_test.go`；module `example.com/matrix`，
3 层链 + 未知回调 + 接口无实现 + 200 个无关类型。固定配置：默认
`--min-conf 1.0`、`CODEINTEL_SEQ_TIMING=1`；本机口径。

| 组 | 样本 | 实测 |
|---|---|---|
| sequence depth=1 | 1 | 4ms；sym=6/sql=8/sig=4/cache miss=1 |
| sequence depth=2 | 1 | 6ms；sym=11/sql=17/sig=5/cache hit=4 |
| sequence depth=3 + focus=return | 1 | 5ms；sym=12/sql=19/sig=5 |
| sequence depth=3 + focus=State | 1 | 6ms；nodes=8 |
| 并发（4 goroutine × 2 轮，独立连接） | 8 | p50=9ms / max=10ms |
| value-trace 少结果 | 1 | 19ms（含条件/派发） |
| 停止延迟（1µs） | 各 1 | sequence≈2.3ms、value-trace≈1.3ms（<2s 宽限） |
| 峰值 RSS（VmHWM） | — | before 40MB → after 41MB |

**统计口径**：sequence `sql_queries` 是 action 层显式仓储调用
（GetLatest/ResolveSymbol/GetSymbol/Callees/InterfaceMethodImpl+
GetImplementsEdges），签名解析内部的 ResolveSymbol 计在 sym/sig；
adapter 内部 SQL 与源码读取不混入（`stats_scope`/`uncovered_ops` 明示）。
value-trace `sql_queries` = GetLatest + CTE，符号查询/条件行/派发分别计数；
缓存上限 128（FIFO），`cache_entries/cache_evictions` 可观察。

**未声称**：未做关闭缓存的 before/after 耗时 A/B；未测更大 module 图/不同
GOGC；并发样本为进程内独立连接，不代表 serve 长生命周期下的内存曲线。

## todo15-17 缓存 A/B 与大仓矩阵/资源（2026-11）

**A/B（同源码/索引/配置/负载）**：`TestValueTraceCacheAB`，单节点字段访问
固定索引，n=6 次同请求：
cache-off wall **7.2ms** / sql **12** / sym **6** / miss 6；
cache-on wall **1.4ms** / sql **7** / sym **1** / hit 5；结果行数一致。
（cache-on 仍每次 GetLatest 1 次 SQL，故 sql=2+5。）

**大仓矩阵**：`integration/todo15_perf_test.go`，fixture **2212 节点/1706 边**
（300 无关类型 + 接口实现 + 未知回调 + Many 120 次读写）。配置默认
min-conf=1.0、CODEINTEL_SEQ_TIMING=1。

| 组 | 实测 |
|---|---|
| sequence depth1 vs depth2（Use） | 覆盖节点数增加；接口 Impl.Do 展开；CB unresolved |
| value-trace unknown（cold） | rows=3、sql=2、sym=2、cond=2、dispatch=1 |
| value-trace unknown（warm） | cache hit=1 |
| value-trace simple | rows=3 |
| 并发 value-trace 4×2（独立连接） | p50=35ms / max=40ms；行数一致 |
| 目标查询峰值 | Linux GNU `time -v`：Maximum RSS=27MB（查询进程，不含测试进程）；macOS/BSD 探测 `time -l`，不支持则明确 unavailable |
| 工作开始后取消 | 5ms 预算：delay≈7ms，`sql_queries=2`（入口拒绝不算） |

**口径**：`sql_queries` 为 action 层显式仓储调用；符号/条件/派发分别计数；
adapter 内部 SQL 与渲染不计入（`stats_scope`/`uncovered_ops` 明示）。
RSS 采样器能力先探测：GNU `time -v` → BSD `time -l` → 都不支持则
`RSS sampling unavailable`（平台/原因/单位/进程范围写日志），不把 0 当成功；
查询自身失败单独报错，不吞成 unavailable。取消验收已与 RSS 拆成独立测试。

## 2026-10-27 写库/SCIP 口径复测（P1-07…P1-13）

固定环境：go2o（127 包 / 12.1 万节点 / 13.7 万边）同机副本，GOGC=40、
workers=8、`--progress plain`。同一台 8 核 / 3GB 机器，单次数字仅作趋势。

| 口径 | 冷构建 | 暖构建（包缓存 + SCIP 缓存全命中） |
|---|---|---|
| wall | 6.25s（重复 6.28–6.85s） | 6.77s（噪声内） |
| Max RSS（`time -v`，含 scip-go） | 741MB | 772MB |
| 峰值 live（MEM_PROFILE） | 386MB | 363MB |
| TotalAlloc / mallocs / NumGC | 1337MB / 19.67M / 47 | 1242MB / 19.52M / 48 |
| `scip index` 阶段 | 2.53s | **47ms**（产物缓存命中） |

**本进程 CPU 采样分布（冷构建，`CODEINTEL_CPU_PROFILE`）**：GC 34.6%
（scanSpan 24.7%）、SQLite 写库 26.6%（`SaveBatchStats`；`_sqlite3VdbeExec`
21.9%）、SSA 发射 8.4%、AST 6%。`/usr/bin/time` 的 380% CPU 与采样差额是
scip-go / go list 等子进程（不在本进程 profile 内）。

**写库（flush）**：新增 p50/p95/max 观测（`flushLatency`，最近秩法）。
go2o 冷构建 10 批：**p50 460ms / p95 704ms / max 704ms**——p95/p50≈1.5，
无长尾；每批 2 万节点或边 ≈ 20µs/行。分配归因（`BenchmarkSaveBatchInsert`，
2000 节点 + 6000 边，6.0MB/op / 172k allocs/op）：`database/sql.
driverArgsConnLocked` 36%、`resultFromStatement` 50%。**复用 `[]any`
参数切片实测 allocs/op 172266→172267（零收益）已回滚**。

**SCIP**：
- 增量构建按变更包传 patterns（P1-12）：`scip index` 1.18s vs 全量 2.80s，
  update 产物（节点/边/摘要/来源）逐项 diff = 0。
- 全量构建按内容指纹缓存产物（P1-08）：暖构建 `scip index` 2.53s → 47ms；
  `scripts/detcheck.sh` 冷1/冷2/暖 四类产物全等（缓存重放保真）。

**DB 体量（`scripts/dbdiag.sh --size`）**：删掉摘要表两个最左前缀冗余索引后
114.3MB → **111.4MB（−2.5%）**；剩余大头仍是 `nodes`+索引（约 72%）与
`idx_nodes_func_id`（9.9MB，8.7%）——后者的替代路径（id 前缀范围扫）与
summary 表整数化见 `field_trace.md` §107 的判据/触发条件。

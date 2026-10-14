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

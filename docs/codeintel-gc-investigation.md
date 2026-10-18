# 构建期 GC / 分配治理调查记录（2026-10-16）

来源：`codeintel-changelog4.txt`。该 changelog 的「已完成待同步」改动源自另一套
环境（其大仓副本约 240 万节点 / 742 万边），本仓库当时并不包含这些代码。
本文件记录**在本仓库重新实现**的部分、本机局部基准、以及仍未完成的验收项。

> 环境（本机基线）：Go 1.27.0 linux/amd64，8 vCPU，索引目标为自举仓库与
> go2o（约 11.8 万节点 / 13.6 万边）。**changelog 里的绝对数字（17× /
> 6.4× / 26.8×）来自另一台机器，不能直接搬用**；下表为本机实测。

## 1. 已实现

| 项 | 文件 | 语义要点 |
|---|---|---|
| 字符串缓存 | `ssa/string_cache.go`、`ssa/func_snapshot.go` | 构建级共享 `types.Type.String()` 与 `candidateKey`（接收者类型+方法名）；并发首次访问加锁，命中零分配；快照缺失（`emitGlobalInit` 轻量提取器）回退纯函数，不静默返回空串 |
| 调试日志级别检查 | `ssa/cf_call.go` | `logger.Check(zap.DebugLevel, ...)`——Debug 关闭时不构造 `cc.String()` / `cc.Value.Type().String()`；开启时日志内容逐字不变 |
| 等价节点写入消除 | `orchestrator/node_batch.go` | 同批次内只跳过与**同 ID 上一次贡献**内容完全相同的节点写；保留 A→B→A 的多次贡献与 json_patch 合并语义；不对边去重，不改变 count |
| 构建期端点正向缓存 | `sqlite/build_write_cache.go` | canonical ID → `nodes.id_int`；32768 上限；**只缓存正向结果、不缓存缺失端点**；**事务提交成功后才发布**；`EndBuildWriteCache` 清空（不跨构建复用）；输入/写入/命中/清表统计 |
| CPU profile 收尾修复 | `cmd/codeintel/cpu_profile.go` | 启动时打开的文件句柄由采样状态持有，停止时 flush 后关闭同一句柄（旧实现再次 `os.Create` 截断文件）；启动失败 no-op，不阻塞构建 |
| 匿名嵌套字段类型路径 | `ssa/fe_nested.go`（含 `fe_nodes.go`/`fe_value.go`/`fe_elements.go`） | 沿 SSA 字段访问/解引用/索引/切片/单一赋值副本向外找最近具名类型，拼出**不依赖变量名**的 `full_path`；独立匿名结构体仍回退 `instance_path` |

`fe_nested.go` 覆盖：指针读写、值读取、单一赋值副本、不同所属类型不混淆、
容器元素（切片/数组索引与 range 遍历，标记 `[*]`）、子切片、两层匿名元素。

## 2. 本机局部基准（go test -bench）

| 基准 | 结果 | 说明 |
|---|---:|---|
| `BenchmarkDispatchKeyDirect` | 631 ns/op，888 B/op，14 allocs | 直接 `iface.String()+candidateKey` |
| `BenchmarkDispatchKeyCached` | 49 ns/op，48 B/op，1 alloc | 构建级缓存命中（约 12.9×；剩 1 次分配来自基准里的字符串拼接） |
| `BenchmarkEndpointResolveUncached` | 1,003,490 ns/op，146 KB/op | 每批直接查库 |
| `BenchmarkEndpointResolveCached` | 41,649 ns/op，74 KB/op | 正缓存命中（约 24×；P1 合并容器后由 58.5µs/94KB 降至） |
| `BenchmarkLogDynInterfaceDispatchDisabled` | 见 `ssa/cf_call_debug_test.go` | Debug 关闭路径只有一次 level 检查 |
| `BenchmarkNodeDedupDuplicate` / `Unique` | 见 `orchestrator/node_batch_test.go` | 重复/唯一节点场景对照 |

> 这些是**局部微基准**（不含全量构建、GC、写盘），不能外推为大仓端到端收益。

## 3. 验证

- `go build ./...` / `go vet ./...` 通过。
- 相关包单测：`ssa`、`sqlite`、`orchestrator`、`cmd/codeintel`、`cli` 全绿。
- `scripts/verify.sh --quick` 全绿（pre-commit hook 增量验证亦通过）。
- 新增回归：`string_cache_test.go`（一致性/并发/泛型实例化/构建隔离/命中零分配）、
  `build_write_cache_test.go`（命中/缺失不缓存/回滚不发布/重建隔离/并发）、
  `node_batch_test.go`（A→B→A 保留、内容差异、不同 ID）、
  `fe_anonymous_path_test.go`（上述匿名路径矩阵）、
  `cpu_profile_test.go`（gzip 头 + `go tool pprof -raw` 可解码）。

## 4. P1/P2 追加（本机）

### 4.1 识别到的"反复构建而不用缓存"（本轮新增缓存）

| 数据 | 原先的重复形态 | 缓存 | 本机基准 |
|---|---|---|---|
| 调用点源码片段 `cc.String()` | `emitEntityFields` 对**每个导出字段**、`emitWhereFilterTyped` 对**每个 where 列**各渲染一次（SSA 值打印）；同一调用点数十次 | `funcSnapshot.callSnippet`（key=`*ssa.CallCommon`） | — |
| 实参嵌套 owner 类型路径 | `recordCallInfo` 对**每个调用点的每个实参**递归展开（纯类型函数） | `funcSnapshot.ownerTypesOfCached`（key=`types.Type`） | 150ns/112B/5allocs → 11ns/0/0 |
| 摘要键 `summaryKey(fn)` | `applySummary` 对**每个调用点**重新拼路径 | `funcSnapshot.summaryKeyCached`（key=`*ssa.Function`） | — |
| 签名节点类型串 | `emitSignatureNodes` 对每个接收者/参数/返回值直接 `Type().String()`，绕过已有构建级类型串缓存 | 插入 `funcSnapshot.typeString` | — |

仍**未**缓存（有意保守，待大仓 profile 确认收益/风险）：`instancePath` /
`recoverVarName` / `allocTypeShort`（每次访问）、`nestedTypePath` 的
`singleStoredValue`（每次字段访问扫描函数块）、`snakeCase`（每字段）。

### 4.2 端点缓存

- **P1 端点解析临时容器**：`resolveNodeRefsCached` 改为直接消费 `edges`
  （不再建 `edgeEndpointIDs` 大切片），单遍去重后 `partition` 把命中直接写
  入结果 map（去掉额外 hits map），未命中已去重 → 走 `resolveNodeRefsUnique`
  不再二次去重。基准：58.5µs/94KB/19allocs → 41.6µs/74KB/15allocs。
- **P2 有界端点缓存淘汰**：整表清空 → 两代分段轮换（`cur` 达 capacity/2
  时 `prev←cur`、`cur` 清空；prev 命中且有空间时晋升）。幂律序列对照
  （capacity=32768，20 万次访问，3 个 seed）：整表清空命中率 ≈8.7–8.8%，
  分段轮换 ≈12.1–12.2%（`TestEvictionHitRateSegmentedBeatsClear` 作护栏）。
  **真实大仓端点序列的命中率/CPU/存活内存仍需在大仓实测**（本机无该仓库）。
- **P1 `emitValue` 分配**、**P2 包缓存 gob 编码分配**：尚未实施（需先按
  changelog 要求测属性 map/装箱/重复字符串与缓冲区上限），见下节。

### P2 包缓存编码评估（本机，`BenchmarkPkgCacheSerialization`）

| 路径 | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| chunked-encode（生产） | 22.3M | 19.9M | 179871 |
| gob-encode（整包对照） | 27.9M | 52.8M | 179689 |
| chunked-decode | 40.3M | 42.3M | 755938 |
| gob-decode（对照） | 36.5M | 40.7M | 754956 |
| json-encode（对照） | 34.4M | 31.6M | 253425 |

结论：分块 gob（v4）已比整包 gob 少 **~62%** 编码分配（历史结论），剩余
分配主要来自 gob 反射/interface 编码，**不换文件协议、不加无界缓冲**即可
不动。缓冲区复用（bytes.Buffer/encoder 池）因 gob 流类型状态不能跨文件共享，
收益不明确且可能推高峰值。

## 5. 未完成（沿用 changelog4 的验收要求）

- **P0**：严格比较里不属于匿名字段修复的内容差异（40 个节点名称、97 条
  dispatch_to 的 `register_site`、3 条间接写 `code_snippet`）——需要大仓副本与
  固定工具链/并行度做双冷构建，本机无该仓库。
- **P1**：`emitValue` 分配（峰值累计约 4 GiB）——先测属性 map、装箱、重复
  字符串贡献，再考虑类型化临时数据/减少重复属性物化。
- **P2**：包缓存 gob 编码临时分配 —— **本轮已评估，结论不动**（分块 gob
  已比整包少 ~62% 编码分配；剩余为反射开销，换协议/无界缓冲不采纳，见上）。
- **测量条件统一**：固定 GOROOT/工具链/GOMAXPROCS/workers/源码输入，记录
  TotalAlloc / NumGC / 累计 GC CPU，区分峰值堆采样累计值与全程累计值。
- 大仓冷/暖保真与 chaincheck 历史基线：需大仓副本，本机 go2o 可作较小口径
  的对照（`scripts/detcheck.sh`、`scripts/chaincheck.sh`）。

## 6. 统一测量条件模板（后续所有构建期 A/B 对照）

固定下列输入，缺一项不得把差异归因于代码：

- **工具链**：`ASDF_GOLANG_VERSION` / `GOROOT` / `PATH`（含期望的 scip-go）与
  `GOTOOLCHAIN`（建议 `local` 防自动下载）；记录 `go version`。
- **并行度**：`GOMAXPROCS`、`--workers`（两者都会改 GC/内存曲线）。
- **源码输入**：固定 commit/工作区指纹；排除目录一致；包缓存状态一致
  （冷：删 `.codeintel/cache`；暖：保留）且**必须先跑一次冷构建再跑暖**。
- **诊断开关**：`CODEINTEL_GOGC`、`CODEINTEL_MEMLIMIT`、`CODEINTEL_CPU_PROFILE`、
  `CODEINTEL_MEM_PROFILE`、`CODEINTEL_EMIT_SAMPLE` 显式记录；profile 会引入
  诊断开销，须与无诊断基线分开记录。
- **记录口径**：
  - 阶段耗时（emitFunction / finishIndex / flush）与 wall
  - 峰值 **RSS**（`/usr/bin/time -v` 的 Max RSS 含 scip-go 子进程；进程内用
    `VmHWM`），与 `CODEINTEL_MEM_PROFILE` 的峰值 **live** 分开
  - 构建结束的 `runtime.MemStats`：`TotalAlloc`、`NumGC`、`PauseTotalNs`
    与累计 GC CPU（`runtime/metrics` 的 `/cpu/classes/gc/total:cpu-seconds`）
  - **区分** 峰值堆采样的累计值与全程累计值（`TotalAlloc` 不是存活量）；记录
    诊断时的强制 GC 次数与耗时
- **产物对照**：`scripts/detcheck.sh <repo> [workers]`（冷1/冷2/暖3 四类产物
  逐行 + `scripts/chaincheck.sh` 链完整性）；比较前先固定 workers 与缓存状态。
- **判定**：时间/内存/分配同时看；只有单次数字或不同环境/并行度的对照，
  一律视为待验收，不写成收益结论（build_perf.md 的既有纪律）。

## 7. remote-todo R13/R14 评估（本机，结论：需大仓 profile 再动）

### R13 内存与 GC（调用记录保留 / 字段产物物化 / 暖缓存字符串）

现状定位（本机代码审计）：

- `funcData.calls`（`crossflow.go`）保留全部静态调用点 + `argStructPaths`/`argNames`；
  间接写传播（`summary.go` 的 `callers` 反向索引 + fixpoint）**需要**它们，
  且某被调函数的写可能在后续轮次才被发现 → 不能在扫描期就丢。
- 可安全减少的是 **fixpoint 结束后**仍无任何匹配写的调用记录：需先算
  `calleeHasWrites` 集合再裁剪，属中等风险（缓存内容会变，需冷暖保真对照）。
- `idents`（token.Pos→名）per-function，随 `emitFunction` 作用域释放；未见长期悬挂。
- 暖缓存：gob 解码为每条目新建字符串（`pkg_cache_*`）——**构建级字符串共享**
  （string_cache.go 已有 `typeStrs`）可覆盖解码后的类型串，但“字符串共享 ≠ 减少
  GC 扫描”；减少扫描需改为无指针紧凑记录。两者不可混为一谈。
- 无大仓峰值 profile 无法量化收益 → **不盲改**；验收须按 §6 模板在大仓测
  GC CPU / 扫描堆 / 峰值 RSS，并保持调用边与摘要传播语义。

### R14 暖构建准备（提前识别包缓存命中）

- 方向：把别名分析（`computeAliases`）、全局初始化（`emitGlobalInit`）、
  派发分析（`emitDispatches`）真正需要的数据与**每函数 emit** 分离；对包缓存
  命中的包跳过类型图/SSA 准备工作，但**不能跳过仍被收尾阶段依赖的建图**。
- 风险：同一 SSA Program 内跨包调用/接口派发需要全程序函数集；跳过某包的
  SSA 构建会让 dispatch/alias 缺失 → 需要“按包产物 + 共享收尾数据”的重构，
  并用冷暖保真（detcheck `scripts/detcheck.sh`）验证。
- 缺大仓暖构建耗时/内存拆分 → 同 R13，先评估后动。
- **todo2-10 代码审计结论（2026-10-16）**：包缓存命中检测发生在
  `prog.Build()` **之后**（`adapter_index.go` 阶段 A 依赖 `byPkg` 的
  `*ssa.Function`）。`ssautil.Packages` 只创建函数对象，`prog.Build()`
  统一构建全部函数体；Go x/tools 没有“只构建部分包”的公开 API（
  `ssautil.BuildPackage` 会新建 Program）。因此“减少类型图/SSA 准备”
  要么改为按包独立 Program（会破坏跨包 `funcSnapshot`/dispatch/alias），
  要么两阶段构建（先判断命中集合再选择性 Build）——两者都需大仓
  暖构建数据与冷暖保真对照，**不盲改**。

## 8. remote-todo2（2026-10-16）执行结果

| # | 事项 | 结果 | 提交 |
|---|---|---|---|
| 1 | 超大图按需查询 + 规模判断复用 | 超阈值视图**不整表加载**，逐节点 SQL 查边（BFS 内 memo）；count/tooLarge 按 build 缓存；保留深度与有向语义 | `abe4796` |
| 2 | 包缓存分块：限制单块 + 减少编码临时容器 | FD 块 map 复用 + 平坦分块（协议不变、重放内容不变） | `ce8d9e1` |
| 3 | 匿名字段路径：副本只扫相关引用、按 Alloc 缓存 | `singleStoredValue` 改扫 `Alloc.Referrers()`（首次也不扫整个函数） | `0d83ad8` |
| 4 | 构建级字符串缓存：并发首次不重复计算 | 写锁内二次检查（RWMutex）；`countingType` 测试断言只渲染一次；命中零分配 | `f03d635` |
| 5 | 缓存与大图测试 | 大图按需 + 规模判断缓存失效、每块条数 ≤ chunkSize、流中途截断拒绝 | `abe4796` / `1934941` |
| 6 | 值身份：闭包/多 init 作用域、result 槽位、别名稳定 | 作用域后缀（`@closure:`/`@init:`）同时用于 `emitValue` 与 alias；闭包/多 init 回归 | `ba3770c` |
| 7 | 注册来源：补文件 + 完整来源、嵌入共享方法 | metadata 增 `register_file`；按 (file,line) 稳定取最小；嵌入共享方法只产一条边 | `ec18dfc` |
| 8 | 摘要来源与传播：完整源位置、候选覆盖、同行调用点 | `callPos` 进间接写键与缓存（格式 v5）；传播代表用 `earlierEntry` 全序；同行两调用点实参不混用 | `036def6` |
| 9 | 内存与 GC | 安全子项已做：不保留无结构体实参的调用记录（间接写永不匹配）；其余需大仓 profile | `ae2e56c` |
| 10 | 暖构建准备 | 评估（同 R14）——需大仓暖构建耗时/内存拆分，未盲改 | §7 |
| 11 | 堆采样：GC 时点统一、构建隔离、峰值/累计分开 | `peakSampler`（GC 后 live 峰值 + 阶段 pre-GC 快照）+ `<profile>.stats.json`（TotalAlloc/NumGC/Pause）；Index 开始 reset | `5bfbb54` |

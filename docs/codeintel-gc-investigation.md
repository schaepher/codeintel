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

## 9. remote-todo3（2026-10-16）执行结果

| # | 事项 | 结果 | 提交 |
|---|---|---|---|
| 1 | 大图规模判断改**有界计数** | 子查询 `LIMIT 阈值+1`（达阈值即停，不再全量 count）；结论仍按 build_id 缓存；新增有界计数 + 并发按需查询（-race）测试 | `e617a14` |
| 2 | 包缓存**超大输入块二次切分** | `encodeChunkSections` 对传入节点/边块再按 chunkSize 切（单条 gob 消息有界）；摘要容器容量上限 chunkSize；新增超大块往返测试 | `d4f8d53` |
| 3 | 匿名路径回溯深度 + 空引用保护 | `maxNestedDepth` 12→32；新增深层嵌套（Rows[*].Cells[*].Inner.Leafs[*]）、nil 值/引用保护测试 | `44d4d08` |
| 4 | 值身份：别名重复查询稳定 ID | `aliasPass.valueIDs` 按值缓存——同一值多次查询不再按槽位访问状态追加 `@行号`；变异验证（去掉缓存即 ID 漂移） | `a8a6c89` |
| 5 | 调用点缓存保真：恢复 `CallPos` | `fromCachedFD` 补 `callPos`（原先暖重放丢失同行调用点区分）；不同 CallPos 往返断言 | `b26d2ab` |
| 6 | 摘要重放预分配 | 按缓存已知条数预分配 calls/direct/indirect 切片，空输入保持 nil 语义；局部基准 2000 项 **5 allocs/op**（不宣称全量/GC 改善） | `b26d2ab` |

## 10. remote-todo4（2026-10-16）执行结果

| # | 事项 | 结果 | 提交 |
|---|---|---|---|
| 1a | 匿名路径以**最近具名类型**为起点 + 深度 64 | `nestedTypePath` 开头先判 `namedStructPath(v.Type())`（`o.In.A.B` → `Inner.A.B`，不再回溯到 `Outer.In.A.B`）；`maxNestedDepth` 32→64 | `464e3cc` |
| 1b | `singleStoredValue` 空引用直接返回 | `alloc.Referrers()==nil` 直接 return；不再要求 `ext.fn != nil`（轻量提取器可追溯）；新增 `fn==nil` 测试 | `464e3cc` |
| 1c | 最近具名 / 深层容器 / 来源歧义测试 | `TestAnonymousPathNearestNamedType`、`DeepNesting`、`Outer/Other` 不同来源、独立匿名回退 | `464e3cc` |
| 2 | 摘要分块预分配 `min(chunkSize, 实际条数)` | `encodeFDSections` 的 `capHint` 取 min；小摘要（<一块）往返内容不变 | `51cf1e0` |
| 3 | 回归补齐 | SQLite 批量 vs 逐条写入逐项一致；包缓存损坏头/多点截断拒绝；字符串缓存并发混用（-race）；AST 嵌套字段选择器链；CPU 采样坏启动后仍可正常启动 | `8324e00` |

## 11. remote-todo5（2026-10-16）执行结果

| # | 事项 | 结果 | 提交 |
|---|---|---|---|
| 1 | 逐文档解码 SCIP index | 新增 `scip/decode.go`：用 `protowire` 逐字段消费，documents(2) 单独 Unmarshal→处理→释放；metadata(1)/external_symbols(3) 仍完整解码校验；未知字段按 wire 校验跳过。截断/非法长度/非法嵌套/输出错误/ctx 取消全部返回失败。与原 `proto.Unmarshal` 逐文档 `proto.Equal` 一致，节点/边产物一致 | `853895a` |
| 2 | 动态类型查询去重 | `funcSnapshot` 增加**排序包快照** + `dynamicTypeOf` 缓存（成功/失败都缓存，RWMutex 写锁内二次检查）；按构建创建、随快照释放、不跨 Program。基准：建索引 13.0ms/851 allocs（一次），首次查询 726ns/8 allocs，命中 36ns/0 allocs | `38e3ffd` |
| 3 | Index 结束释放瞬态引用 | `Adapter.releaseTransient`（funcs/lines/typeMapping/dispatchRegs/regHits）由 `Index` defer 调用（成功/错误/取消）；`DispatchPkgs` 保留；worker emit 错误与 `ctx` 取消现在会传播（原先只打 stderr）。新增成功/错误/复用测试 | `8ff1790` |
| 4a | 数据库健康检查修正 | `scripts/dbdiag.py` 的 `--plans/--size` 适配 Q254c 整数代理键（`source_ref/target_ref` + `edges_v`），内存副本按当前 schema 建表/视图/样本行；`--self-test` 通过 | 本文件同提交 |
| 4b | 验收（见下） | detcheck 五类全等；chaincheck 328/500/408 通过；A/B 与旧版内容零差异 | — |

**todo2-6 补修**（验收中发现）：`valueScopeTag` 原用 `HasPrefix("init")` 把
`initPages/initConfig/(T).init` 等普通函数误判为包级 init → 加作用域后缀 →
与 alias 路径标识分裂，go2o **alias 连通 318（基线 328）**。改为“`types.Func`
名为 init 且无接收者”后恢复 **328**，并补回归（非包级 init 不得带后缀）。

### todo5-4 验收记录（固定环境：GOGC=40 / GOMAXPROCS=8 / workers=1 / 同源码副本）

- `scripts/verify.sh --quick` 全绿；`go test -race ./...` 13 包全绿；`integration` 通过。
- **detcheck**：fixtureapp 与 go2o 副本（冷1/冷2/暖3）均为 节点/边(含来源/置信度/元数据)/摘要/摘要来源 **五类全等**；go2o 节点 118574 / 边 136510 / 摘要 21158 / 来源 3445。
- **内容隔离（todo5 本身零差异）**：构建 “`74e70f6` + init 修正” 对照二进制 vs 当前 HEAD，逐项比较 ids/nodes/edges(含 metadata)/summary/origins —— **差异全 0**（118574/136510/21158/3445）。
- **chaincheck**：一跳 500/500、多跳 408/408、alias 连通 328（= 基线），通过。
- **dbdiag**：健康检查 0 异常；`--plans`（真实库 + 内存副本）全部走预期索引（出边 `sqlite_autoindex_edges_1`、入边 `idx_edges_target_kind`、kind `idx_edges_kind`、id `sqlite_autoindex_nodes_1`）。
- **A/B（旧 `74e70f6` vs 新，交错 2 轮）**：wall 7.12/7.11s vs 7.11/7.17s（持平）；Max RSS 779/817MB vs **735/768MB（约 -5~6%）**；SSA 峰值 live 348MB（两侧相同）；TotalAlloc 1586/1592MB vs **1570/1580MB（约 -0.7~1.0%）**；mallocs 22.37/22.54M vs 22.29/22.41M；NumGC 54/52 vs 53/51。单机单次对照，仅作趋势证据，不宣称稳定全量收益。

## 12. remote-todo6（2026-10-16）执行结果

| # | 事项 | 结果 | 提交 |
|---|---|---|---|
| 1 | 动态类型首次查询按包路径索引 | 排序包快照上建 `pkgsByPath`/`pkgsByName` 索引；首次查询先用类型串限定符收窄候选包，再按原匹配规则（`Scope().Lookup(name)` + `Type().String()` 全等、指针 `NewPointer`）解析；收窄未命中**回退全量扫描**（保证别名等跨包语义等价）；成功/失败结果仍缓存。基准：快照/索引构建 12.5ms（一次）、首次查询 626ns/14allocs、命中 21.8ns/**0 allocs** | `4011f8e` |
| 2 | 模块函数过滤复用 | `moduleFuncs` 按**模块集合**缓存只读切片（写锁内二次检查，并发首次只过滤一次）；单 module 快路径零分配；随 funcSnapshot 释放。5 个消费者（快照初始化/别名/全局 init/派发×2）复用。基准：首次 1.13µs/3allocs、命中 **9.9ns/0 allocs** | `4011f8e` |
| 3 | 回归验收 | 见下 | — |

**关键事实（todo6-1 设计依据）**：Go 1.26 的 `types.Type.String()` 对具名类型的限定符是**包路径**（`example.com/m/a.T`），不是包名——与旧代码注释假设不同。故索引以 **path** 为主；同时保留 name 收窄（兼容自定义 Qualifier/旧版本）；两者都未命中再回退全量扫描。

### todo6-3 验收（固定环境：GOGC=40 / GOMAXPROCS=8 / workers=1 / 同源码副本）

- `verify.sh --quick` 全绿；`go test -race ./...` **13 包全绿**；`integration` 通过。
- **detcheck**（go2o 副本，冷1/冷2/暖3）：节点/边(含来源/置信度/元数据)/摘要/摘要来源 **五类全等**（118574 / 136510 / 21158 / 3445）。
- **内容隔离（todo6 零差异）**：旧 `416c425` vs 当前 HEAD，两轮 DB 逐项比较 ids/nodes/edges(含 metadata)/summary/origins —— **diff 全 0**。
- **chaincheck**：一跳 500/500、多跳 408/408、alias 连通 328（= 基线），通过。
- **dbdiag**：健康检查 0 异常。
- **A/B（旧 `416c425` vs 新，交错 2 轮）**：wall 7.18/7.05s vs 7.15/7.11s（持平）；Max RSS 734/796MB vs 785/771MB（噪声内，无明确变化）；SSA 峰值 live 347/348MB vs 345/346MB；TotalAlloc 1571/1582MB vs 1578/1578MB；NumGC 51/51 vs 52/54。**结论**：todo6 为内容保持的微优化，局部基准明确（模块过滤首次 1.7µs→命中 10ns；动态类型首次 726→626ns、命中 22ns），但 go2o 端到端在噪声内，不宣称全量收益。

## 13. remote-todo7（2026-10-16）执行结果

| # | 事项 | 结果 | 提交 |
|---|---|---|---|
| 1 | 复用 init 阶段派发注册结果 | `emitDispatches` 改为接收 `regs`/`regPkgs`（由 `initFuncSnapshotDispatch` 收集），删除收尾阶段重复的 `collectDispatchRegistrations`。**nil = 未初始化 → 回退扫描一次；非 nil 空 map = 已初始化空注册表 → 不得再扫描**。注册包与动态调用包继续合并为完整 `DispatchPkgs`（只有注册时保留注册包）。全部注册位置/候选选择顺序/来源/置信度/边元数据不变；`releaseTransient` 在成功/错误/取消后释放瞬态引用 | `0f87c6f` |
| 2 | 回归验收 | 见下 | — |

**todo7-1 回归**：新增 `dispatch_reuse_test.go`——复用路径 vs 重新扫描的**派发边集合（含 metadata）与 DispatchPkgs 完全一致**（含多文件同行注册、多模块列表）；已初始化空注册表**不产生 register 边**（证明未重扫，enum 兜底边保留）；只有注册时保留注册包；输出错误传播；适配器连续两次 Index 的节点/边/DispatchPkgs 一致。

### todo7-2 验收（GOGC=40 / GOMAXPROCS=8 / workers=1 / 同源码副本）

- `verify.sh --quick` 全绿；`go test -race ./...` **13 包全绿**；`integration` 通过。
- **detcheck**（go2o，冷1/冷2/暖3）：五类产物 **全等**（118574 / 136510 / 21158 / 3445）。
- **内容隔离**：旧 `fc4a621` vs 当前 HEAD，两轮 DB 逐项比较 ids/nodes/edges(含 metadata)/summary/origins → **diff 全 0**。
- **chaincheck**：一跳 500/500、多跳 408/408、alias 连通 328（= 基线），通过。
- **dbdiag**：健康检查 0 异常。
- **A/B（旧 vs 新，交错 2 轮）**：wall 7.03/7.19s vs 7.07/7.20s（持平）；Max RSS 759.5/758.9MB vs 769.8/769.3MB（约 +1.3%，小样本/噪声级）；峰值 live 345/349MB vs 347/346MB；TotalAlloc 1568/1574MB vs 1574/1575MB；NumGC 54/50 vs 53/53。**结论**：复用消除了收尾阶段的一次全程序注册扫描（逻辑重复），但该扫描在 go2o 上占比很小，端到端无可测收益；产物与 DispatchPkgs 逐项一致。

## 14. remote-todo8（2026-10-16）执行结果

按 todo8 的 14 项逐项实现（第 16 节为方法论，不新增代码任务）：

| # | 事项 | 结果 | 提交 |
|---|---|---|---|
| 1 | 注册位置去重平方级消除 | 新增 `dispatchDedup`：<32 线性；≥32 为分组建 `(文件整数编号, 行号)` 集合（值类型、无指针/哈希），首次出现顺序追加；集合/编号表按需创建（稀疏零额外分配）。基准（256 位置）：线性 200.9ns → 集合 **34.3ns**；稀疏 22.8ns；均 0 alloc | `394d5cc` |
| 2 | 排序键只算一次 | `sortedValues` 预计算 `valueOrderKey` 后排序（比较器只读键），输入切片不变；`sortedValueKeys` 新建切片原地排序不再二次复制 | `a08848e` |
| 3 | 类型字符串缓存复用 | `collectDispatchRegistrations` 的 `mi.X.Type().String()` 走 `funcSnapshot.typeString`；`aliasSlot` 的匿名 Alloc 短名路径经 `aliasSlotWith` 复用 extractor/aliasPass 的 `typeString`（单一槽位命名规则不变） | `394d5cc` |
| 4 | 候选键缓存复用 | `buildRegHits` / `emitDispatches` 的 `candidateKey` 改走 `funcSnapshot.candidateKeyOf`（纯函数兼容路径保留；无跨构建全局缓存） | `394d5cc` |
| 5 | 值/指针方法集复用 | `funcSnapshot.methodSetOf` 构建级缓存 + `findMethodCached`（先值后指针、`Lookup(nil,name)`，不直接用 `Named.Method`）；`implTypePool.SetFuncSnapshot` 共享 | `394d5cc` |
| 6 | 代表注册位置只归一次 | `emitDispatches` 按 (接口, 动态类型) 预计算最佳位置（relPath + (file,line) 最小），同 dyn 的不同方法复用；完整 `dispatchReg` 列表保留 | `394d5cc` |
| 7 | `ssaOp` 去格式化 | `reflect.TypeOf` + `Elem().Name()`；带类型 nil 指针/无类型 nil 与旧 `%T` 逐字一致 | `a08848e` |
| 8 | 包缓存准备按路径查找 | `indexPkgsByPath` 一次建索引（重复路径保留原切片第一个），删除每包内层线性查找 | `a08848e` |
| 9 | 摘要排序复用 | `emitSummaries` 的 `sortedFuncIDs` 一次，反向索引/传播种子/最终发射复用 | `b5426af` |
| 10 | 摘要写条目不物化临时切片 | 删除 `calleeWrites`；`calleeHasMatchingWrite` 与来源收集直接遍历直接写切片 + 间接写映射（直接写优先、提前退出、来源完整） | `b5426af` |
| 11 | 实参名只拼一次 | 传播循环对同一 `indirectSite` 在首个成功传播字段处 `strings.Join` 一次，后续字段复用；无传播不拼接 | `b5426af` |
| 12 | 行缓存并发首次合并 | `lineCache` 每文件 `entry.ready` 单飞（同文件只读一次；不同文件并行；读盘不在全局锁内；错误负缓存不变）；可注入 `readFile` 做可控并发测试 | `b5426af` |
| 13 | 实现类型池并发首次合并 | `implTypePool` 的 `cached`/`methods` 改为 entry+ready 单飞（同键只计算/过滤一次；不同接口并行；两层缓存不持锁互相等待，避免死锁） | `b5426af` |
| 14 | emitValue 实例路径局部复用 | 解引用分支 `instancePath` 只算一次，两个判断分支与命名共用 | `a08848e` |

**方法论落地（todo8 §16）**：先核对现状（非旧提交调用点）→ 用 codeintel/调用关系确认消费者 → 按频率×工作量定位；对“比较器/稳定集合/循环不变量/查找结构/临时物化/并发首次/容器生命周期”逐类排查。负面/边界结论：64 位置时集合去重相对线性无明显优势（哈希开销抵消），256 位置才显著（5.9×）——基准按密度分档避免把“小样本无收益”写成结论；todo8 §16.7 明确否决的两项（字段用途递归 map 缓存、源码行偏移索引）**不作为实现任务**。

### todo8-15 验收（GOGC=40 / GOMAXPROCS=8 / workers=1 / 同源码副本）

- `verify.sh --quick` 全绿；`go test -race ./...` **13 包全绿**；`integration` 通过。
- **detcheck**（go2o，冷1/冷2/暖3）：五类产物 **全等**（118574 / 136510 / 21158 / 3445）。
- **内容隔离**：旧 `8c84844` vs HEAD，两轮 DB 逐项比较 ids/nodes/edges(含 metadata)/summary/origins → **diff 全 0**。
- **chaincheck**：一跳 500/500、多跳 408/408、alias 连通 328（= 基线），通过。
- **dbdiag**：健康检查 0 异常。
- **稀疏/密集注册基准**：`BenchmarkCollectDispatchSparse` 612ns/786B/8allocs；`BenchmarkCollectDispatchDense`（40 位置）7.66µs/6729B/23allocs（增量来自更多注册位置本身；去重集合不产生逐次分配）。
- **A/B（旧 vs 新，交错 2 轮）**：wall 7.95/7.52s vs 7.75/7.49s；Max RSS 774/732MB vs 740/769MB（噪声内）；峰值 live 347/347 vs 349/349MB；**TotalAlloc 1579/1565MB → 1449/1449MB（约 -8%）**；mallocs 22.47/22.21M → 20.25/20.26M（约 -9%）；NumGC 48/52 → 50/51。分配下降明确且两轮一致；wall/RSS 在噪声内，不宣称提速。


## 15. 性能点续查（2026-10-19，todo9）

按 §14 的方法（现状核对 → 调用关系/消费者 → 频率×工作量 → 峰值 profile 定位）继续挖，落 7 项（均先写等价测试再改）：

| # | 事项 | 结果 | 提交 |
|---|---|---|---|
| 1 | 包缓存哈希整文件读入 | `hashFile` 改 32KB 分块 + `sync.Pool`（并包一层隐藏 `*os.File` 的 `WriteTo`，否则缓冲被忽略）；hash 逐字节等价（含空文件/顺序敏感）。局部基准 40×64KB：**2.97MB→10.7KB alloc/op（-99.6%）**，2.01ms→1.33ms | `dfda5ea` |
| 2 | AST `fnID` 重复生成 canonical ID | `Adapter.fnIDOf` 构建级 `sync.Map`（Index 入口重置，不跨构建持有旧 Program 对象）；纯函数 `fnID` 保留为未命中路径。`canonicalizer.GoSymbolID` 摊还 11.5MB→8.5MB，fnID 路径 8.5MB→~1MB | `f904ef8` |
| 3 | SCIP index 整份 `os.ReadFile` | 新增 `forEachIndexFileDocument`：`bufio` + `protowire` 逐字段消费（文档仍逐文档 Unmarshal；metadata/external_symbols 仍完整解码校验；未知字段按 wire 类型跳过、group 校验 end-group）。文件版与切片版逐文档 `proto.Equal`，截断/非法长度/非法消息/取消/大文档各有测试 | `9f52103` |
| 4 | 端点解析每批多建同规模 seen map | `resolveNodeRefsCached` 用 `refs` 兼作 seen（哨兵 0，rowid 从 1 起），miss 后删除哨兵；`resolveNodeRefsCached` 摊还 32.0MB→20.7MB | `20b82ca` |
| 5 | `mergeFuncData` 首次合并 append 增长复制 | 新 owner 按来源切片长度预分配；语义（顺序/空贡献不建条目）测试锁定 | `94fc7a9` |
| 6 | `emitValue` 的 UnOp 分支与通用分支各算一次 `instancePath` | 同一值的路径只算一次（局部变量复用）；解引用加载的命名/ID 不变（既有命名回归覆盖） | `0b9989b` |
| 7 | 批次对象每批重建（4 个切片 + 去重表增长） | `sync.Pool` 复用批次（flush 后归还；切片 `[:0]` 保留容量 + `clear` 去重表）；批次语义测试（复用后切片空、去重表清空、A→B→A 不变） | `f953d85` |

### 验证（GOGC=40 / GOMAXPROCS=8 / workers=1 / 同源码副本）

- 内容隔离：`4af284d`（todo8 终）vs 本批 HEAD，go2o 两轮 DB 逐项比较 ids/nodes/edges(含 metadata)/summary/origins → **diff 全 0**（118574 / 136510 / 21158 / 3445）。
- **detcheck**（冷1/冷2/暖3）五类产物全等；**chaincheck** 一跳 500/500、多跳 408/408、alias 328（= 基线）；**dbdiag** 无异常。
- `verify.sh --quick`、`-race ./...`（13 包）、`integration` 全绿。
- **A/B（旧=4af284d vs 新，交错 2 轮）**：wall 7.87/7.69s vs 7.99/7.54s（噪声内）；Max RSS 812/802MB vs 831/788MB（噪声内）。
- **同二进制 profile 汇总（旧 → 新）**：TotalAlloc **1448MB → 1381MB（-4.6%）**；mallocs **20.24M → 19.92M（-1.6%）**；峰值 live 346MB → 354MB（+2.3%，批次池保留容量与 fnID 缓存，属有意的小额驻留）。分项：`os.ReadFile` 55.1MB→13.0MB、`SaveBatchStats` 累计 157.7MB→135.3MB、`resolveNodeRefsCached` 32.0MB→20.7MB、`nodeDedup` 11.8MB→6.2MB、`GoSymbolID` 11.5MB→8.5MB。**wall/RSS 未宣称收益**（噪声内）。

### 剩余候选（本机可做，按证据排序，未动）

- `database/sql.driverArgsConnLocked` 37.6MB：每行 Exec 的 `[]driver.NamedValue` + 装箱；要消除需绕开 `database/sql` 直用 `modernc` 连接（语义/错误映射/事务改动大，需单独 A/B）。
- `emitValue`（35MB flat）/`emitSignatureNodes`（27MB cum）：每个 ssa_value/参数节点的 `map[string]any` 与实体结构，属数据模型成本；共享只读属性 map 会引入可变性风险，暂不动。
- `buildIdentIndex` 17.5MB：全局 `Pos→标识符名` 表；改按需/按函数懒建理论上更低，但内联值的 `Pos` 属于被内联函数（跨函数），需证明等价后才可做。
- `newFuncSnapshot` 的 `fn.String()` 排序键（约 20MB，一次性）：换复合键可省渲染，但会改变函数遍历顺序，需先证明代表选择/边序完全不受顺序影响。
- `encoding/json`（v2 实现）`bytes.Clone` 21MB、`go/types`/`x/tools` 内部（`recordTypeAndValue` 69MB、`typeWriter` 55MB、`NewMethodSet` 等）：第三方/标准库路径，改动风险大于收益。

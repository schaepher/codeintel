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

## 5. 未完成（沿用 changelog4 的验收要求）

- **P0**：严格比较里不属于匿名字段修复的内容差异（40 个节点名称、97 条
  dispatch_to 的 `register_site`、3 条间接写 `code_snippet`）——需要大仓副本与
  固定工具链/并行度做双冷构建，本机无该仓库。
- **P1**：`emitValue` 分配（峰值累计约 4 GiB）——先测属性 map、装箱、重复
  字符串贡献，再考虑类型化临时数据/减少重复属性物化。
- **P2**：包缓存 gob 编码临时分配（`WriteString` 累计约 2.51 GiB）——先测
  缓冲区增长与复用上限，不直接换文件协议。
- **测量条件统一**：固定 GOROOT/工具链/GOMAXPROCS/workers/源码输入，记录
  TotalAlloc / NumGC / 累计 GC CPU，区分峰值堆采样累计值与全程累计值。
- 大仓冷/暖保真与 chaincheck 历史基线：需大仓副本，本机 go2o 可作较小口径
  的对照（`scripts/detcheck.sh`、`scripts/chaincheck.sh`）。

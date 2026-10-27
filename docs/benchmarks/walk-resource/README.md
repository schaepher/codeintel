# 递归查询资源基准（todo22-14.4-B）

**是什么**：`internal/infrastructure/sqlite` 里的独立子进程资源基准——两档临时
SQLite 图（N=256 / N=4096，各 3N 条 calls 边），对同一锚点 `N0000`（方向
callees、minConfidence=0.8）跑三种 SQL 变体，逐样本记录耗时/峰值 RSS/临时
文件峰值，并自动校验结果摘要与边数。

- 变体：`recursive`（物化前的原递归 CTE）、`materialized`（生产 SQL，`AS
  MATERIALIZED`）、`direct_one_hop`（depth<=1 的生产直接邻接查询，仅 depth=1 记录）。
  三组 SQL 全部**复用生产代码**（`walkDirectQuery` / `walkRecursiveQuery`），
  基准里没有复制 SQL 文本。
- 期望边数与摘要校验在测试里断言：任一档不符或任一变体摘要不一致 → 测试失败。

## 一条完整可复现命令

```sh
CODEINTEL_WALK_BENCH=1 \
CODEINTEL_WALK_BENCH_OUT="$PWD/.tmp/walkbench" \
go test ./internal/infrastructure/sqlite \
  -run '^TestWalkEdgesResourceBench$' -count=1 -v -timeout 60m
```

- `CODEINTEL_WALK_BENCH=1` 才启用（默认 skip，不会进日常测试）。
- `CODEINTEL_WALK_BENCH_REPS=N`：每组（N × depth × variant）样本数，默认 **3**（本记录即 3）。
- `CODEINTEL_WALK_BENCH_OUT=<dir>`：产物目录（默认 `t.TempDir()` 会被清理）。产出
  `samples.jsonl`（逐样本原始数值）、`summary.json`、`summary.md`；失败样本会保留
  `run-*` 现场目录（result/pid/time/child.log）。
- 本次实测总耗时 **1282s**（66 样本，串行；depth=128 的 6 个样本占 ~19 分钟）。

## 环境与版本（由基准自身记录）

| 项 | 值 |
| --- | --- |
| Go | go1.27.0 linux/amd64，GOMAXPROCS=8（NumCPU=8） |
| 驱动 | modernc.org/sqlite v1.57.0 / SQLite 3.53.3 |
| 连接配置 | `temp_store=0`（default，file 系）、`cache_size=-131072` 页（生产 DSN，见 `db.go`） |
| 机器内存 | 3396 MiB（`/proc/meminfo MemTotal`） |
| 峰值 RSS 采集 | `/usr/bin/time -v` 的 `Maximum resident set size`（父进程侧），并以子进程 `/proc/self/status VmHWM` 交叉核对 |
| 临时文件采集 | 父进程每 5ms 采 `/proc/<pid>/fd`，统计目标落在子进程 TMPDIR 的 fd 大小之和（含已 unlink 仍打开的文件），只在采样点落在**查询时间窗**内时才把 0 视为有效观测 |
| 夹具 | N=256：nodes=256 / edges=768，建图 27.2ms；N=4096：nodes=4096 / edges=12288，建图 171.8ms |

## 期望边数 vs 实测

| N | depth=1 | depth=2 | depth=8 | depth=32 | depth=128 |
| --- | ---: | ---: | ---: | ---: | ---: |
| 256 | 3 / 3 | 12 / 12 | 66 / 66 | 282 / 282 | 768 / 768 |
| 4096 | 3 / 3 | 12 / 12 | 66 / 66 | 282 / 282 | 1146 / 1146 |

（`期望 / 实测`；两档的 `recursive` 与 `materialized` 全部命中，`direct_one_hop`
在 depth=1 命中。每个 (N, depth) 组内所有样本的规范化摘要
（source|target|kind|tool_source|confidence|归一化 metadata 排序后 SHA-256）
**完全相同**——见 `summary.json` 的 `result_digest`。）

## 汇总对照（耗时中位数 / 最大 RSS / 临时存储峰值 / 比值）

| N | depth | 期望边 | 摘要(前12) | recursive 中位ms | materialized 中位ms | direct 中位ms | 物化/原递归耗时 | 最大RSS rec/mat (MiB) | RSS比 | 临时峰值 rec/mat (B) |
| --- | --- | --- | --- | ---: | ---: | ---: | ---: | --- | --- | --- |
| 256 | 1 | 3 | b45616b1d7f0 | 0.99 | 1.00 | 0.18 | 1.01x | 13.3/13.4 | 1.01x | unavailable/unavailable |
| 256 | 2 | 12 | 3cf4b243a882 | 2.75 | 2.67 | - | 0.97x | 13.2/13.2 | 1.00x | unavailable/0 |
| 256 | 8 | 66 | 68fbf8ba553a | 44.79 | 42.88 | - | 0.96x | 13.5/13.7 | 1.01x | 0/0 |
| 256 | 32 | 282 | 9a47e758746e | 723.45 | 726.03 | - | 1.00x | 15.3/16.7 | 1.09x | 0/0 |
| 256 | 128 | 768 | 60d52792cd8a | 12149.23 | 12264.48 | - | 1.01x | 27.6/36.9 | 1.33x | 12054528/20709376 |
| 4096 | 1 | 3 | b45616b1d7f0 | 11.80 | 11.63 | 0.21 | 0.99x | 17.3/17.3 | 1.00x | 0/0 |
| 4096 | 2 | 12 | 3cf4b243a882 | 41.29 | 39.73 | - | 0.96x | 17.3/17.2 | 0.99x | 0/0 |
| 4096 | 8 | 66 | 68fbf8ba553a | 698.87 | 688.45 | - | 0.99x | 17.8/17.7 | 0.99x | 0/0 |
| 4096 | 32 | 282 | 9a47e758746e | 11534.58 | 11592.46 | - | 1.01x | 19.2/20.9 | 1.09x | 0/0 |
| 4096 | 128 | 1146 | 2e5fee56136d | 186912.31 | 188316.99 | - | 1.01x | 29.5/37.3 | 1.26x | 11915264/20529152 |

取消契约自检: depth=1000000 预算 25ms → elapsed=26.2ms, DeadlineExceeded=true, 复用后一跳边=3

说明：`物化/原递归耗时` = 物化变体耗时中位数 ÷ 原递归耗时中位数；`RSS` 为样本
最大值；`临时峰值` 为该组样本最大值（`0` 表示查询期采样覆盖内从未出现落盘临时
文件）。

## 逐样本原始数值（66 条，与 `samples.jsonl` 一致）

| # | N | depth | variant | sample | class | result_edges | query_ms | wall_ms | rss_before KiB | max_rss | temp_peak | temp_samples | exit |
| ---: | ---: | ---: | --- | ---: | --- | ---: | ---: | ---: | ---: | --- | --- | ---: | ---: |
| 0 | 256 | 1 | recursive | 0 | cold_first_query | 3 | 1.00 | 6 | 13012 | 13492 KiB | unavailable | 1 | 0 |
| 1 | 256 | 1 | recursive | 1 | warm_repeat | 3 | 0.99 | 6 | 13084 | 13428 KiB | unavailable | 1 | 0 |
| 2 | 256 | 1 | recursive | 2 | warm_repeat | 3 | 0.96 | 6 | 13092 | 13620 KiB | unavailable | 1 | 0 |
| 3 | 256 | 1 | materialized | 0 | warm_repeat | 3 | 0.98 | 6 | 12944 | 13492 KiB | unavailable | 1 | 0 |
| 4 | 256 | 1 | materialized | 1 | warm_repeat | 3 | 1.17 | 6 | 13088 | 13428 KiB | unavailable | 1 | 0 |
| 5 | 256 | 1 | materialized | 2 | warm_repeat | 3 | 1.00 | 6 | 13136 | 13744 KiB | unavailable | 1 | 0 |
| 6 | 256 | 1 | direct_one_hop | 0 | warm_repeat | 3 | 0.18 | 6 | 13088 | 13108 KiB | unavailable | 1 | 0 |
| 7 | 256 | 1 | direct_one_hop | 1 | warm_repeat | 3 | 0.18 | 6 | 13088 | 13168 KiB | unavailable | 1 | 0 |
| 8 | 256 | 1 | direct_one_hop | 2 | warm_repeat | 3 | 0.18 | 5 | 13088 | 13044 KiB | unavailable | 1 | 0 |
| 9 | 256 | 2 | recursive | 0 | warm_repeat | 12 | 2.75 | 9 | 12936 | 13492 KiB | unavailable | 1 | 0 |
| 10 | 256 | 2 | recursive | 1 | warm_repeat | 12 | 2.74 | 8 | 12972 | 13492 KiB | unavailable | 1 | 0 |
| 11 | 256 | 2 | recursive | 2 | warm_repeat | 12 | 2.96 | 8 | 12920 | 13360 KiB | unavailable | 1 | 0 |
| 12 | 256 | 2 | materialized | 0 | warm_repeat | 12 | 2.66 | 8 | 13088 | 13552 KiB | unavailable | 1 | 0 |
| 13 | 256 | 2 | materialized | 1 | warm_repeat | 12 | 2.67 | 8 | 13088 | 13488 KiB | 0 B | 1 | 0 |
| 14 | 256 | 2 | materialized | 2 | warm_repeat | 12 | 2.71 | 7 | 13088 | 13552 KiB | unavailable | 1 | 0 |
| 15 | 256 | 8 | recursive | 0 | warm_repeat | 66 | 44.79 | 51 | 12976 | 13808 KiB | 0 B | 10 | 0 |
| 16 | 256 | 8 | recursive | 1 | warm_repeat | 66 | 45.23 | 52 | 12936 | 13684 KiB | 0 B | 10 | 0 |
| 17 | 256 | 8 | recursive | 2 | warm_repeat | 66 | 43.20 | 50 | 12936 | 13488 KiB | 0 B | 9 | 0 |
| 18 | 256 | 8 | materialized | 0 | warm_repeat | 66 | 42.82 | 49 | 12940 | 13808 KiB | 0 B | 9 | 0 |
| 19 | 256 | 8 | materialized | 1 | warm_repeat | 66 | 42.88 | 49 | 13088 | 14004 KiB | 0 B | 9 | 0 |
| 20 | 256 | 8 | materialized | 2 | warm_repeat | 66 | 43.38 | 50 | 13084 | 13808 KiB | 0 B | 9 | 0 |
| 21 | 256 | 32 | recursive | 0 | warm_repeat | 282 | 728.99 | 736 | 13136 | 15668 KiB | 0 B | 146 | 0 |
| 22 | 256 | 32 | recursive | 1 | warm_repeat | 282 | 723.45 | 730 | 13160 | 15476 KiB | 0 B | 145 | 0 |
| 23 | 256 | 32 | recursive | 2 | warm_repeat | 282 | 722.62 | 730 | 12940 | 15348 KiB | 0 B | 145 | 0 |
| 24 | 256 | 32 | materialized | 0 | warm_repeat | 282 | 725.51 | 732 | 12936 | 17008 KiB | 0 B | 146 | 0 |
| 25 | 256 | 32 | materialized | 1 | warm_repeat | 282 | 727.25 | 734 | 13096 | 17136 KiB | 0 B | 146 | 0 |
| 26 | 256 | 32 | materialized | 2 | warm_repeat | 282 | 726.03 | 732 | 13088 | 16820 KiB | 0 B | 146 | 0 |
| 27 | 256 | 128 | recursive | 0 | warm_repeat | 768 | 12369.54 | 12379 | 13084 | 27888 KiB | 12054528 B | 2475 | 0 |
| 28 | 256 | 128 | recursive | 1 | warm_repeat | 768 | 12104.53 | 12113 | 13088 | 28016 KiB | 12054528 B | 2422 | 0 |
| 29 | 256 | 128 | recursive | 2 | warm_repeat | 768 | 12149.23 | 12156 | 12956 | 28272 KiB | 12054528 B | 2431 | 0 |
| 30 | 256 | 128 | materialized | 0 | warm_repeat | 768 | 12243.14 | 12251 | 12956 | 37740 KiB | 20709376 B | 2449 | 0 |
| 31 | 256 | 128 | materialized | 1 | warm_repeat | 768 | 12264.48 | 12273 | 12996 | 37556 KiB | 20709376 B | 2454 | 0 |
| 32 | 256 | 128 | materialized | 2 | warm_repeat | 768 | 12359.67 | 12367 | 13104 | 37556 KiB | 20709376 B | 2473 | 0 |
| 33 | 4096 | 1 | recursive | 0 | cold_first_query | 3 | 11.58 | 17 | 13088 | 17644 KiB | 0 B | 3 | 0 |
| 34 | 4096 | 1 | recursive | 1 | warm_repeat | 3 | 12.69 | 19 | 13084 | 17524 KiB | 0 B | 3 | 0 |
| 35 | 4096 | 1 | recursive | 2 | warm_repeat | 3 | 11.80 | 18 | 13088 | 17716 KiB | 0 B | 3 | 0 |
| 36 | 4096 | 1 | materialized | 0 | warm_repeat | 3 | 11.67 | 18 | 13092 | 17716 KiB | 0 B | 3 | 0 |
| 37 | 4096 | 1 | materialized | 1 | warm_repeat | 3 | 11.63 | 17 | 13016 | 17584 KiB | 0 B | 3 | 0 |
| 38 | 4096 | 1 | materialized | 2 | warm_repeat | 3 | 11.54 | 18 | 12960 | 17524 KiB | 0 B | 3 | 0 |
| 39 | 4096 | 1 | direct_one_hop | 0 | warm_repeat | 3 | 0.18 | 5 | 13100 | 12916 KiB | unavailable | 1 | 0 |
| 40 | 4096 | 1 | direct_one_hop | 1 | warm_repeat | 3 | 0.21 | 7 | 12956 | 12848 KiB | unavailable | 1 | 0 |
| 41 | 4096 | 1 | direct_one_hop | 2 | warm_repeat | 3 | 0.21 | 6 | 13096 | 13168 KiB | unavailable | 1 | 0 |
| 42 | 4096 | 2 | recursive | 0 | warm_repeat | 12 | 41.75 | 48 | 13096 | 17648 KiB | 0 B | 9 | 0 |
| 43 | 4096 | 2 | recursive | 1 | warm_repeat | 12 | 40.11 | 47 | 13088 | 17716 KiB | 0 B | 9 | 0 |
| 44 | 4096 | 2 | recursive | 2 | warm_repeat | 12 | 41.29 | 48 | 12940 | 17520 KiB | 0 B | 9 | 0 |
| 45 | 4096 | 2 | materialized | 0 | warm_repeat | 12 | 39.13 | 46 | 13084 | 17580 KiB | 0 B | 8 | 0 |
| 46 | 4096 | 2 | materialized | 1 | warm_repeat | 12 | 39.91 | 47 | 12956 | 17452 KiB | 0 B | 9 | 0 |
| 47 | 4096 | 2 | materialized | 2 | warm_repeat | 12 | 39.73 | 47 | 12936 | 17520 KiB | 0 B | 9 | 0 |
| 48 | 4096 | 8 | recursive | 0 | warm_repeat | 66 | 701.53 | 708 | 13136 | 18224 KiB | 0 B | 141 | 0 |
| 49 | 4096 | 8 | recursive | 1 | warm_repeat | 66 | 695.49 | 702 | 12956 | 17844 KiB | 0 B | 140 | 0 |
| 50 | 4096 | 8 | recursive | 2 | warm_repeat | 66 | 698.87 | 706 | 12960 | 17844 KiB | 0 B | 140 | 0 |
| 51 | 4096 | 8 | materialized | 0 | warm_repeat | 66 | 698.26 | 705 | 12940 | 17904 KiB | 0 B | 140 | 0 |
| 52 | 4096 | 8 | materialized | 1 | warm_repeat | 66 | 688.45 | 695 | 13084 | 17972 KiB | 0 B | 138 | 0 |
| 53 | 4096 | 8 | materialized | 2 | warm_repeat | 66 | 687.07 | 696 | 13132 | 18100 KiB | 0 B | 138 | 0 |
| 54 | 4096 | 32 | recursive | 0 | warm_repeat | 282 | 11534.58 | 11543 | 12936 | 19700 KiB | 0 B | 2308 | 0 |
| 55 | 4096 | 32 | recursive | 1 | warm_repeat | 282 | 11475.25 | 11483 | 13084 | 19696 KiB | 0 B | 2296 | 0 |
| 56 | 4096 | 32 | recursive | 2 | warm_repeat | 282 | 11544.14 | 11551 | 13084 | 19700 KiB | 0 B | 2309 | 0 |
| 57 | 4096 | 32 | materialized | 0 | warm_repeat | 282 | 11742.86 | 11750 | 13208 | 21428 KiB | 0 B | 2349 | 0 |
| 58 | 4096 | 32 | materialized | 1 | warm_repeat | 282 | 11557.22 | 11565 | 12968 | 21044 KiB | 0 B | 2312 | 0 |
| 59 | 4096 | 32 | materialized | 2 | warm_repeat | 282 | 11592.46 | 11600 | 13084 | 21172 KiB | 0 B | 2319 | 0 |
| 60 | 4096 | 128 | recursive | 0 | warm_repeat | 1146 | 186912.31 | 186923 | 13084 | 29680 KiB | 11915264 B | 37384 | 0 |
| 61 | 4096 | 128 | recursive | 1 | warm_repeat | 1146 | 186558.05 | 186567 | 13088 | 30256 KiB | 11915264 B | 37313 | 0 |
| 62 | 4096 | 128 | recursive | 2 | warm_repeat | 1146 | 188142.65 | 188152 | 13084 | 29936 KiB | 11915264 B | 37629 | 0 |
| 63 | 4096 | 128 | materialized | 0 | warm_repeat | 1146 | 188316.99 | 188325 | 12960 | 38128 KiB | 20529152 B | 37664 | 0 |
| 64 | 4096 | 128 | materialized | 1 | warm_repeat | 1146 | 191489.35 | 191497 | 13084 | 38196 KiB | 20529152 B | 38299 | 0 |
| 65 | 4096 | 128 | materialized | 2 | warm_repeat | 1146 | 188100.14 | 188109 | 12936 | 38128 KiB | 20529152 B | 37620 | 0 |

取消契约自检（同一次运行内，N=4096 夹具）：`depth=1000000` + 25ms 预算 →
`elapsed=26.2ms`、`context.DeadlineExceeded=true`、随后**同一连接**一跳查询返回 3 条边
（证明 SQL 真的停了，而不是调用方提前返回）。

## 测量边界与未验范围

- **耗时**：`query_ms` = `QueryContext` → 全部 `Rows.Next/Scan` 完成 → `rows.Close()`
  之前；`process_wall_ms` 是整个子进程（含打开/关闭库、Go 运行时启动）。
- **RSS**：`rss_before_bytes` 是查询前 RSS（**不是**查询带来的增量）；
  `max_rss_bytes` 是进程峰值 RSS。物化/原递归 RSS 比 1.26–1.33x（depth=128）里
  包含驱动/页缓存/扫描结果的共同部分，不能当作"物化 CTE 自身占用"。
- **临时存储**：temp_store=0 时 SQLite 可能把物化 CTE/临时索引留在页缓存而不落盘；
  depth≤32 全部为 0 且采样点覆盖了查询窗口，因此这是**有效观测**（不是采集失败）。
  query_ms 小于采样间隔的样本（depth=1/2 的部分变体）标 `unavailable` 并给出原因。
- **冷暖**：每个样本都是新进程（SQLite 连接级缓存必冷）；`cold_first_query` 仅指
  该 N 库本轮的第一个查询。**没有 drop OS page cache（无权限）**，且夹具刚由
  `SaveBatchStats` 写入，所以"冷"不等于"冷 OS 缓存"。
- **不做优化判断**：本基准只交付测量。depth=128 在 N=4096 上约 **187–188s**、结果
  仅 1146 条边，是所有变体共同现象（物化/原递归耗时比 0.96–1.01x）——记录事实，
  不据此改生产 SQL/索引/schema/超时。
- **未验**：原始 TODO 里的"约 250 万节点业务仓库 61s 延迟"未复现；本夹具是 SQL
  资源机制夹具，不是完整源码 init，绝对值不可外推到大库。

## 复跑与对照

- 同一命令复跑：变异在 ~5%（中位数），depth=128 的临时峰值字节数三次完全一致
  （12054528 / 20709376），说明该值由数据规模而非调度决定。
- 三个变体共用同一测试二进制与同一 DSN（不是 `-N -l` 调试二进制对照）。
- 上游语义回归（不替代本基准）：
  `go test ./internal/infrastructure/sqlite -run 'TestWalkEdges|TestCallersCalleesDepthAndConfidence|TestCalleesContextInterruptsRecursiveSQL' -count=1 -timeout=30s`、
  `go test -race ./internal/infrastructure/sqlite -run 'TestWalkEdges|TestCalleesContextInterruptsRecursiveSQL' -count=10 -timeout=60s`。

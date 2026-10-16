# changelog4 执行清单（2026-10-16）

来源：`codeintel-changelog4.txt`。本仓库自 `d7f951d` 起按 changelog4 实施；
「已完成待同步」一批**在另一套环境完成、本仓库原本不存在**，故按描述在本仓库
重新实现（含测试）。逐项证据如下。

## A. 已完成待同步的任务（本仓库重新实现）

| # | 项 | 提交 | 证据 |
|---|---|---|---|
| 1 | SSA 字符串缓存（类型字符串 + candidateKey，构建级共享） | `6e82acb` | `ssa/string_cache.go`、`ssa/string_cache_test.go`（并发/泛型/空/隔离/零分配）；本机派发键 631ns→49ns |
| 2 | 无效调试日志计算优化（Debug 关闭不构造昂贵字段） | `6e82acb` | `ssa/cf_call.go` `logDynInterfaceDispatch`、`ssa/cf_call_debug_test.go` |
| 3 | 等价节点写入消除（node_batch.go） | `32fe7ac` | `orchestrator/node_batch.go` + 测试（A→B→A 保留/内容差异/不同 ID） |
| 4 | 构建期端点正缓存（build_write_cache.go） | `0af5f7f` | `sqlite/build_write_cache.go` + 测试（提交后发布/缺失不缓存/回滚/重建隔离/并发） |
| 5 | CPU profile 收尾修复（cpu_profile.go） | `6e9d0ba` | `cmd/codeintel/cpu_profile.go` + 测试（gzip + `go tool pprof -raw` 可解码） |
| 6 | 优化回归测试与调查记录 | `083a599` | `docs/codeintel-gc-investigation.md`、`docs/field_trace.md` |
| 7 | 匿名嵌套字段类型路径修复（fe_nested.go） | `077dbed` | `ssa/fe_nested.go`、`ssa/fe_anonymous_path_test.go` |
| 8 | 匿名容器元素字段修复（[*] 层级 / 两层 / 子切片） | `077dbed` | 同上（索引/遍历/数组/子切片/两层匿名元素） |

## B. 待完成代码任务

- [x] 修复 `scripts/verify.sh` / `scripts/chaincheck.sh` shell 变量边界
      （`6e9d0ba` 之后 `4de1106`：中文标点前用 `${var}`）——`bash -n` 通过，
      verify/chaincheck 已实测。
- [x] **P1 端点解析临时容器**（`c3d8940`）：直消费 `edges`（去 `edgeEndpointIDs`）、
      `partition` 命中直写结果 map、miss 走 `resolveNodeRefsUnique`（不二次去重）。
      本机基准 58.5µs/94KB/19allocs → 41.6µs/74KB/15allocs。
- [x] **P2 有界端点缓存淘汰**（`c3d8940`）：整表清空 → 两代分段轮换；幂律序列
      同容量命中率 8.7–8.8% → 12.1–12.2%（护栏测试）。
- [x] **P2 包缓存编码分配评估**（`cdbb79a` 起文档）：分块 gob 已比整包少 ~62% 分配；
      剩余为反射开销 → **不换协议/不加无界缓冲**（数据见调查文档 §4）。
- [ ] **P0 严格比较内容差异**（40 节点名称 / 97 dispatch_to `register_site` /
      3 间接写 `code_snippet`）：**需大仓副本**（约 240 万节点 / 742 万边），
      本机无该仓库；需固定工具链与并行度做双冷构建。
- [ ] **P1 `emitValue` 分配**：需先在大仓峰值 profile 上拆解属性 map/装箱/重复
      字符串贡献，再决定类型化临时数据；本机无该 profile。
- [ ] 统一测量条件：模板已写入 `docs/codeintel-gc-investigation.md` §6；
      实际对照数据需在大仓按模板采集。

## C. 待完成验收 Checklist

- [x] 匿名字段路径：`fe_anonymous_path_test.go` 覆盖指针读写/值读取/副本/不同
      所属类型/容器元素/子切片/两层匿名元素/独立回退。
- [x] SSA/sqlite/orchestrator/cmd 四包 `-race` 全绿（2026-10-16）。
- [x] `integration`（`make it`，含 scip-go）通过；`scripts/verify.sh --quick` 全绿。
- [x] shell 脚本：verify/chaincheck 实测可跑到底层测试（`bash -n` + 运行）。
- [ ] 大仓冷/暖保真、chaincheck 历史基线、PlantUML/Git 环境全量验收：需大仓
      与环境配置，本机未做（不冒用远端记录）。
- [ ] VSIX 打包/安装：需联网环境（同 changelog3 遗留）。

## D. 本机验证汇总

- `go build ./...`、`go vet ./...` 通过。
- `go test ./...`（非 race）全绿；`scripts/verify.sh --quick` 全绿。
- 四包 `-race`：ssa 26.3s / sqlite 23.3s / orchestrator 6.8s / cmd 1.4s 全绿。
- `go test -tags integration ./integration/` 通过。
- 局部基准与淘汰对照见 `docs/codeintel-gc-investigation.md`。

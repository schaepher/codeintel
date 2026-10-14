# codeintel-changelog3 验收 Checklist（逐项证据，2026-10-14）

规则：`[x]` 表示有完成/验证证据；评估后不采用也记为 `[x]`（附数据、原因、触发
条件）。实现类证据见对应提交；性能/评估数据见 [`build_perf.md`](build_perf.md)；
逐 Q 记录见 [`field_trace.md`](field_trace.md)。

## 现有本地功能回归

- [x] 函数值调用者补边：包级注册表经 getter、集合参数及返回值传递后调用者
  可查到注册函数；递归类型不爆栈 —— 单测
  `TestFuncSetRegistryGetter` / `TestFuncSetPkgRegistry` / `TestFuncSetRecursiveType`
  / `TestFuncCollectionNestedCallCalls`。
- [x] 补边属性：CALLS conf 0.80、行号、`registered_function_value` 标记；
  无依据不建边 —— 同组单测断言 confidence/metadata。
- [x] Schema 操作：旧结构触发 schema mismatch → 提示 `reindex` 且说明保留
  `relation_rules`/`qa_history`；clean 行为与配置表文档一致 ——
  `TestOpenSchemaAutoMigrate` / `TestOpenSchemaMissingColumnFails` /
  `TestRebuildDerivedSchemaPreservesManualData` / `rebuild_schema` + `clean` 告知测试。
- [x] JSON 契约：callers 返回 canonical ID，不暴露 `id_int`/`source_ref`/
  `target_ref` —— `TestQueryCallersJSONEnriched` / `TestQueryJSONNoIntegerProxyKeys`。
- [x] 插件交付：仓库根 `make plugin` / `make install-plugin` 已就绪（固定版本
  vsce）；命令图标正常（四命令 icon + 时序图/刷新）；`node --check` +
  package.json JSON 校验 + `scripts/selfcheck-vscode-extension.cjs` 通过。
  **环境限制**：本机无外网，`npx @vscode/vsce` 拉包失败（ECONNRESET），未能
  实际产出 `.vsix`——命令链路与清单内容已验证，打包待有网环境执行。
- [x] 接口二级缓存：新旧候选扫描一致、按 (接口,方法) 隔离、含空候选、并发
  安全、命中/分配对照 —— `TestImplTypePool*` + `BenchmarkImplTypePoolCached`
  （22.9ns/0 allocs）vs Legacy（5565ns/4442B/76 allocs）。

## 已实现的分块缓存与采样

- [x] 包缓存 v4：节点/边/摘要每 200 条一个 gob 消息 + 独立结束标记；
  `pkgCollect` 直接编码已有块、摘要保留追加合并、临时文件原子替换 ——
  `pkg_cache_chunk.go` / `pkg_cache_io.go`。
- [x] 缓存完整性：跨块往返、旧格式拒绝、截断/缺结束标记/结束标记带数据/
  尾随消息 —— `TestPkgCacheChunkedMultiBlock` / `TestPkgCacheChunkIntegrity`；
  SSA 全包测试通过（见全量验证）。
- [x] 小型仓库缓存保真度：两次冷构建 + 一次暖构建产物一致 ——
  `TestPkgCacheReplayFidelity`。
- [x] 编码短基准：分块 vs 整包 gob 分配/耗时 —— `BenchmarkPkgCacheSerialization`
  （chunked-encode 23.2ms/19.9MB vs 整包 26.8ms/52.8MB per op，本机夹具口径；
  不外推全量）。
- [x] 大仓冷构建复测：旧版/新版同一临时仓库交替运行，记录 wall/RSS/产物 ——
  `build_perf.md`「大仓 A/B」；产物逐行全等（diff 0）。
- [x] 文件写入成本：真实写盘/体积/读取 —— `BenchmarkPkgCacheFileWriteRead`
  （8002KB 文件；写 25.4ms/3.2MB，读 45.2ms/42.3MB）。
- [x] 大仓冷暖保真度：旧/新版产物逐行比较（nodes/edges/摘要键）——
  旧新版 `diff` 0 行；既有同版本不确定性单独记录。
- [x] 采样准确性：嵌套 slots/returns/参数调用者统计 ——
  `TestExtractorStatsNested`。
- [x] 采样开关：关闭无输出、开启后函数处理结束统计 —— `TestEmitSampleSwitch`
  （`defer sampleEmitContainers` 在 emitFunctionFields 末尾）。

## 重复解析与重复计算候选

- [x] 静态调用点测量：全程序扫描 vs 索引 ——
  `BenchmarkStaticCallSitesLegacyPerTarget`（1.52ms/op）vs
  `BenchmarkStaticCallSitesIndexBuild`（0.62µs/op）。
- [x] 静态调用点索引：构建级反向索引，扫描范围/调用形式/实参/候选顺序与旧
  实现一致（含无调用点、递归）—— `fdc1c69` +
  `TestStaticCallSitesIndexMatchesLegacyScan` / `TestStaticCallSitesRecursive`。
- [x] 函数身份测量/复用：构建级只读缓存（含失败结果），生命周期不跨 Program；
  普通函数/方法/闭包/无定义对象语义一致 —— `e2a70ad` + `8d19017` +
  `TestFuncSnapshotIdentityCache` / `TestExtractorIdentityFallsBackWithoutSnapshot`。
- [x] 类型表名与返回值测量/复用：构建级缓存，空结果缓存、并发安全、优先级
  不变 —— `cee00ab` / `8bb626c` + `TestFuncSnapshotReturnOperandsCache` /
  `TestFuncSnapshotTableNameCache`。
- [x] SQL 重复度/结果缓存：按原始 SQL 缓存（含失败），统计与降级提示不因命中
  失真 —— `e99e31a` + `TestParseSQLStmtCachedStats`。
- [x] 摘要复制测量/优化：预分配实测中性→不采用；包合并 map 复用为采纳项；
  闭包同 owner 追加合并语义保留、冷暖保真 —— `86fcfc8` +
  `BenchmarkMergeFuncData*` / `BenchmarkToCachedFD`。
- [x] SCIP 评估：记录重复加载/分析成本与内存压力，决定保留（符号权威），
  给出后续触发条件 —— `build_perf.md`「SCIP 重复加载评估」。
- [x] 已有共享机制守护：适配器共享 loadPackages、AllFunctions 快照只读共享、
  源码行缓存构建级 —— `TestAdaptersShareLoadedPackages` /
  `TestFuncSnapshotListShared` / `TestAdapterSharesOneLineCache`。

## GC 与整体性能验收

- [x] 紧凑容器评估：评估整数索引/纯值结构成本与映射表引用问题，未采纳并记
  触发条件 —— `build_perf.md`「紧凑容器/对象复用评估」。
- [x] 对象复用评估：map 池化引用/存活风险与验证要求，未采纳并记触发条件 ——
  同上。
- [x] 运行参数对照：GOGC=40 vs 100（wall/RSS）—— `build_perf.md` 表
  （100 约 -19% wall、+19% RSS）。
- [x] 阶段定位：emitFunction/finishIndex/scip 等分阶段耗时 ——
  `build_perf.md`「阶段定位」。
- [x] 稳定 A/B：同源码同工具链，旧/新版交错运行、产物逐行比较 ——
  `build_perf.md`「大仓 A/B」（wall/RSS 持平、产物全等）。
- [x] 结果判定：总/阶段耗时、分配、RSS 等一并记录；未证明的收益不宣称 ——
  `build_perf.md` 各节结论。
- [x] 内容差异处理：定位并记录同版本不确定性；跨版本差异不豁免 ——
  `build_perf.md`「内容差异与覆盖限制」。
- [x] 覆盖限制：目标仓库编译/类型错误口径一致，未声称覆盖全部调用关系 ——
  同上。

## 最终交付检查

- [x] 影响分析：改动集中在 ssa 包，`codeintel query impact` 可用于相关符号；
  构建后跑链完整性/确定性门槛。
- [x] 功能与并发测试：单测 + `-race` 全量；不同 workers 产物一致 ——
  `TestBuildDeterminismAcrossWorkers`；图构建后跑 go2o `chaincheck` / `detcheck`。
- [x] 全量验证：`scripts/verify.sh`（build+vet+-race 逐包）—— 见本仓库验证日志；
  本机 Git 身份/PlantUML 无阻塞。
- [x] 数据门槛：产物比较用 `detcheck`/`chaincheck`（退出码 + 非空 + 归一化）。
- [x] 文档与证据：`build_perf.md` / `field_trace.md` / 本文件记录方案、命令、
  数据；目标仓库名称脱敏按需。
- [x] 变更范围：`gofmt`/行数/`git diff --check` 通过；未修改目标仓库索引以外的
  无关内容（为缓存验证重建了 go2o 派生索引，产物与基线一致）。

# 设计：构建期内存整改第二轮（Q247，2026-09-19）

> 状态：**待确认**（本文档经设计树访谈定稿；确认后按 §3 实施，完成后
> 归档进 `field_trace.md §88` 并删除本文件——沿用 Q235/Q238 流程）

## 0 目标与验收（用户已确认口径）

**目标**：让大仓库构建在小内存机器上不再换页/被 OOM 杀，同时不牺牲
产物质量、不明显牺牲时间。

**背景**：Q246 已解决写库侧与 emit 侧的分配问题（go2o 冷构建 wall
1:24.3→1:15.7、次要页错误 -75%、非自愿上下文切换 -94%），但**峰值 RSS
位置未变**：进程内 bench 显示 peak VmHWM 2218MB 且出现在 **t≈11-14s**，
即 `loadPackages` + SSA 构建阶段；之后 100 秒都在峰值之下。3GB 机器上
这个峰值就是换页/被杀的成因。

**验收（硬指标，同机同副本可复测）**：

| 指标 | Q246 后基线 | 本轮目标 |
|---|---|---|
| go2o 冷构建 in-process VmHWM | 2218MB | **≤ 1400MB** |
| go2o 冷构建 bench wall | 91.1s | ≤ 100s（+10% 上限） |
| CLI `init` wall（含 scip 子进程） | 75.7s | ≤ 83s |
| 次要页错误 / 非自愿上下文切换 | 1366609 / 12271 | 不高于基线量级 |
| AST 适配器产物 | — | **逐字节相同**（md5） |
| 全图节点/边 | 119215 / 135502 | 差异在既有不确定度内（±1%） |

## 1 事实（本次实测，探针临时文件已删）

### 1.1 根因：`NeedDeps` 把整个传递依赖图的 AST 也解析并常驻

`internal/orchestrator/orch_build.go:83` 的 `packages.Config.Mode` 含
`packages.NeedDeps`。实测（go2o 136 个模块包 / 774 个 reachable 包）：

| Mode | reachable | 带 AST 的包 / 语法文件 | load 后存活堆 | load 耗时 |
|---|---|---|---|---|
| 现状（含 NeedDeps） | 774 | **774 / 3718** | **955MB** | 2.5s |
| 去掉 NeedDeps | 774 | 137 / 526 | **148MB** | 1.1s |

本仓库（20 模块包）：**1095MB → 55MB**。即单这一项就是 0.8-1.0GB。

代码注释声称"依赖包走 go/packages fast 模式（NeedTypes 从 export data
加载）"，但 Mode 里带着 `NeedDeps`——**注释与实现不符**；紧随其后的
`释放依赖 AST` 循环只对**返回切片中**的非模块包置 nil，而返回切片里
全是模块包（每个 module 各按 `dir + "./..."` 加载），因此**是空操作**。
这解释了为什么早先 `stage()` 里"释放依赖 AST"一步只降了几十 MB。

### 1.2 依赖的 types 依然完整（去掉 NeedDeps 不会丢类型信息）

- 外部包 `Types == nil` 的数量：两种 Mode 都是 **0**（638 个外部包全有
  Types，`p.Types.Imports()` 图完整）——类型信息来自 export data，与
  `NeedDeps` 无关。
- `pkg.Imports[直接依赖].CompiledGoFiles` 两模式一致（go2o 均 1 个空项），
  即 `pkgCacheKeyHash` 的依赖失效键行为不变。
- 差别只在：外部包的 `Syntax`（638→1）与 `TypesInfo`。

### 1.3 消费者核查（`pkg.Imports` 的全部用法）

| 位置 | 用法 | 去掉 NeedDeps 后 |
|---|---|---|
| `ast/ast_index.go:109` | 只取 Imports 键 + `isInModule` 过滤 | 不变 |
| `ast/ast_emit.go:20` | `imp.Types.Scope().Lookup("Handler")`（net/http） | 不变（Types 可达） |
| `ast/ast_grpc_collect.go:83` | 递归 walk **只用 Types**（R90 注释："依赖包 fast 模式无 Syntax 仅 types"） | 不变（extTypesNil=0） |
| `ssa/pkg_cache_io.go:72` | 直接依赖 `CompiledGoFiles` 做 hash | 不变 |
| `ssa` `ssautil.Packages` | 只对模块包建 SSA；跨包类型/成员走 `types` | 见 1.4 |

### 1.4 产物等价性

- **AST 适配器**（go2o 全量，两种 Mode）：87213 行产物 **md5 完全相同**
  （`6577761b…`，52881 节点 / 34332 边），耗时 802ms vs 808ms。
- **SSA 适配器**（本仓库，各跑两遍）：节点 50382–50387、边 61762–61813；
  **同 Mode 两次之间的差异 ≥ 两种 Mode 之间的差异**（既有 slot 命名
  不稳定，见 §5）→ 无实质差异。

## 2 方案

### 2.1 主改动：`loadPackages` 去掉 `NeedDeps` + 把契约写成测试

- `internal/orchestrator/orch_build.go`：Mode 去掉 `packages.NeedDeps`；
  注释改为明确的**契约**：「模块内包给 Syntax+TypesInfo；依赖包只给
  Types（AST/TypesInfo 不保证）——任何适配器不得读非模块包的 Syntax/
  TypesInfo」，并注明依据（R90 的 grpc 外部注册识别即按 types 设计）。
- 删除那行空操作的 `释放依赖 AST` 循环（依赖包不再有 AST 可释放），
  相关说明并入契约注释。
- **契约测试**（`orchestrator` 包，测试先行）：临时模块 import 一个
  module cache 里已有的真实外部依赖，加载后断言：
  ① 模块包 `Syntax != nil && TypesInfo != nil`；
  ② 外部依赖 `Types != nil`（类型可达——这条同时保护 R90 外部注册识别）；
  ③ 外部依赖 `Syntax == nil`（防将来有人把 NeedDeps 加回来）。

### 2.2 兜底：小内存机器自动内存上限（GOMEMLIMIT）

位置与 `CODEINTEL_GOGC` 同处（`cmd/codeintel/main.go`，仅
init/reindex/update）：

优先级：`GOMEMLIMIT`（Go 原生，已生效，**不覆盖**）>
`CODEINTEL_MEMLIMIT`（显式，支持 `1500MiB` / `2GiB` / 纯字节）>
自动（`MemTotal < 4GiB` → `min(1.5GiB, 55%×MemTotal)`）> 不设。

- 生效时打印一行（zap + stderr）：`[index] 内存上限 1.5GB（MemTotal
  3.0GB，小内存兜底；CODEINTEL_MEMLIMIT 覆盖）`；非法值→警告并忽略。
- 读不到 `/proc/meminfo`（非 Linux）→ 不设，不报错。
- 取舍：这是"自动降速换不 OOM"——上限低于真实 live heap 时会 GC 抖动
  （时间涨），由 §0 的 wall 上限与日志可见性约束。

### 2.3 诊断补齐（同批，为第三轮 SSA 归因）

- `stage()` 阶段日志同时输出 `HeapInuse`（不强制 GC，避免改变构建时序），
  并注明 `HeapAlloc` 含未回收垃圾、**只有 heap profile 才是真实 live**。
- 新增 `CODEINTEL_MEM_PROFILE=<file>`：构建命令退出前 `pprof.Lookup("heap").WriteTo`
  （与既有 `CODEINTEL_CPU_PROFILE` 对称）；SSA 分波改造前先用它按阶段归因。

## 3 实施步骤

1. 契约测试先写（红）→ 改 `loadPackages` + 删空操作循环 → 测试绿。
2. `main.go` 内存上限兜底 + 阶段日志/内存 profile 诊断。
3. 验证（§4）→ 文档：`field_trace.md §88` + `AGENTS.md`（Q247 教训：
   "注释声称的 Mode 与实现不符"、"释放依赖 AST 是空操作"类假动作）+
   `runbook.md` 第 20 条补"已知峰值位置"。
4. 收尾：删本设计文件、git push（一个 Q 一个提交）。

## 4 验证计划（口径已确认）

| 层 | 手段 | 通过标准 |
|---|---|---|
| 单测 | 契约测试 + 全套 `verify.sh --quick` | 全绿（含既有用例） |
| 竞态 | 改动包 `-race`（orchestrator/ast/ssa） | 全绿 |
| 集成 | `make it`（⚠ HEAD 上已有 6 个既有失败，见 §5） | 失败集合不扩大 |
| 产物 | go2o AST 产物 md5 diff；全图边集合 diff | AST 逐字节相同；全图差异 ≤ 既有不确定度 |
| 性能 | `.probe/go2o` 副本冷构建：bench（VmHWM/HeapAlloc/wall）+ CLI `/usr/bin/time -v`（Max RSS / minor faults / 非自愿切换） | 满足 §0 全部硬指标 |
| 冒烟 | 本仓库 `make e2e-fixture` + `codeintel update` 增量 | 通过 |

## 5 明确不做（本轮）与后续立项

- **SSA 阶段分波/按需构建**：需要先把跨包信息（returns/summary/dispatch
  候选）落盘或按需重建，风险与验证成本是另一个量级。**前置条件**：用
  §2.3 的 heap profile 归因出 SSA 阶段的 live 构成（先量再设计）。
- **图构建不确定性**（Q246 实测：同仓两次全量构建 567 行边差异，全为
  `#tN` / `#tN@line` 槽位名与 `init` 里 `var.*` 目标漂移）：违反 #218
  "输出确定性"目标，对 Agent 是实质缺陷。**建议独立立项**（本设计的
  产物比对只能落在"既有不确定度内"）。
- **包缓存命中率体检**：Q246 观察到 go2o 连续 5 次构建 `hits=0`，疑与
  开发期频繁改 `ssa/*.go`（analyzer 版本 hash 变）有关，未能证明是 bug；
  若稳态仍 0 命中，则 113MB 缓存写入是纯开销。
- **写库侧单写者解耦 / 两阶段导入（中间文件 → drop 索引 → 导入 → 重建）**：
  Q246 实测 flush 尾部 10-27s，占比 12-30%，收益需先证明瓶颈。
- **构建期关外键**：会改变"悬挂边被丢弃"的语义，需配套
  `PRAGMA foreign_key_check` 收尾，风险高于收益。

## 6 影响面与回滚

- **影响面**（`codeintel query impact (Orchestrator).loadPackages`：深度 3
  / 567 节点；调用点 2 个：`orch_build.go:37` 全量、`orch_incremental.go:86`
  增量）：所有适配器的输入契约——AST（codegraph）与 SSA 两个适配器是
  真实消费者，scip/git 不读 `pkgs`。回归策略：契约测试 + 两个适配器的
  产物 diff + 全套测试。
- **回滚**：主改动是**一行**（Mode 加回 `NeedDeps`）；内存上限可用
  `CODEINTEL_MEMLIMIT=` 显式放宽或直接移除该分支。无 schema/数据变更，
  不需要重建索引。

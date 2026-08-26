---
name: codeintel-cli
license: 'MIT'
description: '使用 codeintel 命令行工具：Go 代码库智能索引与字段追溯查询。构建索引（init）、查询符号/字段读写摘要（query fields）、字段追溯（trace-backward/forward）、数据值全链追踪（value-trace）、调用关系（callers/callees/impact）、导出（export）、Web 服务（serve）。当用户要求分析 Go 代码库的字段数据流、追踪字段使用方/产生点、查看函数字段读写、查询调用关系时使用本 skill。'
---

# codeintel CLI 使用指南

codeintel 是 Go 代码库智能索引工具（SSA 字段追溯），输出存 SQLite（`.codeintel/codeintel.db`）。仓库 module：`github.com/schaepher/codeintel`。

## 构建与安装

```bash
# 在 codeintel 仓库内构建
go build -o codeintel ./cmd/codeintel
# 或安装到 GOBIN
make install
```

## 命令速查

```bash
codeintel init --repo <path> [--workers N] # 全量构建索引（Q221：--workers 默认 min(NumCPU,8)——8 核实测冷启动 5m16s→15.6s；小内存机器可 --workers 1；CODEINTEL_GOGC 覆盖 GOGC 默认 40；CODEINTEL_CPU_PROFILE 输出构建期 CPU profile——须有 go.mod；go.work 根目录会提示进模块目录）
codeintel update --repo <path> [--workers N] [--base <dir>]
                                           # 增量更新（R84：git 检测变更文件 → 按包增量分析——
                                           # 只 Load/分析变更文件所在包，依赖包走 export data，
                                           # 其他包复用库中已有索引；go.mod/go.work 变更或 analyzer
                                           # 版本变化自动全量降级；已知语义：SSA 全程序扫描
                                           # （dispatch 注册等）随分析范围收缩，改 impl 包后
                                           # dispatch_to 边少量缺失——reindex 恢复）
                                           # R85 --base <dir>：多 workspace 分层——base 目录的
                                           # .codeintel 为完整索引（只读共享）；变更基准 = base
                                           # HEAD（diff base..当前），base 数据物化到本地（秒级
                                           # 复制非分析，幂等），只分析变更包；新 workspace 首次
                                           # 建索引 = 物化 + 变更包分析（go2o 实测 3.5s vs 全量
                                           # 23s）；base commit 变化自动重新物化
codeintel serve --repo <path> --addr :8096 # 启动图探索 Web 服务（前端 AntV G6，端口默认 :8090；另含数据库 ER 图页 /er.html：表卡片/嵌套双画法、双击表展开其关联线（全图画线开关刷新后自动恢复，Q217）、每条线独立配色、Q218 fk 类型默认线）
codeintel mcp --repo <path>            # stdio MCP server（Q243：query 能力暴露为 tools——symbol/fields/callers/callees/impact/trace/value_trace/context/table/relations/table_path/summary/module_calls；输出复用 --json 契约 docs/json-contract.md；Claude Code 配置 `codeintel mcp --repo <path>` 即接入）
codeintel query <sub> ... --repo <path>    # 查询（见下；默认加 --json 取结构化输出）
codeintel export --repo <path> [--out x.json]  # 导出字段双层索引 JSON（字段→产生者/消费者）
codeintel export relations --repo <path> [--out x.json]  # 导出全库表间关联 JSON（Q160：{"relations": [...]}，与 query relations --all 同源）
codeintel export graph --type value-trace|callees|lifecycle --target <节点> [--format mermaid|dot] [--out file]
                                           # 图导出：value-trace 默认 mermaid（函数分组）、callees 默认 dot、
                                           # lifecycle 生命周期图（[存储]/[观测]/[读]/[写]+条件标注）
codeintel clean --repo <path> --force      # 删除索引（schema 变更后必须 clean + init 重建；默认保留 .codeintel/cache 包级分析缓存——pkg hash + analyzer 分析源码 hash 自校验（Q181/Q183，仅分析逻辑变化失效，CLI/前端等无关改动不触发），分析逻辑/包源码变化自动失效重算；磁盘清理加 --purge-cache）
codeintel reindex --repo <path> [--workers N] # 一步重建索引（FullBuild 清空图数据表 DROP+CREATE 重建，不删库文件；--workers 默认同 init）
codeintel rule add <from> <to> --repo <path>  # 用户连线规则（Q220c/d）：值流验证不了的外键列手动声明连线
                                           #   rule add merchant_id mch_merchant.id   模式规则（所有含 merchant_id 列的表 → mch_merchant.id）
                                           #   rule add table_a.merchant_id mch_merchant.id  显式列对（单对）
                                           #   目标列省略默认 id；输出用箭头（→）；单参形态兼容旧解析；生成关系 type=fk（ER 默认显示）
codeintel rule list [--json] --repo <path>  # 列出规则
codeintel rule remove <id> --repo <path>    # 删除规则
                                           # 规则存 relation_rules 表（数据库），clean/reindex 保留；
                                           # 生效时校验目标表/列真实存在（不存在静默跳过）
codeintel wiki [--repo <path>] [--out docs/wiki] [--yaml wiki.yaml] [--format md|html] [--init]
                                           # 业务 wiki 生成（模块页/表清单/ER 图/实体协作图/
                                           # 命令入口/流程/枚举/包地图；wiki.yaml 是 AI 产出 →
                                           # 人工确认的契约，无配置时纯自动生成）
codeintel wiki --diagram plantuml|mermaid [--max-entries <N>]
                                           # R32/R37/R38：图引擎（默认 plantuml——HTML 渲染
                                           # PNG 嵌入、md 输出文本块；mermaid 浏览器端渲染）；
                                           # 流程页每节/每页入口展开上限（默认 15，超出折叠
                                           # 为清单）；gRPC 服务子页按业务领域分目录
                                           # （<领域>/processes-grpc-<svc>.md/.html）
codeintel wiki --ai [--agent codex|claude|auto] [--with-qa]
                                           # AI 增量补缺：无描述模块/无别名表/无说明列/术语表
                                           # → 批量 prompt（缺口合并一次请求）→ 写回 wiki.yaml
                                           # 标注 # AI 初稿（git diff 可回滚）；增量语义——只补
                                           # 仍缺的，重跑不回退；批次上限 20 条/200 列名、
                                           # 超时 240s（R28：go2o 300 缺口实测收敛）；
                                           # --with-qa 读历史问答（ask/serve 对话收集的
                                           # qa_history）相关条目作参考（W3）
codeintel domains [--repo <path>] [--yaml wiki.yaml] [--agent claude|codex] [--export-facts <file>]
                                           # R34：AI 业务域分析——静态事实包（packages/tables/
                                           # entities/services JSON）导出文件 → agent 读文件归纳
                                           # → 解析校验（归属须在事实包中）→ 写回 wiki.yaml
                                           # domains 区块（# AI 初稿）；超时 360s（R38）；
                                           # 重跑整体替换旧 domains（clearDomains）
codeintel query grpc-routes [--json]       # R29：服务端 gRPC 路由清单（服务名/实现类型
                                           # impl_id/注册点/方法全集含 handler——ServiceDesc 解析）
codeintel query http-routes [--json]       # R31：HTTP 路由清单（method/path/handler/handler_id/
                                           # resolver native|gin/注册点）
codeintel query packages [--json]          # R77：包结构清单（包路径/职责 doc_comment/符号数）
codeintel query architecture [--json]      # R77：模块间调用架构图（mermaid——模块→包→调用边）
codeintel query er [--json]                # R77：ER 图（表间键关联，mermaid，500 边自动降级）
codeintel query processes [--json]         # R77：系统流程（进程/入口调用链，--max-entries）
codeintel query module <包路径> [--json]   # R77：模块详情（包内符号/进出调用）
codeintel query sequence <符号> [--code] [--depth N]
                                           # R81：调用时序图（索引调用链，mermaid）；--code 代码级
                                           # AST 时序（调用/分支/循环/switch 嵌套展开，--depth 默认 3，
                                           # seq.stop_packages 停止包配置）；R84：接口方法入口
                                           # （grpc 服务入口接口——动态入口无方法体）自动具体化到
                                           # 实现方法再解析
codeintel query grpc-callers <符号> [--json]
                                           # R83：调用链最终调用的 grpc 服务（BFS + 接口具体化；
                                           # 结果缓存 ext_chain_cache，索引 commit 变化失效）
codeintel query http-callers <符号> [--json]
                                           # R83：调用链最终调用的 http 接口（同上）
codeintel query ext-chain <符号> [--json]  # R83：外部系统调用链——递归 grpc 服务端方法再查
                                           # grpc/http 直到没有（visited 防环 + 深度 6 + 缓存）
codeintel query cli-routes [--json]        # R35：urfave/cli/v2 命令树（App 字面量/包级 Commands/
                                           # 子命令递归，query cli-routes + wiki 命令清单页）
codeintel query external-deps [--json]     # R36：redis/kafka 外部依赖（redis 方法式+命令式
                                           # 键、kafka producer/consumer topic）
codeintel ask "<问题>" [--symbol X] [--table Y] [--agent codex|claude|auto]
                                           # 项目上下文问答（自动识别问题中的符号/表名并附加
                                           # 查询结果；--agent 默认 auto，~/.codeintel/config.yaml
                                           # 可设默认）；无问题参数进入交互 REPL——多轮追问复用
                                           # 同一会话（resume），回答收集进 qa_history
codeintel before <符号|字段|表>             # 改动影响预判（Q244：callers/impact 或字段读写方
                                           # 或表关联，一次聚合）
codeintel trace <字段|符号|表>              # 数据来龙去脉（Q244：值流全链 + 生命周期主链）
codeintel batch <符号1> <符号2>…            # 批量符号概览（Q244：多输入一次返回）
codeintel query enums [--include-untyped]   # 枚举权威清单（默认只返回有类型枚举）
codeintel query entities [--format mermaid] # 实体协作图 + 设计诊断（高耦合对/循环/上帝对象/
                                           # 游离函数占比——DDD 领域分组见 F2）
codeintel precompute relations --repo <path> # 全量预计算表间关联（进度写 db，查询直接命中
                                           # 缓存；serve 首次请求自动兜底）
codeintel version
```

## query 子命令

| 子命令 | 用途 | 关键参数 |
|---|---|---|
| `symbol <sym>` | 符号详情（含调用者/被调用者） | |
| `fields <func>` | 函数字段读写摘要（direct_read/write + indirect_write） | | `--json` 行带 `origins`（Q161 间接写多来源：调用点/被调函数/候选 origin+置信度） |
| `trace-backward <field> --func <func>` | 字段产生点反向追溯 | `--max-depth N` 默认 8、`--follow-indirect`（Q172：跨函数间接写链——沿 summary_origins 到下游真实写者再反向 data_flows_to） |
| `trace-forward <field> --func <func>` | 字段后续使用正向追踪 | `--max-depth N` |
| `value-trace <nodeID>` | 数据值全链（跨函数，函数上下文分组） | `--max-depth N`、`--min-conf N`（默认 1.0——Q163 候选边默认剪枝，从字段锚点追踪不进入其他接口候选实现；显式 `--min-conf 0` 展开候选并标注 `[动态候选 enum 0.7 接口]` 路径累计标记）、`--include-container`（显式父容器路径扩展） |
| `callers/callees <sym>` | 调用者/被调用者 | `--depth N` 默认 1 |
| `impact <sym>` | 影响分析 | `--depth N` 默认 3 |
| `summary <节点>` | 跨层摘要：入口→计算→写入→消费主链（每步带 file:line） | `--format mermaid` |
| `unused` | 未调用函数与孤立链分析（死代码/流程衔接检查） | `--since <ref>`、`--fail-on unused\|isolated` |
| `path <from> <to>` | 节点间最短路径（数据流/调用断言） | `--kind data\|calls`、`--max-depth N` 默认 50 |
| `table <表名>` | 表级数据流聚合：列虚拟节点 + 写入方函数与行号（Q97 字符串 SQL + GORM 结构体写路径） | 从数据库表反推数据流；`--json` 结构化 |
| `table-path <表A> <表B>` | 表间数据通路（Q241）：A → B 最短跳数（跨 mapping 表），每步表.列 → [类型] → 表.列；类型优先级 fk>query>write>read 最优一条，`--json` 全列候选 | `--max-hops N`（默认 6） |
| `relations <表名>` | 表间关联推断：本表列的值沿数据流链流入其他表列（A.x 读出 → B.y 过滤，无外键依赖）；类型分级 fk（值流验证的外键键关联，ER 图默认线）/query（键关联）/write（同源）/read（间接） | `--json`、`--format mermaid`（列级图，query 粗线）；P0④：`--type fk\|query\|write\|read`（Q218：默认 fk+query+write，read 需显式展开；fk 独立类型，--type=query,write 不含 fk）、`--max-hops N`（输出过滤）、`--max-results N`、`--memory full\|sql`（auto 按规模，>50 万节点自动逐节点 SQL 防爆内存）；Q195/Q196/Q197 降噪参数：`--include-long-query`（query 不限跳数）、`--query-max-hops/--write-max-hops/--read-max-hops N`（三类各自跳数上限，0=不限制，默认 4） |
| `relations --all` | 全库关联单次聚合（Q160）：一次加载内存图 BFS 全部表合并去重（同列对取 hops 最小 + type 最高），AGENT 一次调用拿全库键关联 | 无需表名；`--json` 数组；过滤/降噪参数同单表；结果按 build_id 缓存（relation_candidates，增量 update 后自动失效）；go2o 实测 4.8s |
| `grpc-routes` | R29 服务端 gRPC 路由清单：服务名/实现类型（grpc_impl 边 + implements 追业务实现，R37 断言扫描兜底）/注册点/方法全集（ServiceDesc go/parser 提取，含 handler；手写服务回退节点 methods 属性） | `--json` 契约：`services[].{name,impl,impl_id,impl_file,register,methods[].{name,handler}}` |
| `http-routes` | R31 HTTP 路由清单（两个 resolver 各自识别模式） | `--json` 契约：`routes[].{method,path,handler,handler_id,resolver,register}`；handler_id = handler canonical ID（R37 发射端解析，流程页展开用） |
| `cli-routes` | R35 urfave/cli/v2 命令树 | `--json` 契约：`commands[].{name,usage,action,subcommands[]}` |
| `external-deps` | R36 redis/kafka 外部依赖（redis 方法式+命令式、kafka producer/consumer） | `--json` 契约：redis keys / kafka topics 聚合 |

- **执行约定：查询命令默认加 `--json`**——所有 query 子命令默认附加 `--json` 取结构化输出（AGENT 可直接解析字段/断言 reachable）；仅当结果要直接展示给人看（表格/树形）时才省略。`--compact` 去缩进可与 `--json` 叠加
- **`--since <ref>`**（unused/symbol/fields/callers/callees/impact）：基于 `git diff <ref>` 对本次新增/修改的函数标注 `[new]`/`[mod]`——需求写完检查"本次改动的函数是否接线"
- 日志写入 `.codeintel/codeintel.log`（与 db 同目录），stdout 只留查询结果

- `<sym>` 接受 canonical ID（`symbol:go:<pkg>:<name>`，方法 `(T).m`）或名称（多匹配时报错列出候选）
- `--repo` 接受已注册仓库的短名/路径后缀/module 名（Q238：文件系统
  优先，注册表唯一命中即用，多命中报候选）——`codeintel list` 查看已注册仓库
- `<field>` 是类型限定路径（如 `example.com/app/internal/agent.Config.APIKey`）
- `<nodeID>` 是完整节点 ID（如 `symbol:go:...:(Manager).Run#m.cfg.APIKey.read@47`）

## 真实示例

以下以某 Go 仓库（module `example.com/app`，含 LLM 代理的
`m.cfg.APIKey` 字段）为例。**示例默认带 `--json`**（本 skill 执行约定）；
展示给人看时可去掉：

```bash
# 1. 构建索引（首次或 schema 变更后）
codeintel init --repo <目标仓库>

# 2. 函数字段读写摘要（验证后能看到 [direct_read]/[direct_write]/[indirect_write] 分组）
codeintel query fields "(Manager).Run" --json --repo <目标仓库>

# 3. 字段使用方正向追踪
codeintel query trace-forward example.com/app/internal/agent.Config.APIKey \
  --func "(Manager).Run" --json --repo <目标仓库>

# 4. 数据值全链（跨函数）：先查节点 ID，再追踪
sqlite3 <目标仓库>/.codeintel/codeintel.db \
  "SELECT id FROM nodes WHERE kind='field_access' AND json_extract(properties,'\$.instance_path')='m.cfg.APIKey' LIMIT 1"
codeintel query value-trace "<上面查到的ID>" --json --repo <目标仓库>

# 5. 需求写完检查（流程衔接）：本次新增/改动的函数调用情况
codeintel query unused --since HEAD --json --repo <目标仓库>
#   → [new]/[mod] 函数是否被调用；孤立链 ⚠（流程没衔接）；[无引用]（多余代码）
#   加入 CI：--fail-on unused 存在未调用函数时退出码 1

# 6. 数据流断言："X 的值应到达 Y"——直接判定 reachable
codeintel query path <起点节点ID> <终点节点ID> --json --repo <目标仓库>
#   → {path, length, reachable}；reachable=false 即不可达
#   --kind calls 切换函数调用路径

# 7. 全量冗余检查（死代码）：无 --since 时报告全部未调用函数
codeintel query unused --json --repo <目标仓库>

# 8. 表间关联（键关联优先）：全库聚合，默认滤 write/read 噪音与 >4 跳长链
codeintel query relations --all --json --repo <目标仓库>
#   查看 query 长链（如 10 跳的真实键关联）：
codeintel query relations --all --include-long-query --json --repo <目标仓库>
#   自定义三类跳数上限（0=不限制）：
codeintel query relations --all --query-max-hops 10 --write-max-hops 0 --json --repo <目标仓库>

# 9. 业务 wiki 生成 + AI 增量补缺（wiki.yaml 是人工确认的契约）：
codeintel wiki --repo <目标仓库> --yaml wiki.yaml --out docs/wiki --format html
codeintel wiki --repo <目标仓库> --yaml wiki.yaml --ai --agent claude   # 补缺（写回 yaml 标 # AI 初稿）
codeintel wiki --repo <目标仓库> --yaml wiki.yaml --ai --with-qa        # 参考历史问答补缺
#   增量语义：只补仍缺的（无描述模块/无别名表/无说明列/术语表），重跑不回退已补内容

# 10. 项目上下文问答（单次 / REPL 交互）：
codeintel ask "订单状态流转涉及哪些表？" --repo <目标仓库> --agent claude
codeintel ask                                   # 无参数 → ask> REPL，exit 退出
```

## 输出解读

- **fields 摘要**：`[direct_read]` 读字段、`[direct_write]` 写字段、`[indirect_write]` 经别名/调用闭包间接写（如 `m.mu :55 m.mu.Lock()`）。摘要表按字段 UNIQUE 去重（同一字段多处访问只列首行），明细在图节点里
- **value-trace**：`【函数名】` 分组 + 缩进树，`←` 产生链（反向）、`→` 使用链（正向），边类型（data_flows_to/argument/returns/phi_operand）、`[读]/[写]` 标记与行号
- 读链中间层（如 `m.cfg.APIKey` 的内层 `m.cfg`）标记为 read 而非 write；`[]T{...}` 字面量初始化不产元素节点
- **路径条件**：追溯行可带 `[条件: ...]`（if/类型分支/env，查询期计算）
- **动态派发**：symbol 接口类型展示候选实现（`[register 0.9]`/`[enum 0.7]` + 注册点）——接口视角的候选集不受剪枝影响；value-trace 默认剪枝候选边（Q163：从字段锚点追踪不进入其他接口候选实现，需显式 `--min-conf 0` 才展开并标注 `[动态候选]`）
- **持久化**：SQL 写映射为 `users.name` 虚拟节点（字段→表.列，经 value-trace 可见）
- **relations 降噪**（Q195/Q196/Q197，全部出口统一应用）：① write/read 按 from字段→to表 聚合（全列 INSERT 的列爆炸收敛为字段级，query 保持列级）；② 跳数上限默认 4（三类可分别配置，0=不限制；query 长链默认滤，--include-long-query 查看）；③ Q218 fk 类型默认不限跳（值流已验证——11 跳真实链 item_info.id→item_image.item_id 直接可见，对象字段换名噪声链保持 query）。实测：radar 592→3 条、go2o 41142→54 条
- **ER 图页**（serve 后 `/er.html`，数据源 `/api/er`）：表卡片/嵌套（表外框内嵌字段矩形）双画法切换；默认不连线，**双击表**展开其关联线（隐藏无关表重排，支持多表叠加展开，再双击收起）；线走正交绕障通道不压表矩形、每条线独立配色；页头勾选框按类型过滤（外键关联 fk 默认勾选/键关联/同源写/间接读——Q218 默认只画 fk 真实链，query 噪声需手动开启）；线型：fk 粗实线 / query 长虚线 / write 虚线 / read 点线；信息栏显示统计与展开状态
- **全局溯源**：全局变量跨函数共享节点（`var.<name>`），value-trace 可达初始化表达式
- **跨层摘要**：`query summary <节点>` 输出生命周期主链（entry/compute/write/consume）
- **unused 两档**：`无调用`（calls/passes_result 入边空——流程衔接检查）与 `[无引用]`（+passes_to/dispatch_to/initializes/var 初始化引用也空——真死代码）；main/init 永不报告；exported 标 `[exported]`（可能被外部调用）；孤立链 = 链头无 caller、链内 caller ⊆ 链、有链外 caller 断开、互调环整环孤立；盲区：函数值赋值/外部实参嵌套调用/嵌入提升方法（如 `(DB).Exec`）可能误报
- **path 输出**：路径节点序列（`名称 ← 边类型 :行号`）；`--json` 输出 `{path, length, reachable}`——AGENT 可直接断言 reachable
- **--since 标注**：`[new]`（声明行命中 diff 新增行/新增文件）、`[mod]`（函数行号区间命中新增行）——symbol/fields/callers/callees/impact 输出标注，unused 用它过滤报告范围

## 项目自举（Q236/Q237）：分析本项目自己

用户明确要求：分析/修改/搜索 **codeintel 仓库自身**时优先用 codeintel 命令
（代替 grep/codegraph）——符号定义/签名/调用者/值流/影响面等结构问题：

```bash
# 在仓库内直接跑（--repo 缺省 = 当前工作目录，Q237）
./codeintel query symbol|fields|callers|callees|value-trace|context|impact ...
```

- 本仓库 `.codeintel/` 已 reindex（含最新分析逻辑）；逻辑变更后 `update` 按
  git diff 判断（工作区干净会跳过）——用 `reindex` 确保新逻辑生效
- 改 `assets/web/` 前端后须重新 `go build -o codeintel ./cmd/codeintel`
  （go:embed 打包，旧二进制嵌旧页面；`strings codeintel | grep Q230` 验证）
- grep 仍用于字面文本（字符串/注释/日志）
- 文档/记忆中不硬编码本仓库路径（目录可能被重命名）——用默认 cwd 表述

## 验证与注意事项

- 改动验证矩阵：`make test`（单元）、`make it`（集成，需 scip-go）、`make e2e`（playwright 22 项，端口 8096，用 `E2E_REPO=<仓库>` 指定验证仓库）
- **schema 无自动迁移**（user_version=4）：改 schema 后验证仓库须 `clean --force` + `init` 重建，否则报版本不匹配
- 每次改完并验证完后要 `git push`（用户约定）
- 日志：zap + OTel 写入 `.codeintel/codeintel.log`（main 粗解析 --repo 传入 Setup）；
  --verbose 的 debug 日志也在文件里；stdout 仅查询结果
- 坑：`pkill -f "codeintel-e2e serve"` 会匹配自身命令行自杀（用 `pgrep -x codeintel-e2e` + kill）
- **agent 子进程 cwd = 目标仓库根（R38）**：claude/codex 对 cwd 项目内
  文件 Read 免权限弹窗——domains 事实包（仓库 .codeintel/ 内）才能被
  AI 读取；ask/wiki --ai/domains 全部注入（不用改参数）
- 索引查询无网络依赖；构建需 `go` 与可选的 `scip-go`（缺失时 scip 适配器降级跳过）

# codeintel — Go 代码库智能索引与查询

读懂大型 Go 代码库，不用从头翻源码：一条命令构建索引，之后用自然
方式问「改这个函数会影响谁」「这个字段的数据从哪来」「这两张表
怎么连起来的」。

## 它能给你什么

- **改代码前的影响预判**：`codeintel before <函数|字段|表>`——动一个
  符号前先看调用者、影响面、表关联
- **数据来龙去脉**：`codeintel trace <字段>`——一个字段从哪产生、被
  谁写入/读取，跨函数全链路
- **调用图与影响分析**：callers / callees / impact，支持深度遍历
- **数据库表间通路**：`query table-path <表A> <表B>`——业务数据经哪些
  mapping 表流转；`query relations` 分 fk / 键关联 / 同源写 / 间接读
  三级置信度
- **业务 wiki 生成**：`codeintel wiki`——模块页/流程页/ER 图/命令页
  （Markdown 或单文件 HTML），`--ai` 增量补缺（描述/别名/列说明），
  serve 下 `/wiki/` 直接浏览
- **AI 问答**：`codeintel ask "<问题>"`——自动打包符号/表事实上下文；
  `codeintel domains` 由 AI 归纳业务域初稿写回 `wiki.yaml`
- **给 AI Agent 用**：`codeintel mcp`（stdio MCP server，工具清单
  自描述）或直接 CLI `--json`（契约见 `docs/json-contract.md`）
- **网页图探索**：`codeintel serve` 启动可视化页面，双击展开依赖

## 安装

一行命令（下载预编译二进制，支持 linux/darwin × amd64/arm64）：

```shell
curl -sfL https://github.com/schaepher/codeintel/releases/latest/download/install.sh | sh
```

或用 Go 安装（需要 Go 1.26.2+，见 go.mod）：

```shell
go install github.com/schaepher/codeintel@latest
```

依赖 [scip-go](https://github.com/scip-code/scip-go)（符号索引器，
缺失时构建自动降级但符号精度下降）：

```shell
go install github.com/scip-code/scip-go/cmd/scip-go@latest
```

AI 相关命令（`wiki --ai` / `domains` / `ask`）复用本机已登录的
`claude` 或 `codex` CLI（零密钥管理；`--agent codex|claude|auto` 选择，
默认读 `~/.codeintel/config.yaml`）。

VS Code 插件（Activity Bar 查询视图/调用链/时序图）：

```shell
make install          # 安装 CLI（并初始化/合并全局配置）
make install-plugin   # 打包 VSIX 并安装到 VS Code（--force 覆盖旧版）
```

## 30 秒上手

```shell
codeintel init --repo <你的仓库>      # 1. 构建索引（生成 .codeintel/）
codeintel before main --repo <你的仓库>   # 2. 问第一个问题
codeintel serve --repo <你的仓库>     # 3. 或打开网页探索
```

## 真实示例

**1. 改一个函数前，先看影响面**

```shell
codeintel before "(Manager).Run" --repo <你的仓库>
```

输出：调用者（深度 2）、影响节点（深度 3）——动了它哪些代码会波及，
一目了然。也支持字段（写入方/读取方）和表（关联 + 列访问）。

**2. 一个字段的数据从哪来**

```shell
codeintel trace example.com/app/internal/agent.Config.APIKey --repo <你的仓库>
```

输出：值流全链（跨函数，谁产生、谁写入、谁读取，带行号）+ 生命周期
主链（入口 → 计算 → 写入 → 消费）。

**3. 两张表之间的数据通路**

```shell
codeintel query table-path orders settlement --repo <你的仓库>
```

输出：`orders.xxx → [fk] → order_tab → [query] → settlement`——中间
经过哪些 mapping 表、每步什么关系类型。

## 常用命令速查

全量清单见 `codeintel help`（含每个子命令的参数与默认值）。

| 命令 | 用途 |
|---|---|
| `codeintel init/update/reindex --repo <path>` | 全量构建 / 增量更新 / 重建（均支持 `--progress auto\|plain\|none`；`update --base <dir>` 分层复用 base 索引） |
| `codeintel config ignore-dir add <目录>` | 仓库级忽略目录（写 `<repo>/codeintel.yaml`，init/update/reindex 自动应用） |
| `codeintel config default\|merge` | 全局配置模板 / 补齐缺失项（`~/.codeintel/config.yaml`） |
| `codeintel query symbol <符号>` | 符号详情（签名、行号、调用者/被调用者） |
| `codeintel query callers/callees <符号>` | 调用者 / 被调用者（`--depth N`） |
| `codeintel query impact <符号>` | 影响分析（`--depth N`） |
| `codeintel query fields <函数>` | 函数字段读写摘要 |
| `codeintel query trace-backward/forward <字段> --func <函数>` | 字段产生点 / 使用点追溯 |
| `codeintel query value-trace <节点>` | 数据值全链（含源码片段；`--format tree\|mermaid`） |
| `codeintel query context <符号>` | 一次拿全链上下文（调用者/字段/链/派发） |
| `codeintel query path <A> <B>` | 两符号间最短路径（`--format calls` 切到调用边集） |
| `codeintel query table <表>` / `relations <表>` | 表列数据流 / 表间关联（`--all` 全库） |
| `codeintel query table-path <A> <B>` | 表间数据通路 |
| `codeintel query enums/entities/helpers` | 枚举权威值 / 实体协作图+设计诊断 / 工具函数清单 |
| `codeintel query grpc-routes/http-routes/cli-routes` | 路由与命令入口清单 |
| `codeintel query external-deps/external-interfaces/kafka-topics` | redis/kafka 外部依赖、外部接口调用识别 |
| `codeintel query grpc-callers/http-callers/ext-chain <符号>` | 调用链最终触及的外部系统（`ext-chain` 递归） |
| `codeintel query sequence <符号>` | 时序图（默认 depth 2）；`--code` 源码级（分支/循环，默认 depth 1，`--format mermaid\|plantuml`） |
| `codeintel query packages/architecture/er/processes/module` | 包结构 / 架构图 / ER 图 / 系统流程 / 模块详情（wiki 数据源） |
| `codeintel query unused [--since <ref>]` | 未使用符号 / 孤立链（`--fail-on` 供 CI） |
| `codeintel before/trace/batch <目标…>` | 意图命令：影响预判 / 数据来龙去脉 / 批量符号概览 |
| `codeintel precompute relations` | 全量预计算表间关联（serve 首次请求也会自动兜底） |
| `codeintel rule add\|list\|remove` | 表间关联人工规则（`merchant_id → mch_merchant.id` 这类无值流验证的外键形态） |
| `codeintel serve --repo <path>` | 网页图探索 + wiki 网页版（`/wiki/`，默认 `:8090`） |
| `codeintel mcp --repo <path>` | MCP server（AI Agent 直接调用） |
| `codeintel ask "<问题>"` | 项目上下文问答（无参数进入多轮 REPL） |
| `codeintel wiki [--yaml wiki.yaml]` | 生成业务 wiki（`--format md\|html`、`--ai`、`--with-qa`、`--diagram`） |
| `codeintel domains [--yaml wiki.yaml]` | AI 归纳业务域初稿写回 `wiki.yaml` |
| `codeintel list` / `codeintel workspace init` | 已注册仓库台账 / 按需创建 git worktree |
| `codeintel export graph --type <类型>` | 导出 mermaid/dot 图（callees / value-trace / lifecycle / modules） |
| `codeintel clean --repo <path>` | 破坏性清空 `.codeintel/`（**含人工规则/问答/时序图**；`--force` 不确认；默认保留包级缓存，`--purge-cache` 连缓存一起删） |

全部查询支持 `--json`（结构化输出，契约见 `docs/json-contract.md`）与
`--repo` 短名（已注册仓库可用短名/路径后缀/module 名）；查询前检测
到索引过期会打印提示（MCP 场景在结果前加 `[stale]` 标注）。

## 常见问题

- **改了代码查询还是旧数据**：`codeintel update --repo <path>` 增量
  更新（git 检测变更文件）；分析逻辑升级后包级缓存自动失效并重新
  分析（无需手动清缓存）
- **`schema mismatch`**：列变更需重建——`codeintel reindex --repo <path>`
  会自动在事务中重置派生图表（**保留 relation_rules 连线规则与
  qa_history 问答**，失败回滚）；新增表/索引属于加法演进，打开时
  自动补建，不需要重建
- **日志在哪**：`.codeintel/codeintel.log`（stdout 只留查询结果）
- **配置在哪**：全局 `~/.codeintel/config.yaml`（`codeintel config
  default` 出模板、`config merge` 补缺失项；配 agent / plantuml_jar /
  seq / helpers / ai 开关）；仓库级 `<repo>/codeintel.yaml`（忽略目录）
  与 `<repo>/wiki.yaml`（wiki 业务描述/别名/domains/tables）
- **go.work 仓库**：请进入具体模块目录后 `init`
- **AI 命令没反应/报 agent 缺失**：装 `claude` 或 `codex` CLI，或用
  `--agent` 指定；也可在配置里 `ai: {domains: off, fill: off, ask: off}`
  关掉对应使用点
- **查询结果带 `[stale]` 标注**：索引已过期（MCP 工具场景），先 update；
  CLI 场景是 stderr 的 warning 提示

## 开发者信息

- 面向 AI 代理的开发指南：[`AGENTS.md`](AGENTS.md)（含强制流程）
- 设计文档：[`docs/TD.md`](docs/TD.md)（v2.0）、数据模型总表
  [`docs/data-model.md`](docs/data-model.md) 与逐 Q 实现记录
  [`docs/field_trace.md`](docs/field_trace.md)
- 排障方法论（事后树）与防错设计（事前树）：
  [`docs/troubleshooting-tree.md`](docs/troubleshooting-tree.md) /
  [`docs/prevention-tree.md`](docs/prevention-tree.md)

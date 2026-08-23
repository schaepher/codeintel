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
  mapping 表流转
- **网页图探索**：`codeintel serve` 启动可视化页面，双击展开依赖

## 安装

一行命令（下载预编译二进制，支持 linux/darwin × amd64/arm64）：

```shell
curl -sfL https://github.com/schaepher/codeintel/releases/latest/download/install.sh | sh
```

或用 Go 安装：

```shell
go install github.com/schaepher/codeintel@latest
```

依赖 [scip-go](https://github.com/scip-code/scip-go)（符号索引器，
缺失时构建自动降级但符号精度下降）：

```shell
go install github.com/scip-code/scip-go/cmd/scip-go@latest
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

| 命令 | 用途 |
|---|---|
| `codeintel init/update/reindex --repo <path>` | 全量构建 / 增量更新 / 重建 |
| `codeintel query symbol <符号>` | 符号详情（签名、行号、调用者/被调用者） |
| `codeintel query callers/callees <符号>` | 调用者 / 被调用者（--depth N） |
| `codeintel query impact <符号>` | 影响分析（--depth N） |
| `codeintel query fields <函数>` | 函数字段读写摘要 |
| `codeintel query trace-backward/forward <字段> --func <函数>` | 字段产生点 / 使用点追溯 |
| `codeintel query table <表>` / `relations <表>` | 表列数据流 / 表间关联 |
| `codeintel query table-path <A> <B>` | 表间数据通路 |
| `codeintel batch <符号1> <符号2> …` | 批量符号概览 |
| `codeintel serve --repo <path>` | 网页图探索 + wiki 网页版（/wiki/，默认 :8090） |
| `codeintel mcp --repo <path>` | MCP server（AI Agent 直接调用） |
| `codeintel list` | 已注册仓库台账（init 后自动注册） |
| `codeintel wiki [--yaml wiki.yaml]` | 生成业务 wiki（Markdown/单文件 HTML，docs/wiki/；serve 下 /wiki/ 直接浏览） |

全部查询支持 `--json`（结构化输出，契约见 docs/json-contract.md）与
`--repo` 短名（已注册仓库可用短名/路径后缀/module 名）。

## 常见问题

- **改了代码查询还是旧数据**：`codeintel update --repo <path>` 增量
  更新（git 检测变更文件）；分析逻辑升级后自动全量重建
- **`schema version mismatch`**：列变更需重建——`codeintel reindex
  --repo <path>`（删旧库 + 全量构建）
- **日志在哪**：`.codeintel/codeintel.log`（stdout 只留查询结果）
- **go.work 仓库**：请进入具体模块目录后 `init`
- **查询结果带 `[stale]` 标注**：索引已过期（MCP 工具场景），先 update

## 开发者信息

- 面向 AI 代理的开发指南：[`AGENTS.md`](AGENTS.md)（含强制流程）
- 设计文档：[`docs/TD.md`](docs/TD.md)（v2.0）与逐 Q 实现记录
  [`docs/field_trace.md`](docs/field_trace.md)
- 排障方法论（事后树）与防错设计（事前树）：
  [`docs/troubleshooting-tree.md`](docs/troubleshooting-tree.md) /
  [`docs/prevention-tree.md`](docs/prevention-tree.md)

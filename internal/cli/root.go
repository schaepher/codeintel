// Package cli 实现 codeintel 命令行：init（全量构建）/ query（查询）/ clean（清理）。
package cli

import (
	"context"
	"fmt"
	"go.uber.org/zap"
	"os"
)

// Main 是 CLI 入口（cmd/codeintel 调用）。ctx 携带 root span（链路追踪）。
func Main(ctx context.Context, args []string) int {
	logger := zap.L()
	logger.Debug("enter Main")
	defer logger.Debug("exit Main")
	if len(args) < 1 {
		usage()
		return 2
	}
	switch args[0] {
	case "init":
		return cmdInit(ctx, args[1:])
	case "update":
		return cmdUpdate(ctx, args[1:])
	case "reindex":
		return cmdReindex(ctx, args[1:])
	case "query":
		return cmdQuery(args[1:])
	case "export":
		return cmdExport(args[1:])
	case "serve":
		return cmdServe(ctx, args[1:])
	case "clean":
		return cmdClean(args[1:])
	case "rule":
		return cmdRule(args[1:])
	case "list":
		return cmdList(args[1:])
	case "workspace":
		return cmdWorkspace(args[1:])
	case "precompute":
		return cmdPrecompute(args[1:])
	case "mcp":
		return cmdMCP(args[1:])
	case "before":
		return cmdBefore(args[1:])
	case "trace":
		return cmdTrace(args[1:])
	case "batch":
		return cmdBatch(args[1:])
	case "wiki":
		return cmdWiki(args[1:])
	case "domains":
		return cmdDomainsArgs(args[1:])
	case "ask":
		return cmdAsk(args[1:])
	case "config":
		return cmdConfig(args[1:])
	case "version", "--version", "-v":
		return cmdVersion(args[1:])
	case "help", "-h", "--help":
		usage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", args[0])
		usage()
		return 2
	}
}

func usage() {
	logger := zap.L()
	logger.Debug("enter usage")
	defer logger.Debug("exit usage")
	fmt.Fprint(os.Stderr, usageText)
}

const usageText = `codeintel - Go 代码库智能索引与查询（MVP）

用法:
  （--repo <path> 缺省 = 当前工作目录，Q237；也接受已注册仓库的短名/
  路径后缀/module 名，Q238——codeintel list 查看已注册仓库）
  codeintel list [--stale|--unbuilt|--worktree-of|--workspace|--module]
                                  全局注册台账（~/.codeintel：init 后自动注册）
  codeintel workspace init --dir <目录> [--repo <子集>] [--build] [--branch <b>]
                                  把已注册仓库创建 git worktree 到 workspace
                                  （幂等；默认 detached 不构建）
  codeintel workspace prune       清理目录已消失的注册条目
  codeintel init --repo <path>     全量构建索引（生成 .codeintel/codeintel.db）
  codeintel update --repo <path> [--base <dir>]
                                  增量更新（git 检测变更文件，按包分析；
                                  --base 多 workspace 分层：base 目录索引
                                  物化到本地，只分析 diff(base..当前) 的包）
  codeintel reindex --repo <path> 全量重建索引（清空 + 重新分析）
  codeintel clean --repo <path>    删除仓库的索引数据库
  codeintel rule <命令>            表间关联人工规则（list/add/rm——merchant_id
                                  等外键形态列无值流验证时人工连线）
  codeintel serve --repo <path>    启动图探索 Web 服务（AntV G6 前端，--addr 默认 :8090）
  codeintel mcp --repo <path>      stdio MCP server（Q243：query 能力暴露为
                                  tools/list + tools/call，Agent 直接调用）
  codeintel before <符号|字段|表>   改动影响预判（Q244：callers/impact 或
                                  字段读写方或表关联，一次聚合）
  codeintel trace <字段|符号|表>    数据来龙去脉（Q244：值流全链 + 生命周期
                                  主链）
  codeintel batch <符号1> <符号2>… 批量符号概览（Q244：多输入一次返回）
  codeintel query <symbol|name>    查询符号详情（含调用者/被调用者）
  codeintel query callers <sym>    查询调用者（--depth N，默认 1，置信度阈值 0.8）
  codeintel query callees <sym>    查询被调用者（--depth N，默认 1）
  codeintel query impact <sym>     影响分析（--depth N，默认 3）
  codeintel query fields <func>    字段读写摘要（direct_read/write + indirect_write）
  codeintel query trace-backward <field> --func <func>
                                  字段产生点反向追溯（--max-depth N，默认 8）
  codeintel query trace-forward <field> --func <func>
                                  字段后续使用正向追踪
  codeintel export [--out json]   导出双层索引 JSON（字段 → 产生者/消费者）
  codeintel clean --repo <path>    删除仓库的索引数据库
  codeintel query table-path <A> <B> [--max-hops N] [--json]
                                  表 A → 表 B 数据通路（跨 mapping 表，
                                  每步 表.列 → [类型] → 表.列）
  codeintel query enums [--include-untyped] [--repo <path>]
                                  枚举权威清单（默认只返回有类型枚举）
  codeintel query entities [--format mermaid] [--json]
                                  实体协作图 + 设计诊断（高耦合/循环/
                                  上帝对象/游离函数占比）
  codeintel query helpers [--min-packages N] [--json]
                                  工具函数清单（游离函数且被 ≥N 个包调用，
                                  N 默认 3——config.yaml helpers.min_packages）
  codeintel query grpc-routes     服务端 gRPC 路由清单（服务/实现/方法）
  codeintel query http-routes     HTTP 路由清单（method/path/handler）
  codeintel query cli-routes      urfave/cli 命令树
  codeintel query external-deps   redis/kafka 外部依赖（方法式+命令式键）
  codeintel query external-interfaces
                                  外部系统接口调用（grpc/http 接口未在本项目）
  codeintel query kafka-topics    kafka topic 生产/消费归属
  codeintel query grpc-composites 完整包含 grpc server 接口的组合接口
  codeintel query grpc-callers <sym> [--json]
                                  调用链最终调用的 grpc 服务（缓存优先）
  codeintel query http-callers <sym> [--json]
                                  调用链最终调用的 http 接口
  codeintel query ext-chain <sym> [--json]
                                  外部系统调用链（递归 grpc 服务端方法
                                  再查 grpc/http 直到没有）
  codeintel query sequence <sym> [--code] [--depth N] [--format mermaid]
                                  调用时序图；--code 代码级（AST 分支/循环/
                                  switch 嵌套展开，默认 depth 3）
  codeintel query packages [--json]
                                  包结构清单（包路径/职责/符号数）
  codeintel query architecture [--json]
                                  模块间调用架构图（mermaid）
  codeintel query er [--json]     ER 图（表间键关联，mermaid，500 边降级）
  codeintel query processes [--json] [--max-entries N]
                                  系统流程（进程/入口调用链）
  codeintel query module <包路径> [--json]
                                  模块详情（包内符号/进出调用）
  codeintel query module-calls    模块间调用清单
  codeintel query value-trace <字段> [--max-depth N]
                                  数据值全链追踪
  codeintel query summary <sym> [--format mermaid]
                                  函数摘要（SSA 字段追溯聚合）
  codeintel query context <sym>   符号上下文（入口/调用者/实现）
  codeintel query unused [--since <ref>] [--fail-on unused|isolated]
                                  未使用符号（CI 可失败）
  codeintel query path <A> <B>    两符号间路径
  codeintel query table <表名>    表列数据流
  codeintel query relations <表名> [--all] [--type fk|query|write|read]
                                  [--max-hops N] [--memory full|sql]
                                  表间关联（--all 全库聚合；fk=值流验证
                                  的真实键/query=WHERE 键关联）
  codeintel query table-path <A> <B> [--max-hops N]
                                  表 A → 表 B 数据通路
  codeintel precompute relations --repo <path>
                                  全量预计算表间关联（进度写 db，查询
                                  直接命中缓存；serve 首次请求自动兜底）
  codeintel ask "<问题>" [--agent codex|claude|auto] [--symbol X] [--table Y]
                                  项目上下文问答（自动识别问题中的符号/
                                  表名并附加查询结果；--agent 默认 auto，
                                  ~/.codeintel/config.yaml 可设默认；
                                  无问题参数进入交互模式——多轮追问
                                  复用同一会话）
  codeintel wiki [--repo <path>] [--out <dir=docs/wiki>] [--yaml <file>]
                 [--format md|html] [--diagram plantuml|mermaid]
                 [--ai] [--agent codex|claude|auto] [--with-qa] [--max-entries N]
                                  生成业务 wiki（wiki.yaml 补充业务描述/
                                  别名/隐藏符号；--ai 增量补缺写回标注
                                  # AI 初稿；plantuml 渲染 PNG 嵌入）
  codeintel domains [--repo <path>] [--yaml wiki.yaml] [--agent claude|codex]
                 [--prompt "<用户约束>"] [--export-facts <file>]
                                  业务域分析（静态事实包 → AI 归纳 →
                                  wiki.yaml domains 初稿 + subdomains）
  codeintel config default        输出默认全局配置（~/.codeintel/
                                  config.yaml 模板——Makefile install
                                  首次安装自动写入）
  codeintel version                输出编译时的 commit hash

符号可用 canonical ID（symbol:go:<pkg>:<name>）或名称精确/模糊查找。

任意位置加 --verbose（或 --debug）输出 debug 级日志（默认 info 级）。
`

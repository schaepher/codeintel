package cli

import (
	"fmt"
	"os"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
	"github.com/schaepher/codeintel/internal/logging"
)

// outputOpts 查询输出选项。
type outputOpts struct {
	json     bool   // 结构化 JSON 输出（stdout 仅 JSON，日志已切文件）
	compact  bool   // 树形/表格输出压缩为紧凑形式
	repoPath string // 目标仓库根（Q235-10：value-trace 源码片段读取）
}

// cmdQuery 实现 codeintel query。
func cmdQuery(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "error: query 需要一个子命令（symbol/fields/trace/value-trace/summary/path/unused/callers/callees/impact/sequence/enums/entities/grpc-routes/http-routes/cli-routes/external-*/kafka-topics/grpc-callers/http-callers/ext-chain/packages/architecture/er/processes/module）")
		return 2
	}
	sub := args[0]
	rest := args[1:]

	f := parseQueryFlags(rest)
	target := ""

	if sub != "unused" && sub != "module-calls" && sub != "enums" && sub != "entities" && sub != "grpc-routes" && sub != "http-routes" && sub != "cli-routes" && sub != "external-deps" && sub != "external-interfaces" && sub != "kafka-topics" && sub != "grpc-composites" && sub != "grpc-callers" && sub != "http-callers" && sub != "ext-chain" && sub != "packages" && sub != "architecture" && sub != "er" && sub != "processes" && sub != "helpers" && !(sub == "relations" && f.all) {
		if len(f.positional) < 1 {
			fmt.Fprintf(os.Stderr, "error: 缺少符号参数\n")
			return 2
		}
		target = f.positional[0]
	}

	abs, _, err := resolveRepo(f.repoPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		if f.repoPath == "." { // Q238：缺省 cwd 非仓库时附引导（Q13）
			printRepoHint()
		}
		return 1
	}

	if err := logging.ToFile(abs); err != nil {
		fmt.Fprintf(os.Stderr, "warning: 日志切换失败: %v\n", err)
	}
	db, err := sqlite.Open(abs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer db.Close()
	acts := action.New(sqlite.NewRepo(db))

	if tip := staleInfo(abs, sqlite.NewRepo(db)); tip != "" {
		fmt.Fprintf(os.Stderr, "warning: %s\n", tip)
	}

	// R5：枚举权威清单（源码提取）
	if sub == "enums" {
		return cmdEnums(abs, f)
	}
	// R9：实体协作图 + 设计诊断（不依赖符号参数）
	if sub == "entities" {
		return cmdEntities(acts, outputOpts{json: f.json, compact: f.compact, repoPath: f.repoPath}, f.format)
	}
	// R29：gRPC 路由清单
	if sub == "grpc-routes" {
		return cmdGrpcRoutes(acts, abs, f)
	}
	if sub == "http-routes" {
		return cmdHTTPRoutes(acts, f)
	}
	if sub == "cli-routes" {
		return cmdCLIRoutes(abs, f)
	}
	// R36：外部依赖（redis/kafka）
	if sub == "external-deps" {
		return cmdExternalDeps(acts, f)
	}
	// R45：外部系统接口调用识别
	if sub == "external-interfaces" {
		return cmdExternalInterfaces(acts, f)
	}
	// R46：kafka topic 生产/消费归属
	if sub == "kafka-topics" {
		return cmdKafkaTopics(acts, f)
	}
	// R49：完整包含 grpc server 接口的组合接口
	if sub == "grpc-composites" {
		return cmdGrpcComposites(abs, f)
	}
	// R88：工具函数清单（游离函数 + 跨包使用数 ≥N）
	if sub == "helpers" {
		return cmdQueryHelpers(sqlite.NewRepo(db), f.minPkgs, f.json)
	}
	// R83：grpc/http 调用链 + 外部系统调用链（递归）——R95 查询逻辑
	// 迁 action（Actions.ChainGrpcHTTP/ExtChain）
	if sub == "grpc-callers" || sub == "http-callers" || sub == "ext-chain" {
		if len(f.positional) < 1 {
			fmt.Fprintln(os.Stderr, "error: 缺少符号参数")
			return 2
		}
		if sub == "ext-chain" {
			return cmdExtChain(acts, abs, f.positional[0], f.json)
		}
		return cmdChainGrpcHTTP(acts, f.positional[0], sub, f.json)
	}

	opts := outputOpts{json: f.json, compact: f.compact, repoPath: f.repoPath}
	if code, done := dispatchWikiSub(sub, acts, db, abs, f, opts); done {
		return code
	}
	// --since：symbol/fields 等标注 [new]/[mod]
	var since *domain.SinceInfo
	if f.since != "" {
		since = runGitDiffSince(abs, f.since)
	}
	switch sub {
	case "symbol":
		return querySymbol(acts, target, opts, since)
	case "fields":
		return queryFields(acts, target, opts, since)
	case "trace-backward", "trace-forward":
		return queryTraceDir(acts, target, f.funcPath, f.maxDepth, sub == "trace-forward", f.followIndirect, opts)
	case "value-trace":

		mc := f.minConf
		if !f.minConfSet {
			mc = 1.0
		}
		return queryValueTrace(acts, target, f.maxDepth, mc, f.includeContainer, opts, f.format)
	case "summary":
		return querySummary(acts, target, opts, f.format)
	case "context":
		return queryContext(acts, target, opts)
	case "unused":
		return queryUnused(acts, abs, f)
	case "module-calls":
		module := ""
		if len(f.positional) >= 1 {
			module = f.positional[0]
		}
		return queryModuleCalls(acts, module, opts)
	case "table":
		return queryTable(acts, target, opts)
	case "relations":
		if f.all {
			return queryRelationsAll(acts, f.format, opts, &f)
		}
		return queryRelations(acts, target, f.format, opts, &f)
	case "path":
		return queryPath(acts, f.positional[0], f.positional[1], f)
	case "table-path":
		return queryTablePath(acts, f.positional, f.json, f.full)
	case "sequence":
		// R76：时序图；R81：--code 代码级时序（--depth 嵌套层级）
		if f.code {
			return cmdQuerySequenceCode(acts, abs, target, f.depth, f.format == "mermaid", f.json)
		}
		return cmdQuerySequence(acts, target, f.depth, f.format == "mermaid", f.json)
	case "callers", "callees", "impact":
		d := f.depth
		if d <= 0 {
			switch sub {
			case "impact":
				d = 3
			default:
				d = 1
			}
		}
		return queryGraph(acts, sub, target, d, opts, since)
	default:
		fmt.Fprintf(os.Stderr, "error: unknown query subcommand %q\n", sub)
		return 2
	}
}

// parseQueryFlags 手动解析 query 参数（flags 与位置参数任意顺序）。

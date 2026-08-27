package cli

// Q243 MCP：`codeintel mcp [--repo <path>]`——stdio MCP server
// （github.com/modelcontextprotocol/go-sdk，协议由 SDK 实现），把 query
// 能力暴露为 tools（Agent 直接调用；输出复用 --json 契约，
// docs/json-contract.md）。工具 = 参数结构体（json tag 即 inputSchema
// 字段名）+ handler（闭包捕获 Actions）。

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// 工具参数结构体（json tag = inputSchema 字段名，snake_case）。

// #228 写操作工具参数：batch_symbols 批量概览；update/init 重建索引
// （stale 自愈——Agent 一条消息从「过期」到「可用」）。

// #229 file:line 定位参数。

func (p symbolParams) getRepo() string      { return p.Repo }
func (p fieldsParams) getRepo() string      { return p.Repo }
func (p graphParams) getRepo() string       { return p.Repo }
func (p traceParams) getRepo() string       { return p.Repo }
func (p valueTraceParams) getRepo() string  { return p.Repo }
func (p contextParams) getRepo() string     { return p.Repo }
func (p tableParams) getRepo() string       { return p.Repo }
func (p relationsParams) getRepo() string   { return p.Repo }
func (p tablePathParams) getRepo() string   { return p.Repo }
func (p summaryParams) getRepo() string     { return p.Repo }
func (p moduleCallsParams) getRepo() string { return p.Repo }
func (p batchParams) getRepo() string       { return p.Repo }
func (p fileSymbolsParams) getRepo() string { return p.Repo }
func (p updateParams) getRepo() string      { return p.Repo }

// #232 多仓库：mcpEnv 持有默认仓库 + 已解析仓库缓存（ref → 条目）。

// registerMCPTools 注册全部工具（handler 闭包捕获 env；staleWrap 追加
// [stale] 标注——Q243 新鲜度显式化；mcpRepo 支持 #232 多仓库）。
func registerMCPTools(server *mcp.Server, env *mcpEnv, r *sqlite.Repo, repoAbs string) {
	registerQueryTools(server, env, r, repoAbs)
	registerTableTools(server, env, r, repoAbs)
	registerAdminTools(server, env, r, repoAbs)
	registerWikiTools(server, env, r, repoAbs) // R77：wiki 特性工具（packages/architecture/er/processes/module）
}

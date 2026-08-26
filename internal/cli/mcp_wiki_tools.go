package cli

// R77 MCP 工具组：wiki 特性转命令后补齐的工具——packages（包结构）/
// architecture（架构图）/ er（ER 图）/ processes（系统流程）/
// module（模块详情）。数据函数与 query 命令共用（wiki 渲染同源）。

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// 参数结构体（json tag = inputSchema 字段名）。
type packagesParams struct {
	Repo string `json:"repo,omitempty"` // #232 多仓库：空=默认仓库
}
type architectureParams struct {
	Repo string `json:"repo,omitempty"` // #232 多仓库：空=默认仓库
}
type erParams struct {
	Repo string `json:"repo,omitempty"` // #232 多仓库：空=默认仓库
}
type processesParams struct {
	Repo       string `json:"repo,omitempty"`        // #232 多仓库：空=默认仓库
	MaxEntries int    `json:"max_entries,omitempty"` // 每节入口展开上限（0 = 默认 15）
}
type moduleParams struct {
	Repo string `json:"repo,omitempty"` // #232 多仓库：空=默认仓库
	Name string `json:"name"`           // 模块名（完整路径或短名）
}

func (p packagesParams) getRepo() string     { return p.Repo }
func (p architectureParams) getRepo() string { return p.Repo }
func (p erParams) getRepo() string           { return p.Repo }
func (p processesParams) getRepo() string    { return p.Repo }
func (p moduleParams) getRepo() string       { return p.Repo }

// registerWikiTools 注册 wiki 特性工具（R77——与 query 命令共用数据
// 函数；repo 数据用默认仓库（同 grpc_routes/http_routes 模式））。
func registerWikiTools(server *mcp.Server, env *mcpEnv, r *sqlite.Repo, repoAbs string) {
	mcp.AddTool(server, &mcp.Tool{Name: "packages", Description: "包结构（包路径 + doc_comment + 无说明时包内结构体/方法/函数清单）——了解包职责"},
		staleWrap(r, repoAbs, mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args packagesParams) (*mcp.CallToolResult, []action.PkgInfo, error) {
			out, err := a.PackagesData()
			if err != nil {
				return toolErr(err.Error()), nil, nil
			}
			return toolJSON(out), out, nil
		})))
	mcp.AddTool(server, &mcp.Tool{Name: "architecture", Description: "整体架构图（三层架构：接入层→领域层→存储层，domains 配置时领域聚合 + 外部接口节点）——返回 mermaid 文本 + 结构摘要"},
		staleWrap(r, repoAbs, mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args architectureParams) (*mcp.CallToolResult, architectureOut, error) {
			data, err := wikiDataFor(a, repoAbs)
			if err != nil {
				return toolErr(err.Error()), architectureOut{}, nil
			}
			out := architectureData(a, r, data, loadWikiCfg(repoAbs, ""), false)
			return toolJSON(out), out, nil
		})))
	mcp.AddTool(server, &mcp.Tool{Name: "er", Description: "ER 图（表间直接键关联 fk/query 的 erDiagram + 关系明细）——了解表间真实键关联"},
		staleWrap(r, repoAbs, mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args erParams) (*mcp.CallToolResult, erOut, error) {
			out := erData(a, loadWikiCfg(repoAbs, ""), false)
			return toolJSON(out), out, nil
		})))
	mcp.AddTool(server, &mcp.Tool{Name: "processes", Description: "系统流程（main 入口 + HTTP 路由入口 + gRPC 服务方法入口——全部入口聚合 + 调用链，接口具体化）"},
		staleWrap(r, repoAbs, mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args processesParams) (*mcp.CallToolResult, processesOut, error) {
			data, err := wikiDataFor(a, repoAbs)
			if err != nil {
				return toolErr(err.Error()), processesOut{}, nil
			}
			out := processesData(a, r, data, loadWikiCfg(repoAbs, ""), repoAbs, args.MaxEntries)
			return toolJSON(out), out, nil
		})))
	mcp.AddTool(server, &mcp.Tool{Name: "module", Description: "模块详情（职责/入口/核心符号/关键数据流/模块间调用/相关表/包间调用）"},
		staleWrap(r, repoAbs, mcpRepo(env, func(a *action.Actions, ctx context.Context, req *mcp.CallToolRequest, args moduleParams) (*mcp.CallToolResult, *moduleOut, error) {
			if args.Name == "" {
				return toolErr("缺少 name（模块名）"), nil, nil
			}
			data, err := wikiDataFor(a, repoAbs)
			if err != nil {
				return toolErr(err.Error()), nil, nil
			}
			out := moduleData(a, data, args.Name)
			if out == nil {
				return toolErr("模块不存在：" + args.Name), nil, nil
			}
			return toolJSON(out), out, nil
		})))
}

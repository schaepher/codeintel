package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
	"github.com/schaepher/codeintel/internal/logging"
	"go.uber.org/zap"
)

// mcpServer 组装 MCP server（工具注册；#232 多仓库 env）。
func mcpServer(acts *action.Actions, r *sqlite.Repo, repoAbs string) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "codeintel", Version: "0.1.0"}, nil)
	env := &mcpEnv{defaultActs: acts, cache: map[string]mcpRepoEntry{}}
	registerMCPTools(server, env, r, repoAbs)
	return server
}

// cmdMCP 实现 `codeintel mcp`（stdio MCP server）。
func cmdMCP(args []string) int {
	logger := zap.L()
	logger.Debug("enter cmdMCP")
	defer logger.Debug("exit cmdMCP")
	repoPath := "."
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--repo" && i+1 < len(args):
			repoPath = ResolveRepoRef(args[i+1])
			i++
		case strings.HasPrefix(a, "--repo="):
			repoPath = ResolveRepoRef(strings.TrimPrefix(a, "--repo="))
		case a == "--help" || a == "-h":
			fmt.Println("用法: codeintel mcp [--repo <path>]\n  stdio MCP server（tools/list + tools/call 暴露 query 能力）")
			return 0
		default:
			fmt.Fprintf(os.Stderr, "error: 未知参数 %q\n", a)
			return 2
		}
	}
	abs, _, err := resolveRepo(repoPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		if repoPath == "." {
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
	session, err := mcpServer(acts, sqlite.NewRepo(db), abs).Connect(context.Background(), &mcp.StdioTransport{}, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mcp: %v\n", err)
		return 1
	}
	session.Wait()
	return 0
}

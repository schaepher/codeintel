package cli

// Q243 MCP：`codeintel mcp` stdio server（go-sdk）——tools/list +
// tools/call 暴露 query 能力（Agent 直接调用，输出复用 --json 契约
// docs/json-contract.md）。测试用内存 transport + SDK client 直连。

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// seedRepoM2 第二个仓库（module example.com/m2，含 main 节点）——#232
// 多仓库跨库查询测试。
func seedRepoM2(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m2\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m2:main", Kind: domain.KindFunction, Name: "main", FilePath: "main.go"},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// mcpDial 起 server（内存 transport）+ client 连接，返回 client session。
func mcpDial(t *testing.T, dir string) *mcp.ClientSession {
	t.Helper()
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	acts := action.New(sqlite.NewRepo(db))

	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	server := mcpServer(acts, sqlite.NewRepo(db), dir)
	srvSession, err := server.Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { srvSession.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil)
	cliSession, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { cliSession.Close() })
	return cliSession
}

// mcpCallTool 调工具并取 text 内容（isError 断言由调用方做）。
func mcpCallTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	text := ""
	if len(res.Content) > 0 {
		if tc, ok := res.Content[0].(*mcp.TextContent); ok {
			text = tc.Text
		}
	}
	return text, res.IsError
}

// TestMCPDial：连接握手（initialize）成功 + 服务端信息。
func TestMCPDial(t *testing.T) {
	cs := mcpDial(t, seedRepo(t))
	if cs == nil {
		t.Fatal("client session 为空")
	}
}

// TestMCPToolsList：tools/list 返回工具注册表（含 inputSchema）。
func TestMCPToolsList(t *testing.T) {
	cs := mcpDial(t, seedRepo(t))
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	names := map[string]bool{}
	for _, tool := range res.Tools {
		names[tool.Name] = true
		if tool.InputSchema == nil {
			t.Errorf("工具 %s 缺 inputSchema", tool.Name)
		}
	}
	for _, want := range []string{"symbol", "fields", "callers", "callees", "impact", "context", "table", "relations", "table_path", "value_trace"} {
		if !names[want] {
			t.Errorf("tools/list 缺 %s（现有: %v）", want, names)
		}
	}
}

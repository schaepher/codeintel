package cli

// Q243 MCP：`codeintel mcp` stdio server（go-sdk）——tools/list +
// tools/call 暴露 query 能力（Agent 直接调用，输出复用 --json 契约
// docs/json-contract.md）。测试用内存 transport + SDK client 直连。

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// TestMCPToolSymbol：tools/call symbol——content text 是契约 JSON。
func TestMCPToolSymbol(t *testing.T) {
	cs := mcpDial(t, seedRepo(t))
	text, isErr := mcpCallTool(t, cs, "symbol", map[string]any{"id": "main"})
	if isErr {
		t.Fatalf("symbol 调用报错: %s", text)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(text), &m); err != nil {
		t.Fatalf("content 应为 JSON: %v\n%s", err, text)
	}
	if m["name"] != "main" || m["id"] != "symbol:go:example.com/m:main" {
		t.Errorf("symbol 结果 = %v", m)
	}
}

// TestMCPToolFields：fields 输出契约字段（function_id/access_kind）。
func TestMCPToolFields(t *testing.T) {
	cs := mcpDial(t, seedFieldTrace(t))
	text, isErr := mcpCallTool(t, cs, "fields", map[string]any{"func": "main"})
	if isErr {
		t.Fatalf("fields 调用报错: %s", text)
	}
	if !strings.Contains(text, `"access_kind"`) || !strings.Contains(text, `"function_id"`) {
		t.Errorf("fields 应输出契约字段（access_kind/function_id）:\n%s", text)
	}
}

// TestMCPUnknownTool：tools/call 未知工具 → SDK 报错。
func TestMCPUnknownTool(t *testing.T) {
	cs := mcpDial(t, seedRepo(t))
	if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "nope_tool"}); err == nil {
		t.Error("未知工具应报错")
	}
}

// TestMCPToolError：工具执行错误 → isError（如符号不存在）。
func TestMCPToolError(t *testing.T) {
	cs := mcpDial(t, seedRepo(t))
	text, isErr := mcpCallTool(t, cs, "symbol", map[string]any{"id": "nope_nope"})
	if !isErr {
		t.Errorf("符号不存在应 isError，text=%s", text)
	}
	if !strings.Contains(text, "不存在") {
		t.Errorf("错误信息应含原因: %s", text)
	}
}

// TestMCPToolBatchSymbols：#228 batch_symbols——多符号一次返回（部分
// 成功：单输入失败跳过），保持输入顺序。
func TestMCPToolBatchSymbols(t *testing.T) {
	dir := seedRepo(t)
	cs := mcpDial(t, dir)
	text, isErr := mcpCallTool(t, cs, "batch_symbols", map[string]any{"symbols": []string{"main", "nope", "(Svc).Run"}})
	if isErr {
		t.Fatalf("batch_symbols 调用报错: %s", text)
	}
	var m struct {
		Results []struct {
			Name string `json:"name"`
			ID   string `json:"id"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(text), &m); err != nil {
		t.Fatalf("content 应为 JSON: %v\n%s", err, text)
	}
	if len(m.Results) != 2 {
		t.Fatalf("部分成功应返回 2 条（nope 跳过），got %d: %s", len(m.Results), text)
	}
	if m.Results[0].Name != "main" || m.Results[1].Name != "(Svc).Run" {
		t.Errorf("应保持输入顺序: %v", m.Results)
	}
}

// TestMCPToolUpdate：#228 update 写工具——无变更仓库 → up_to_date
// （stale 自愈入口：先 update 再查询即无 [stale]）。
func TestMCPToolUpdate(t *testing.T) {
	dir := seedGitRepo(t) // git 仓库（go.mod + main.go，无 .codeintel 数据）
	cs := mcpDial(t, dir)
	text, isErr := mcpCallTool(t, cs, "update", map[string]any{})
	if isErr {
		t.Fatalf("update 调用报错: %s", text)
	}
	var m buildResult
	if err := json.Unmarshal([]byte(text), &m); err != nil {
		t.Fatalf("content 应为 JSON: %v\n%s", err, text)
	}
	if m.Status != "up_to_date" {
		t.Errorf("无变更仓库应 up_to_date, got %s (%s)", m.Status, text)
	}
}

// TestMCPToolInit：#228 init 写工具——迷你仓库全量重建 → success +
// 符号落库（改后查询可见）。
func TestMCPToolInit(t *testing.T) {
	dir := seedGitRepo(t)
	cs := mcpDial(t, dir)
	text, isErr := mcpCallTool(t, cs, "init", map[string]any{})
	if isErr {
		t.Fatalf("init 调用报错: %s", text)
	}
	var m buildResult
	if err := json.Unmarshal([]byte(text), &m); err != nil {
		t.Fatalf("content 应为 JSON: %v\n%s", err, text)
	}
	if m.Status != "success" || m.Nodes < 1 {
		t.Errorf("init 应 success 且符号落库, got %s nodes=%d (%s)", m.Status, m.Nodes, text)
	}
	// 改后查询可见（stale 自愈闭环）
	text2, isErr := mcpCallTool(t, cs, "symbol", map[string]any{"id": "main"})
	if isErr {
		t.Fatalf("init 后 symbol 应可查: %s", text2)
	}
	if !strings.Contains(text2, `"name": "main"`) {
		t.Errorf("symbol 结果应含 main: %s", text2)
	}
}

// TestMCPToolRoots：#229 概览工具——roots 顶层入口（结构正确，可为空）。
func TestMCPToolRoots(t *testing.T) {
	cs := mcpDial(t, seedRepo(t))
	text, isErr := mcpCallTool(t, cs, "roots", map[string]any{})
	if isErr {
		t.Fatalf("roots 调用报错: %s", text)
	}
	var m struct {
		Roots []map[string]any `json:"roots"`
	}
	if err := json.Unmarshal([]byte(text), &m); err != nil {
		t.Fatalf("content 应为 JSON: %v\n%s", err, text)
	}
	if m.Roots == nil {
		t.Error("roots 应为数组（可为空）")
	}
}

// TestMCPToolRepoSummary：#229 概览工具——repo_summary 规模 + 表数 +
// 最新构建。
func TestMCPToolRepoSummary(t *testing.T) {
	cs := mcpDial(t, seedRepo(t))
	text, isErr := mcpCallTool(t, cs, "repo_summary", map[string]any{})
	if isErr {
		t.Fatalf("repo_summary 调用报错: %s", text)
	}
	var m struct {
		Nodes int `json:"nodes"`
		Edges int `json:"edges"`
	}
	if err := json.Unmarshal([]byte(text), &m); err != nil {
		t.Fatalf("content 应为 JSON: %v\n%s", err, text)
	}
	if m.Nodes < 2 || m.Edges < 1 {
		t.Errorf("seedRepo 应有 2 节点 1 边，got nodes=%d edges=%d (%s)", m.Nodes, m.Edges, text)
	}
}

// TestMCPToolFileSymbols：#229 file:line 解析——报错栈定位符号。
func TestMCPToolFileSymbols(t *testing.T) {
	dir := seedGitRepo(t) // git 仓库 + init 后 main.go 有真实行号
	cs := mcpDial(t, dir)
	text, isErr := mcpCallTool(t, cs, "init", map[string]any{})
	if isErr {
		t.Fatalf("init 调用报错: %s", text)
	}
	text, isErr = mcpCallTool(t, cs, "file_symbols", map[string]any{"file": "main.go", "line": 3})
	if isErr {
		t.Fatalf("file_symbols 调用报错: %s", text)
	}
	var m struct {
		Symbols []struct {
			Name string `json:"name"`
			Kind string `json:"kind"`
		} `json:"symbols"`
	}
	if err := json.Unmarshal([]byte(text), &m); err != nil {
		t.Fatalf("content 应为 JSON: %v\n%s", err, text)
	}
	if len(m.Symbols) == 0 {
		t.Fatalf("main.go:3 应命中 main: %s", text)
	}
	if m.Symbols[0].Name != "main" {
		t.Errorf("首个命中应为 main，got %v", m.Symbols)
	}
}

// TestMCPToolMultiRepo：#232 多仓库——repo 参数（路径）解析目标仓库；
// 空 repo 用默认仓库；不存在仓库 isError。
func TestMCPToolMultiRepo(t *testing.T) {
	dir2 := seedRepoM2(t)
	cs := mcpDial(t, seedRepo(t)) // 默认仓库 example.com/m

	// 跨仓库：repo=dir2 → 命中 m2 的 main
	text, isErr := mcpCallTool(t, cs, "symbol", map[string]any{"id": "main", "repo": dir2})
	if isErr {
		t.Fatalf("跨仓库 symbol 调用报错: %s", text)
	}
	var m struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(text), &m); err != nil {
		t.Fatalf("content 应为 JSON: %v\n%s", err, text)
	}
	if !strings.Contains(m.ID, "example.com/m2:main") {
		t.Errorf("应命中 m2 仓库 main，got id=%s", m.ID)
	}

	// 空 repo → 默认仓库
	text, isErr = mcpCallTool(t, cs, "symbol", map[string]any{"id": "main"})
	if isErr {
		t.Fatalf("默认仓库 symbol 调用报错: %s", text)
	}
	if err := json.Unmarshal([]byte(text), &m); err != nil {
		t.Fatalf("content 应为 JSON: %v\n%s", err, text)
	}
	if !strings.Contains(m.ID, "example.com/m:main") {
		t.Errorf("空 repo 应命中默认仓库 main，got id=%s", m.ID)
	}

	// 不存在仓库 → isError
	_, isErr = mcpCallTool(t, cs, "symbol", map[string]any{"id": "main", "repo": "/nope/nope"})
	if !isErr {
		t.Error("不存在的仓库应 isError")
	}
}

// TestMCPToolStale：索引过期（commit_sha ≠ HEAD）时工具结果追加
// [stale] 标注（Agent 可见；content[0] 仍是契约 JSON）。
func TestMCPToolStale(t *testing.T) {
	dir := seedGitRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	mainID := domain.CanonicalID("symbol:go:example.com/m:main")
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: mainID, Kind: domain.KindFunction, Name: "main", FilePath: "main.go"},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO build_metadata (build_id, commit_sha, tool_name, status, timestamp) VALUES ('b1','deadbeef','all','success',?)`, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	cs := mcpDial(t, dir)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "symbol", Arguments: map[string]any{"id": "main"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(res.Content) < 2 {
		t.Fatalf("过期时应有 2 个 content（契约 + stale 标注），got %d", len(res.Content))
	}
	tc, ok := res.Content[1].(*mcp.TextContent)
	if !ok || !strings.Contains(tc.Text, "[stale]") || !strings.Contains(tc.Text, "deadbeef") {
		t.Errorf("content[1] 应为 stale 标注: %v", res.Content[1])
	}
	// content[0] 仍是契约 JSON（不受影响）
	var m map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &m); err != nil {
		t.Errorf("content[0] 应为契约 JSON: %v", err)
	}
}

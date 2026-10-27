package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

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
	dir := seedGitRepo(t)
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
	dir := seedGitRepo(t)
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

// TestMCPToolSymbolCandidates P1-03：MCP symbol 工具描述承诺"动态派发
// 候选"，输出必须与 query symbol 同源（组合在 action.SymbolDetail）——
// 原先只有 cli 自己拼 candidates，MCP 拿不到。
func TestMCPToolSymbolCandidates(t *testing.T) {
	dir := seedRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	r := sqlite.NewRepo(db)
	iface := domain.CanonicalID("symbol:go:example.com/m/svc:Handler")
	impl := domain.CanonicalID("symbol:go:example.com/m/svc:(Svc).Run")
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: iface, Kind: domain.KindInterface, Name: "Handler", FilePath: "svc/svc.go", LineStart: 3},
	}, []*domain.Fact{{
		SourceID: iface, TargetID: impl, Kind: domain.FactDispatchTo,
		ToolSource: domain.ToolSSA, Confidence: 0.9,
		Metadata: map[string]any{"origin": "register", "interface_method": "Handle",
			"register_site": float64(5), "confidence": 0.9},
	}}, nil); err != nil {
		t.Fatalf("save dispatch: %v", err)
	}
	db.Close()

	cs := mcpDial(t, dir)
	text, isErr := mcpCallTool(t, cs, "symbol", map[string]any{"id": string(iface)})
	if isErr {
		t.Fatalf("symbol 调用报错: %s", text)
	}
	var m struct {
		Candidates []struct {
			ID           string  `json:"id"`
			Method       string  `json:"method"`
			Origin       string  `json:"origin"`
			Confidence   float64 `json:"confidence"`
			RegisterSite int     `json:"register_site"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal([]byte(text), &m); err != nil {
		t.Fatalf("content 应为 JSON: %v\n%s", err, text)
	}
	if len(m.Candidates) != 1 {
		t.Fatalf("candidates = %d, want 1:\n%s", len(m.Candidates), text)
	}
	c := m.Candidates[0]
	if c.ID != string(impl) || c.Method != "Handle" || c.Origin != "register" ||
		c.Confidence != 0.9 || c.RegisterSite != 5 {
		t.Errorf("candidate = %+v", c)
	}
}

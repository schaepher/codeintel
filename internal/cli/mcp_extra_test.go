package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// TestMCPToolMultiRepo：#232 多仓库——repo 参数（路径）解析目标仓库；
// 空 repo 用默认仓库；不存在仓库 isError。
func TestMCPToolMultiRepo(t *testing.T) {
	dir2 := seedRepoM2(t)
	cs := mcpDial(t, seedRepo(t))

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

// TestMCPOutputSchema：#235 全部工具输出 schema 自描述（tools/list 可见，
// Agent 零猜测字段）。
func TestMCPOutputSchema(t *testing.T) {
	cs := mcpDial(t, seedRepo(t))
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	tools := res.Tools
	if len(tools) < 19 {
		t.Fatalf("工具数 = %d, want >= 19", len(tools))
	}
	for _, tl := range tools {
		if tl.OutputSchema == nil {
			t.Errorf("工具 %s 缺输出 schema", tl.Name)
		}
	}

	for _, tl := range tools {
		if tl.Name != "symbol" || tl.OutputSchema == nil {
			continue
		}
		b, err := json.Marshal(tl.OutputSchema)
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"callers", "confidence", "name", "signature"} {
			if !strings.Contains(string(b), key) {
				t.Errorf("symbol schema 应含 %s: %s", key, b)
			}
		}
	}
}

// TestMCPToolRecentChanges：#237 recent_changes——commit 时间降序 +
// 文件/符号聚合（fixture 直连库构造）。
func TestMCPToolRecentChanges(t *testing.T) {
	dir := t.TempDir()
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "commit:ccc333", Kind: domain.KindCommit, Name: "ccc333cccccc", Properties: map[string]any{"date": "2026-08-20", "message": "old"}},
		{ID: "commit:ddd444", Kind: domain.KindCommit, Name: "ddd444dddddd", Properties: map[string]any{"date": "2026-08-23", "message": "new"}},
		{ID: "file:x.go", Kind: domain.KindFile, Name: "x.go"},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SaveBatchStats(nil, []*domain.Fact{
		{SourceID: "file:x.go", TargetID: "commit:ddd444", Kind: domain.FactModifiedBy, ToolSource: domain.ToolGit, Confidence: 1.0},
	}, nil); err != nil {
		t.Fatal(err)
	}
	cs := mcpDial(t, dir)
	text, isErr := mcpCallTool(t, cs, "recent_changes", map[string]any{})
	if isErr {
		t.Fatalf("recent_changes 调用报错: %s", text)
	}
	var m struct {
		Commits []struct {
			CommitSHA string `json:"commit_sha"`
			Date      string `json:"date"`
			Message   string `json:"message"`
			Files     []struct {
				Path string `json:"path"`
			} `json:"files"`
		} `json:"commits"`
	}
	if err := json.Unmarshal([]byte(text), &m); err != nil {
		t.Fatalf("content 应为 JSON: %v\n%s", err, text)
	}
	if len(m.Commits) != 2 {
		t.Fatalf("commits = %d, want 2: %s", len(m.Commits), text)
	}
	if m.Commits[0].CommitSHA != "commit:ddd444" || m.Commits[0].Date != "2026-08-23" {
		t.Errorf("最新 commit 应在前: %+v", m.Commits[0])
	}
	if len(m.Commits[0].Files) != 1 || m.Commits[0].Files[0].Path != "x.go" {
		t.Errorf("文件聚合 = %+v", m.Commits[0].Files)
	}
}

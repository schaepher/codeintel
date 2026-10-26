package sqlite

// todo11-1：关系身份（edge_key）——同一 (source,target,kind) 下的不同
// RPC 方法必须独立保存与读取（此前被 UNIQUE 合并，方法只剩一份）。

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestGrpcCallEdgeKeyIdentity：同函数调同服务的 Submit/Cancel → 两条边、
// 各自带方法身份；count 按边（方法）各自累加。
func TestGrpcCallEdgeKeyIdentity(t *testing.T) {
	r := newTestRepo(t)
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:D.Run", Kind: domain.KindMethod, Name: "(D).Run"},
		{ID: "symbol:go:example.com/m:svc.S", Kind: domain.KindGrpcService, Name: "svc.S"},
	}, []*domain.Fact{
		{SourceID: "symbol:go:example.com/m:D.Run", TargetID: "symbol:go:example.com/m:svc.S",
			Kind: domain.FactGrpcCall, Key: "Submit", Confidence: 1.0,
			Metadata: map[string]any{"method": "Submit", "line_num": 6}},
		{SourceID: "symbol:go:example.com/m:D.Run", TargetID: "symbol:go:example.com/m:svc.S",
			Kind: domain.FactGrpcCall, Key: "Cancel", Confidence: 1.0,
			Metadata: map[string]any{"method": "Cancel", "line_num": 7}},
	}, nil); err != nil {
		t.Fatal(err)
	}
	facts, err := r.GetFactsByKinds(string(domain.FactGrpcCall))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, f := range facts {
		got[f.Key] = true
	}
	if len(facts) != 2 || !got["Submit"] || !got["Cancel"] {
		t.Fatalf("应按方法各存一条边（edge_key），实为 %d 条: %+v", len(facts), facts)
	}

	// 再存一次 Submit（同一身份）→ 合并进同一条并累加 count，不新增边
	if _, err := r.SaveBatchStats(nil, []*domain.Fact{
		{SourceID: "symbol:go:example.com/m:D.Run", TargetID: "symbol:go:example.com/m:svc.S",
			Kind: domain.FactGrpcCall, Key: "Submit", Confidence: 1.0,
			Metadata: map[string]any{"method": "Submit", "line_num": 8}},
	}, nil); err != nil {
		t.Fatal(err)
	}
	facts, err = r.GetFactsByKinds(string(domain.FactGrpcCall))
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 2 {
		t.Fatalf("同身份重复写入应合并（仍 2 条），实为 %d", len(facts))
	}
	var submitCount int
	for _, f := range facts {
		if f.Key == "Submit" {
			submitCount = f.Count
		}
	}
	if submitCount != 2 {
		t.Errorf("Submit 边 count = %d; want 2（同身份累加）", submitCount)
	}
}

// TestOldEdgesSchemaRequiresReindex：缺 edge_key 的旧库 → 打开报错，并给出
// "RPC 方法身份需重建索引"的可操作提示（旧库里丢失的方法无法还原）。
func TestOldEdgesSchemaRequiresReindex(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".codeintel"), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", filepath.Join(dir, ".codeintel", "codeintel.db"))
	if err != nil {
		t.Fatal(err)
	}
	// 造一个"有 edges 但没有 edge_key"的库
	if _, err := raw.Exec(`
CREATE TABLE nodes (id_int INTEGER PRIMARY KEY, id TEXT NOT NULL UNIQUE, kind TEXT NOT NULL,
  name TEXT NOT NULL, file_path TEXT, line_start INTEGER, line_end INTEGER, properties JSON,
  signature_text TEXT GENERATED ALWAYS AS (json_extract(properties, '$.signature')) VIRTUAL,
  created_at INTEGER DEFAULT (strftime('%s','now')));
CREATE TABLE edges (id INTEGER PRIMARY KEY, source_ref INTEGER NOT NULL, target_ref INTEGER NOT NULL,
  kind TEXT NOT NULL, tool_source TEXT NOT NULL, confidence REAL NOT NULL DEFAULT 0.5,
  metadata JSON, count INTEGER NOT NULL DEFAULT 1,
  FOREIGN KEY (source_ref) REFERENCES nodes(id_int), FOREIGN KEY (target_ref) REFERENCES nodes(id_int),
  UNIQUE(source_ref, target_ref, kind));`); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	_, err = Open(dir)
	if err == nil {
		t.Fatal("缺 edge_key 的旧库应报 schema mismatch")
	}
	msg := err.Error()
	if !strings.Contains(msg, "edge_key") || !strings.Contains(msg, "reindex") {
		t.Errorf("错误信息应指出 edge_key 与 reindex: %v", err)
	}
	if !strings.Contains(msg, "无法还原") {
		t.Errorf("提示应说明旧库的方法元数据已丢失、无法还原: %v", err)
	}
}

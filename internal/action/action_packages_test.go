package action

// R9x 测试（迁自 cli/query_wiki_test.go 的逻辑断言部分，fixture 自建）：
// PackagesData——包 doc_comment（去 Copyright）+ 无说明时包内代码事实
// （结构体字段数/函数签名）。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// seedPackagesRepo 构造含包节点 + 代码事实的索引（query packages 用）：
// 有 doc_comment 的包（含 Copyright 行）+ 无说明的包（fallback 事实）。
func seedPackagesRepo(t *testing.T) *Actions {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	pkg := &domain.CodeEntity{ID: "symbol:go:example.com/m:m", Kind: domain.KindPackage, Name: "m", FilePath: "m.go",
		Properties: map[string]any{"doc_comment": "// Copyright (c) 2026\n// 主包：业务入口"}}
	svcPkg := &domain.CodeEntity{ID: "symbol:go:example.com/m/svc:svc", Kind: domain.KindPackage, Name: "svc", FilePath: "svc/svc.go"}
	svcStruct := &domain.CodeEntity{ID: "symbol:go:example.com/m/svc:OrderService", Kind: domain.KindStruct, Name: "OrderService", FilePath: "svc/order.go",
		Properties: map[string]any{"fields": `[{"name":"repo"},{"name":"db"}]`}}
	svcFn := &domain.CodeEntity{ID: "symbol:go:example.com/m/svc:NewService", Kind: domain.KindFunction, Name: "NewService", FilePath: "svc/order.go",
		Properties: map[string]any{"signature": "func NewService(db *sql.DB) *OrderService"}}
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{pkg, svcPkg, svcStruct, svcFn}, nil, nil); err != nil {
		t.Fatalf("save nodes: %v", err)
	}
	return New(r)
}

// TestPackagesData：doc_comment 去 Copyright；无说明包回退代码事实。
func TestPackagesData(t *testing.T) {
	a := seedPackagesRepo(t)
	out, err := a.PackagesData()
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("包数 = %d; want 2:\n%+v", len(out), out)
	}
	if out[0].Path != "example.com/m" || out[0].Name != "m" {
		t.Errorf("m path/name = %q/%q", out[0].Path, out[0].Name)
	}
	if strings.Contains(out[0].Doc, "Copyright") {
		t.Errorf("doc 应去 Copyright 行: %q", out[0].Doc)
	}
	if !strings.Contains(out[0].Doc, "主包：业务入口") {
		t.Errorf("doc 应含 主包：业务入口: %q", out[0].Doc)
	}
	// svc：无 doc → 包内代码事实（结构体字段数 + 函数签名）
	if out[1].Path != "example.com/m/svc" {
		t.Errorf("svc path = %q", out[1].Path)
	}
	if len(out[1].Structs) != 1 || out[1].Structs[0] != "OrderService（字段 2）" {
		t.Errorf("svc structs = %v", out[1].Structs)
	}
	if len(out[1].Funcs) != 1 || !strings.Contains(out[1].Funcs[0], "func NewService") {
		t.Errorf("svc funcs = %v", out[1].Funcs)
	}
}

// TestPackageDoc：Copyright 行清理（wiki 渲染同源）。
func TestPackageDoc(t *testing.T) {
	p := &domain.CodeEntity{Properties: map[string]any{
		"doc_comment": "// Copyright (c) 2026 Acme\n// Copyright 2025 Other\n// 服务层",
	}}
	doc := PackageDoc(p)
	if strings.Contains(doc, "Copyright") {
		t.Errorf("doc 应去全部 Copyright 行: %q", doc)
	}
	if !strings.Contains(doc, "服务层") {
		t.Errorf("doc 应保留正文: %q", doc)
	}
}

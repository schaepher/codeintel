package sqlite

import (
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestSymbolsAt：#229 file:line 定位符号（报错栈 → 符号）——行范围命中、
// 未命中、内部节点排除。
func TestSymbolsAt(t *testing.T) {
	repo := newTestRepo(t)
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:main", Kind: domain.KindFunction, Name: "main", FilePath: "main.go", LineStart: 3, LineEnd: 9},
		{ID: "symbol:go:example.com/m/svc:(Svc).Run", Kind: domain.KindMethod, Name: "(Svc).Run", FilePath: "svc/svc.go", LineStart: 5, LineEnd: 20},
		// 内部节点（field_access）应被排除
		{ID: "symbol:go:example.com/m:main#m.cfg.read@4", Kind: "field_access", Name: "m.cfg", FilePath: "main.go", LineStart: 4, LineEnd: 4},
	}
	if _, err := repo.SaveBatchStats(nodes, nil, nil); err != nil {
		t.Fatal(err)
	}

	// 命中：main.go:5 在 main 的行范围内
	got, err := repo.SymbolsAt("main.go", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "main" {
		t.Errorf("main.go:5 应命中 main，got %v", names(got))
	}
	// 边界：line_start=line_end=行本身
	got, err = repo.SymbolsAt("main.go", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "main" {
		t.Errorf("main.go:3（起点）应命中 main，got %v", names(got))
	}
	// 未命中：行超出范围
	got, err = repo.SymbolsAt("main.go", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("main.go:100 应未命中，got %v", names(got))
	}
	// 未知文件
	got, err = repo.SymbolsAt("nope.go", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("nope.go 应未命中，got %v", names(got))
	}
}

func names(ns []*domain.CodeEntity) []string {
	out := make([]string, 0, len(ns))
	for _, n := range ns {
		out = append(out, n.Name)
	}
	return out
}

// TestSearchKind：#234 搜索按类型过滤——kind 命中/不命中/LIKE 退化 +
// 空 kind 不过滤。
func TestSearchKind(t *testing.T) {
	repo := newTestRepo(t)
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:main", Kind: domain.KindFunction, Name: "main", FilePath: "main.go"},
		{ID: "symbol:go:example.com/m:Svc", Kind: domain.KindStruct, Name: "Svc", FilePath: "svc.go"},
	}
	if _, err := repo.SaveBatchStats(nodes, nil, nil); err != nil {
		t.Fatal(err)
	}

	// kind=function → 命中 main
	got, err := repo.SearchKind("main", "function")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "main" {
		t.Errorf("SearchKind(main, function) = %v, want [main]", names(got))
	}
	// kind=struct → main 匹配但类型不符 → 空
	got, err = repo.SearchKind("main", "struct")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("SearchKind(main, struct) = %v, want 空", names(got))
	}
	// 空 kind → 不过滤（精确命中 main）
	got, err = repo.SearchKind("main", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "main" {
		t.Errorf("SearchKind(main, '') = %v, want [main]", names(got))
	}
	// LIKE 退化 + kind 过滤：子串 "vc" 命中 Svc（struct）
	got, err = repo.SearchKind("vc", "struct")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "Svc" {
		t.Errorf("SearchKind(vc, struct) = %v, want [Svc]", names(got))
	}
	// LIKE 退化 + kind 不符 → 空
	got, err = repo.SearchKind("vc", "function")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("SearchKind(vc, function) = %v, want 空", names(got))
	}
}

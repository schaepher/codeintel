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

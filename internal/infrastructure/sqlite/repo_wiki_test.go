package sqlite

import (
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestColumnNameNoise：#247 SQL 截断片段噪音——GetAllTableColumns 只
// 返回合法标识符形态的表.列（nodes.access_kind') 等被过滤）。
func TestColumnNameNoise(t *testing.T) {
	repo := newTestRepo(t)
	if _, err := repo.SaveBatchStats([]*domain.CodeEntity{
		{ID: "n1", Kind: domain.KindFieldAccess, Name: "nodes.id", Properties: map[string]any{"is_external": "true", "type_string": "sql"}},
		{ID: "n2", Kind: domain.KindFieldAccess, Name: "nodes.access_kind')", Properties: map[string]any{"is_external": "true", "type_string": "sql"}},
		{ID: "n3", Kind: domain.KindFieldAccess, Name: "nodes.DISTINCT", Properties: map[string]any{"is_external": "true", "type_string": "sql"}},
		{ID: "n4", Kind: domain.KindFieldAccess, Name: "nodes.0", Properties: map[string]any{"is_external": "true", "type_string": "sql"}},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetAllTableColumns()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, c := range got {
		names = append(names, c.Name)
	}
	if len(names) != 1 || names[0] != "nodes.id" {
		t.Errorf("应只返回合法列 nodes.id，got %v", names)
	}
}

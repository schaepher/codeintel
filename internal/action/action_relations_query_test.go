package action

// 批次 C：RelationsQuery/RelationsAllQuery 输出过滤测试（--type/
// --max-hops/--max-results——原 cli/relations_filter.go 逻辑随迁）。

import (
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

func relRow(from, to string, hops int, typ domain.RelationType) *domain.TableRelation {
	return &domain.TableRelation{FromTable: from, ToTable: to, Hops: hops, Type: typ}
}

// TestFilterRelationsDefaultTypes：无 --type 时默认 fk+query+write
// （read 低置信间接扩散不显示——需 --type read 显式展开）。
func TestFilterRelationsDefaultTypes(t *testing.T) {
	rels := []*domain.TableRelation{
		relRow("a", "b", 1, domain.RelationQuery),
		relRow("a", "c", 2, domain.RelationWrite),
		relRow("a", "d", 3, domain.RelationFK),
		relRow("a", "e", 4, domain.RelationRead),
	}
	out := filterRelations(rels, nil, 0, 0)
	if len(out) != 3 {
		t.Fatalf("默认类型过滤 = %d; want 3（read 排除）", len(out))
	}
	for _, r := range out {
		if r.Type == domain.RelationRead {
			t.Errorf("read 不应在默认结果: %+v", r)
		}
	}
}

// TestFilterRelationsTypeExplicit：--type read 显式展开。
func TestFilterRelationsTypeExplicit(t *testing.T) {
	rels := []*domain.TableRelation{
		relRow("a", "b", 1, domain.RelationQuery),
		relRow("a", "e", 4, domain.RelationRead),
	}
	out := filterRelations(rels, []string{"read"}, 0, 0)
	if len(out) != 1 || out[0].Type != domain.RelationRead {
		t.Fatalf("--type read = %+v; want 仅 read", out)
	}
	// 类型参数空白/空串忽略——回落默认（fk+query+write，read 排除）
	out = filterRelations(rels, []string{"", "  "}, 0, 0)
	if len(out) != 1 || out[0].Type != domain.RelationQuery {
		t.Errorf("空白类型参数应回落默认（仅 query）: %+v", out)
	}
}

// TestFilterRelationsMaxHops：--max-hops 过滤。
func TestFilterRelationsMaxHops(t *testing.T) {
	rels := []*domain.TableRelation{
		relRow("a", "b", 1, domain.RelationQuery),
		relRow("a", "c", 3, domain.RelationQuery),
		relRow("a", "d", 5, domain.RelationQuery),
	}
	out := filterRelations(rels, nil, 3, 0)
	if len(out) != 2 {
		t.Fatalf("--max-hops 3 = %d; want 2（≤3 跳）", len(out))
	}
	out = filterRelations(rels, nil, 0, 0)
	if len(out) != 3 {
		t.Errorf("maxHops 0 = 不限制: %+v", out)
	}
}

// TestFilterRelationsMaxResults：--max-results 截断。
func TestFilterRelationsMaxResults(t *testing.T) {
	rels := []*domain.TableRelation{
		relRow("a", "b", 1, domain.RelationQuery),
		relRow("a", "c", 2, domain.RelationQuery),
		relRow("a", "d", 3, domain.RelationQuery),
	}
	out := filterRelations(rels, nil, 0, 2)
	if len(out) != 2 {
		t.Fatalf("--max-results 2 = %d; want 2", len(out))
	}
}

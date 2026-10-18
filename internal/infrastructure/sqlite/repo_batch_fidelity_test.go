package sqlite

import (
	"strconv"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// TestSaveBatchFidelity（todo4-3）：批量写入与逐条写入产出**同一图内容**
// （节点/边逐项一致，含 count/confidence/属性）。
func TestSaveBatchFidelity(t *testing.T) {
	nodes := []*domain.CodeEntity{
		node("symbol:go:example.com/m:a", "function", "a", "a.go"),
		node("symbol:go:example.com/m:b", "function", "b", "b.go"),
		node("symbol:go:example.com/m:c", "function", "c", "c.go"),
	}
	edges := []*domain.Fact{
		{SourceID: "symbol:go:example.com/m:a", TargetID: "symbol:go:example.com/m:b", Kind: domain.FactCalls, ToolSource: domain.ToolSSA, Confidence: 0.8, Count: 2},
		{SourceID: "symbol:go:example.com/m:b", TargetID: "symbol:go:example.com/m:c", Kind: domain.FactCalls, ToolSource: domain.ToolSSA, Confidence: 0.8, Count: 1},
	}
	dump := func(r *Repo) string {
		t.Helper()
		var b strings.Builder
		rows, err := r.Query(`SELECT id, kind, name, file_path, properties FROM nodes ORDER BY id`)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var id, kind, name, file, props string
			if err := rows.Scan(&id, &kind, &name, &file, &props); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			b.WriteString(id + "|" + kind + "|" + name + "|" + file + "|" + props + "\n")
		}
		rows.Close()
		rows, err = r.Query(`SELECT source_id, target_id, kind, confidence, count FROM edges_v ORDER BY source_id, target_id, kind`)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var s, tg, kind string
			var conf float64
			var cnt int
			if err := rows.Scan(&s, &tg, &kind, &conf, &cnt); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			b.WriteString(s + "|" + tg + "|" + kind + "|" + strconv.FormatFloat(conf, 'f', 2, 64) + "|" + strconv.Itoa(cnt) + "\n")
		}
		rows.Close()
		return b.String()
	}

	batched := newTestRepo(t)
	save(t, batched, nodes, edges)

	perItem := newTestRepo(t)
	for _, n := range nodes {
		save(t, perItem, []*domain.CodeEntity{n}, nil)
	}
	for _, e := range edges {
		save(t, perItem, nil, []*domain.Fact{e})
	}
	if a, b := dump(batched), dump(perItem); a != b {
		t.Errorf("批量 vs 逐条写入内容不一致:\n批量:\n%s\n逐条:\n%s", a, b)
	}
}

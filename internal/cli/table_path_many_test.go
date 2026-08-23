package cli

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// seedManyPaths 造 60 条同跳数（2 跳 read）候选路径：table_a → hub_i →
// table_hub（60 个不同中间表——同表对多列会被 adj 表级去重）。
func seedManyPaths(t *testing.T) string {
	t.Helper()
	dir := seedRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	funcID := domain.CanonicalID("symbol:go:example.com/m:find")
	readA := domain.CanonicalID(string(funcID) + "#ext.sql.table_a.id.read@6")
	readHub := domain.CanonicalID(string(funcID) + "#ext.sql.table_hub.hub_col.read@8")
	var nodes []*domain.CodeEntity
	var facts []*domain.Fact
	for _, n := range []*domain.CodeEntity{
		{ID: readA, Kind: domain.KindFieldAccess, Name: "table_a.id", FilePath: "a.go", LineStart: 6,
			Properties: map[string]any{"full_path": "table_a.id", "access_kind": "read",
				"type_string": "sql", "is_external": "true", "func_id": string(funcID)}},
		{ID: readHub, Kind: domain.KindFieldAccess, Name: "table_hub.hub_col", FilePath: "a.go", LineStart: 8,
			Properties: map[string]any{"full_path": "table_hub.hub_col", "access_kind": "read",
				"type_string": "sql", "is_external": "true", "func_id": string(funcID)}},
	} {
		nodes = append(nodes, n)
	}
	for i := 1; i <= 60; i++ {
		hub := fmt.Sprintf("hub_%02d", i)

		fA := domain.CanonicalID(string(funcID) + "#ext.sql." + hub + ".a_id.filter@" + fmt.Sprint(30+i))

		rB := domain.CanonicalID(string(funcID) + "#ext.sql." + hub + ".x_id.read@" + fmt.Sprint(60+i))
		fB := domain.CanonicalID(string(funcID) + "#ext.sql.table_hub.hub_col.filter@" + fmt.Sprint(90+i))
		nodes = append(nodes,
			&domain.CodeEntity{ID: fA, Kind: domain.KindFieldAccess, Name: hub + ".a_id", FilePath: "a.go", LineStart: 30 + i,
				Properties: map[string]any{"full_path": hub + ".a_id", "access_kind": "filter",
					"type_string": "sql", "is_external": "true", "func_id": string(funcID)}},
			&domain.CodeEntity{ID: rB, Kind: domain.KindFieldAccess, Name: hub + ".x_id", FilePath: "a.go", LineStart: 60 + i,
				Properties: map[string]any{"full_path": hub + ".x_id", "access_kind": "read",
					"type_string": "sql", "is_external": "true", "func_id": string(funcID)}},
			&domain.CodeEntity{ID: fB, Kind: domain.KindFieldAccess, Name: "table_hub.hub_col", FilePath: "a.go", LineStart: 90 + i,
				Properties: map[string]any{"full_path": "table_hub.hub_col", "access_kind": "filter",
					"type_string": "sql", "is_external": "true", "func_id": string(funcID)}},
			&domain.CodeEntity{ID: domain.CanonicalID(string(funcID) + "#ta" + fmt.Sprint(i)), Kind: domain.KindSSAValue, Name: "ta" + fmt.Sprint(i), Properties: map[string]any{"func_id": string(funcID)}},
			&domain.CodeEntity{ID: domain.CanonicalID(string(funcID) + "#xa" + fmt.Sprint(i)), Kind: domain.KindSSAValue, Name: "xa" + fmt.Sprint(i), Properties: map[string]any{"func_id": string(funcID)}},
			&domain.CodeEntity{ID: domain.CanonicalID(string(funcID) + "#tb" + fmt.Sprint(i)), Kind: domain.KindSSAValue, Name: "tb" + fmt.Sprint(i), Properties: map[string]any{"func_id": string(funcID)}},
			&domain.CodeEntity{ID: domain.CanonicalID(string(funcID) + "#xb" + fmt.Sprint(i)), Kind: domain.KindSSAValue, Name: "xb" + fmt.Sprint(i), Properties: map[string]any{"func_id": string(funcID)}},
		)
		facts = append(facts,
			&domain.Fact{SourceID: readA, TargetID: domain.CanonicalID(string(funcID) + "#ta" + fmt.Sprint(i)), Kind: domain.FactSummaryIO, ToolSource: domain.ToolSSA, Confidence: 1},
			&domain.Fact{SourceID: domain.CanonicalID(string(funcID) + "#ta" + fmt.Sprint(i)), TargetID: domain.CanonicalID(string(funcID) + "#xa" + fmt.Sprint(i)), Kind: domain.FactDataFlowsTo, ToolSource: domain.ToolSSA, Confidence: 1},
			&domain.Fact{SourceID: domain.CanonicalID(string(funcID) + "#xa" + fmt.Sprint(i)), TargetID: fA, Kind: domain.FactSummaryIO, ToolSource: domain.ToolSSA, Confidence: 1},
			&domain.Fact{SourceID: rB, TargetID: domain.CanonicalID(string(funcID) + "#tb" + fmt.Sprint(i)), Kind: domain.FactSummaryIO, ToolSource: domain.ToolSSA, Confidence: 1},
			&domain.Fact{SourceID: domain.CanonicalID(string(funcID) + "#tb" + fmt.Sprint(i)), TargetID: domain.CanonicalID(string(funcID) + "#xb" + fmt.Sprint(i)), Kind: domain.FactDataFlowsTo, ToolSource: domain.ToolSSA, Confidence: 1},
			&domain.Fact{SourceID: domain.CanonicalID(string(funcID) + "#xb" + fmt.Sprint(i)), TargetID: fB, Kind: domain.FactSummaryIO, ToolSource: domain.ToolSSA, Confidence: 1},
		)
	}
	if _, err := r.SaveBatchStats(nodes, facts, nil); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := r.Save(&domain.BuildMeta{BuildID: "b1", ToolName: "all", Status: "success"}); err != nil {
		t.Fatalf("Save build: %v", err)
	}
	if err := r.PrecomputeAllRelations(nil); err != nil {
		t.Fatalf("precompute: %v", err)
	}
	return dir
}

// TestTablePathCandidatesCap：--json 候选超上限截断 + truncated 标记；
// --full 全列。
func TestTablePathCandidatesCap(t *testing.T) {
	dir := seedManyPaths(t)
	var out string
	errOut := captureStderr(func() {
		out = captureStdout(func() {
			if code := cmdQuery([]string{"table-path", "table_a", "table_hub", "--repo", dir, "--json"}); code != 0 {
				t.Errorf("table-path exit = %d", code)
			}
		})
	})
	t.Logf("stderr: %s", errOut)
	t.Logf("stdout: %s", out)
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("json: %v", err)
	}
	cands, _ := m["candidates"].([]any)
	if len(cands) > maxTablePathCandidates {
		t.Errorf("candidates = %d, want ≤ %d", len(cands), maxTablePathCandidates)
	}
	if m["candidates_truncated"] != true {
		t.Errorf("应标记 candidates_truncated: %v", m["candidates_truncated"])
	}

	out2 := captureStdout(func() {
		if code := cmdQuery([]string{"table-path", "table_a", "table_hub", "--repo", dir, "--json", "--full"}); code != 0 {
			t.Errorf("table-path --full exit = %d", code)
		}
	})
	var m2 map[string]any
	if err := json.Unmarshal([]byte(out2), &m2); err != nil {
		t.Fatalf("json2: %v", err)
	}
	cands2, _ := m2["candidates"].([]any)
	if len(cands2) != 60 {
		t.Errorf("--full candidates = %d, want 60", len(cands2))
	}
	if m2["candidates_truncated"] != nil {
		t.Errorf("--full 不应 truncated: %v", m2["candidates_truncated"])
	}
	if _, has := m2["candidates_truncated"]; has {
		t.Errorf("--full 不应 truncated: %v", m2["candidates_truncated"])
	}
}

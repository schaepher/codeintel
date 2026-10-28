//go:build integration

package integration

// todo25 §13.1/§13.4：快照输入/范围/冲突/失败矩阵。

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
)

func TestTodo25RuntimeInputFailures(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir, _ := writeRuntimeRepo(t, 2, todo25RuntimeV2, "runtime-v2.json")
	bad := map[string]string{
		"bad-json.json":      `{`,
		"bad-version.json":   `{"version":3,"sources":[],"observations":[]}`,
		"dup-key.json":       strings.Replace(todo25RuntimeV2, `"version": 2,`, `"version": 2, "version": 2,`, 1),
		"unknown-field.json": strings.Replace(todo25RuntimeV2, `"version": 2,`, `"version": 2, "bogus": 1,`, 1),
		"missing-rows.json":  `{"version":2,"sources":[{"id":"review","kind":"database"}],"observations":[{"source":"review","resource":{"table":"review_config"},"scope":[{"field":"region","op":"eq","value":"ID"}],"coverage":{"complete":true,"columns":["region","node_type","enabled"]},"provenance":{"observed_at":"2026-10-28T00:00:00Z"}}]}`,
		"null-rows.json":     `{"version":2,"sources":[{"id":"review","kind":"database"}],"observations":[{"source":"review","resource":{"table":"review_config"},"scope":[{"field":"region","op":"eq","value":"ID"}],"coverage":{"complete":true,"columns":["region","node_type","enabled"]},"rows":null,"provenance":{"observed_at":"2026-10-28T00:00:00Z"}}]}`,
		"bad-kind.json":      strings.Replace(todo25RuntimeV2, `"kind": "database"`, `"kind": "file"`, 1),
		"duplicate-source.json": strings.Replace(todo25RuntimeV2,
			`"sources": [{"id": "review", "kind": "database"}],`,
			`"sources": [{"id": "review", "kind": "database"}, {"id": "review", "kind": "database"}],`, 1),
		"duplicate-binding.json": strings.Replace(todo25RuntimeV2,
			`"bindings": [{"target": {"symbol": "symbol:go:example.com/runtimeflow:DB"}, "source": "review"}],`,
			`"bindings": [{"target": {"symbol": "symbol:go:example.com/runtimeflow:DB"}, "source": "review"},{"target": {"symbol": "symbol:go:example.com/runtimeflow:DB"}, "source": "review"}],`, 1),
		"v1-null-rows.json": `{"version":1,"environment":"test","region":"ID","captured_at":"2026-10-28T00:00:00Z","bindings":[],"databases":[{"id":"review","tables":[{"name":"review_config","scope":{"region":"ID"},"complete":true,"rows":null}]}]}`,
	}
	for name, body := range bad {
		p := filepath.Join(dir, name)
		writeFile(t, p, body)
		runtimeErrNoOutput(t, "query", "sequence", "symbol:go:example.com/runtimeflow:Run",
			"--code", "--depth", "8", "--runtime-data", p, "--json", "--repo", dir)
	}
	// 非 sequence --code / 缺值 / 空值：开库前拒绝。
	runtimeErrNoOutput(t, "query", "symbol", "symbol:go:example.com/runtimeflow:DB",
		"--runtime-data", filepath.Join(dir, "runtime-v2.json"), "--repo", dir)
	runtimeErrNoOutput(t, "query", "sequence", "symbol:go:example.com/runtimeflow:Run",
		"--code", "--depth", "8", "--runtime-data", "--repo", dir)
	runtimeErrNoOutput(t, "query", "sequence", "symbol:go:example.com/runtimeflow:Run",
		"--code", "--depth", "8", "--runtime-data=", "--repo", dir)
}

func TestTodo25RuntimeUnknownCases(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir, snap := writeRuntimeRepo(t, 2, todo25RuntimeV2, "runtime-v2.json")
	cases := map[string]string{
		"scope-mismatch.json": strings.NewReplacer(
			`"value": "ID"`, `"value": "MY"`,
			`"value": [2, 3]`, `"value": [5, 6]`).Replace(todo25RuntimeV2),
		"incomplete.json": strings.Replace(todo25RuntimeV2, `"complete": true`, `"complete": false`, 1),
		"wrong-binding.json": strings.Replace(todo25RuntimeV2,
			`symbol:go:example.com/runtimeflow:DB`, `symbol:go:example.com/runtimeflow:OtherDB`, 1),
		"dup-rows.json": strings.Replace(todo25RuntimeV2,
			`{"region": "ID", "node_type": 2, "enabled": 1},
      {"region": "ID", "node_type": 3, "enabled": 0}`,
			`{"region": "ID", "node_type": 2, "enabled": 1},
      {"region": "ID", "node_type": 2, "enabled": 1}`,
			1),
	}
	for name, body := range cases {
		p := filepath.Join(dir, name)
		writeFile(t, p, body)
		root, _ := runtimeSeqTree(t, dir, "symbol:go:example.com/runtimeflow:Run", 8, p)
		assertRuntimeCounts(t, root, 1, 1, 1)
	}
	// 未知 region 实参：不借用其他场景值。
	root, _ := runtimeSeqTree(t, dir, "symbol:go:example.com/runtimeflow:RunUnknown", 8, snap)
	assertRuntimeCounts(t, root, 1, 1, 1)

	// 成功读取但 enabled=0：Auto（同一源码，仅快照值不同）。
	zero := strings.Replace(todo25RuntimeV1, `"enabled":1`, `"enabled":0`, 1)
	pZero := filepath.Join(dir, "runtime-zero.json")
	writeFile(t, pZero, zero)
	rootZero, _ := runtimeSeqTree(t, dir, "symbol:go:example.com/runtimeflow:Run", 8, pZero)
	assertRuntimeCounts(t, rootZero, 0, 1, 1)

	// 快照内容变化 → evidence.runtime_hash/config_hash 变化（不声称复现原分析）。
	rootOne, _ := runtimeSeqTree(t, dir, "symbol:go:example.com/runtimeflow:Run", 8, snap)
	h1 := runtimeEvidence(rootOne)
	h2 := runtimeEvidence(rootZero)
	if h1 == nil || h2 == nil || h1.SnapshotHash == h2.SnapshotHash {
		t.Errorf("快照 hash 应随内容变化: %+v vs %+v", h1, h2)
	}
	if reqHash(rootOne) == reqHash(rootZero) {
		t.Errorf("config_hash 应包含快照内容 hash")
	}
}

func reqHash(root *action.CodeSeqNode) string {
	var h string
	walkSeq(root, func(n *action.CodeSeqNode) {
		if n.Evidence != nil && n.Evidence.Request != nil && n.Evidence.Request.ConfigHash != "" {
			h = n.Evidence.Request.ConfigHash
		}
	})
	return h
}

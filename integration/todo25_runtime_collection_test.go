//go:build integration

package integration

// todo25 §13.4：集合/分组（QueryContext + rows.Scan + map 聚合）。
// 公开夹具为脱敏 review_config；不手灌图，真实 init → CLI。

import (
	"path/filepath"
	"strings"
	"testing"
)

const todo25CollectionSrc = `package runtimeflow
import ("context"; "database/sql")
var DB *sql.DB
type Row struct { Region string; NodeType int; ServiceType int; Enabled int }
func Manual(){}
func Auto(){}
func Failure(){}
func Load(ctx context.Context, services []int) ([]*Row, error) {
    rows, err := DB.QueryContext(ctx, "SELECT region,node_type,service_type,enabled FROM review_config WHERE region = ? AND node_type = ? AND service_type IN (?,?)", "ID", 2, services[0], services[1])
    if err != nil { return nil, err }
    defer rows.Close()
    var out []*Row
    for rows.Next() {
        r := &Row{}
        if err := rows.Scan(&r.Region, &r.NodeType, &r.ServiceType, &r.Enabled); err != nil { return nil, err }
        out = append(out, r)
    }
    return out, nil
}
func Group(ctx context.Context, services []int) {
    rows, err := Load(ctx, services)
    if err != nil { Failure(); return }
    m := make(map[int][]*Row)
    for _, r := range rows { m[r.ServiceType] = append(m[r.ServiceType], r) }
    if len(m) == 2 { Manual() }
    if r := m[1]; len(r) > 0 && r[0].Enabled == 1 { Auto() }
}
`

const todo25CollectionV2 = `{
  "version": 2,
  "sources": [{"id": "review", "kind": "database"}],
  "bindings": [{"target": {"symbol": "symbol:go:example.com/runtimeflow:DB"}, "source": "review"}],
  "observations": [{
    "source": "review",
    "resource": {"table": "review_config"},
    "scope": [
      {"field": "region", "op": "eq", "value": "ID"},
      {"field": "node_type", "op": "eq", "value": 2},
      {"field": "service_type", "op": "in", "value": [1, 3]}
    ],
    "coverage": {"complete": true, "columns": ["region", "node_type", "service_type", "enabled"]},
    "rows": [
      {"region": "ID", "node_type": 2, "service_type": 1, "enabled": 1},
      {"region": "ID", "node_type": 2, "service_type": 3, "enabled": 0}
    ],
    "provenance": {"observed_at": "2026-10-28T00:00:00Z", "revision": "coll"}
  }]
}`

func writeCollectionRepo(t *testing.T, snapshot string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/runtimeflow\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), todo25CollectionSrc)
	snap := filepath.Join(dir, "runtime-v2.json")
	writeFile(t, snap, snapshot)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "init")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit=%d", code)
	}
	return dir, snap
}

// TestTodo25RuntimeCollectionVariants 使用三个入口验证 2/1/0 元素分组；
// 空结果用 rows:[] 的完整快照。
func TestTodo25RuntimeCollectionVariants(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	src := strings.Replace(todo25CollectionSrc, `func Group(ctx context.Context, services []int) {
    rows, err := Load(ctx, services)`, `func Group(ctx context.Context) {
    rows, err := Load(ctx, []int{1, 3})`, 1)
	src += `
func GroupOne(ctx context.Context) {
    rows, err := Load(ctx, []int{1, 1})
    if err != nil { Failure(); return }
    m := make(map[int][]*Row)
    for _, r := range rows { m[r.ServiceType] = append(m[r.ServiceType], r) }
    if len(m) == 1 { Manual() } else { Auto() }
}
func GroupEmpty(ctx context.Context) {
    rows, err := Load(ctx, []int{1, 3})
    if err != nil { Failure(); return }
    m := make(map[int][]*Row)
    for _, r := range rows { m[r.ServiceType] = append(m[r.ServiceType], r) }
    if len(m) == 0 { Manual() } else { Auto() }
}
`
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/runtimeflow\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), src)
	snap := filepath.Join(dir, "runtime-v2.json")
	writeFile(t, snap, todo25CollectionV2)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "init")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit=%d", code)
	}
	root, _ := runtimeSeqTree(t, dir, "symbol:go:example.com/runtimeflow:Group", 12, snap)
	if countRuntimeLabel(root, "Manual") != 1 || countRuntimeLabel(root, "Auto") != 1 ||
		countRuntimeLabel(root, "Failure") != 1 {
		t.Errorf("两项分组应 Manual=1 Auto=1 Failure=1: %d/%d/%d",
			countRuntimeLabel(root, "Manual"), countRuntimeLabel(root, "Auto"), countRuntimeLabel(root, "Failure"))
	}
	one, _ := runtimeSeqTree(t, dir, "symbol:go:example.com/runtimeflow:GroupOne", 12, snap)
	if countRuntimeLabel(one, "Manual") != 1 || countRuntimeLabel(one, "Auto") != 0 {
		t.Errorf("服务集合 [1] 应 Manual=1 Auto=0: %d/%d",
			countRuntimeLabel(one, "Manual"), countRuntimeLabel(one, "Auto"))
	}

	emptySnap := strings.Replace(todo25CollectionV2,
		`{"region": "ID", "node_type": 2, "service_type": 1, "enabled": 1},
      {"region": "ID", "node_type": 2, "service_type": 3, "enabled": 0}`,
		``, 1)
	writeFile(t, snap, emptySnap)
	empty, _ := runtimeSeqTree(t, dir, "symbol:go:example.com/runtimeflow:GroupEmpty", 12, snap)
	if countRuntimeLabel(empty, "Manual") != 1 || countRuntimeLabel(empty, "Auto") != 0 {
		t.Errorf("完整空结果应 Manual=1 Auto=0: %d/%d",
			countRuntimeLabel(empty, "Manual"), countRuntimeLabel(empty, "Auto"))
	}

	// 不完整范围：查询不得套值（保留 unknown/Failure）。
	writeFile(t, snap, strings.Replace(todo25CollectionV2, `"complete": true`, `"complete": false`, 1))
	incomplete, _ := runtimeSeqTree(t, dir, "symbol:go:example.com/runtimeflow:Group", 12, snap)
	if countRuntimeLabel(incomplete, "Manual") != 1 || countRuntimeLabel(incomplete, "Auto") != 1 ||
		countRuntimeLabel(incomplete, "Failure") != 1 {
		t.Errorf("不完整范围应保留未知与 Failure: %d/%d/%d",
			countRuntimeLabel(incomplete, "Manual"), countRuntimeLabel(incomplete, "Auto"),
			countRuntimeLabel(incomplete, "Failure"))
	}
}

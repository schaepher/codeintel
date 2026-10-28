//go:build integration

package integration

// todo26 U03：运行时集合聚合 / 动态 SQL 过滤 / model-tag 映射的 init→CLI 验收。

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
)

const todo26U03Src = `package flow

import (
	"context"
	"database/sql"
)

var DB *sql.DB

type Row struct {
	Key     int ` + "`db:\"service_type\"`" + `
	Enabled int ` + "`db:\"enabled\"`" + `
}

func Load(ctx context.Context, keys []int) (map[int]int, error) {
	q := "SELECT service_type, enabled FROM review_config WHERE region = ? AND node_type = ?"
	if len(keys) > 0 {
		q += " AND service_type IN (?, ?)"
	}
	rows, err := DB.QueryContext(ctx, q, "ID", 2, keys[0], keys[1])
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int]int)
	for rows.Next() {
		var r Row
		if err := rows.Scan(&r.Key, &r.Enabled); err != nil {
			return nil, err
		}
		out[r.Key] = r.Enabled
	}
	return out, nil
}

func Manual()  {}
func Auto()    {}
func Failure() {}

func Entry(ctx context.Context) {
	m, err := Load(ctx, []int{1, 3})
	if err != nil {
		Failure()
		return
	}
	if m[1] == 1 {
		Manual()
	} else {
		Auto()
	}
}

func EntryEmpty(ctx context.Context) {
	m, err := Load(ctx, []int{9, 10})
	if err != nil {
		Failure()
		return
	}
	if len(m) == 0 {
		return
	}
	if m[1] == 1 {
		Manual()
	} else {
		Auto()
	}
}
`

const todo26U03Runtime = `{"version":2,"sources":[{"id":"review","kind":"database"}],
"bindings":[{"target":{"symbol":"symbol:go:example.com/flow:DB"},"source":"review"}],
"observations":[{"source":"review","resource":{"table":"review_config"},
"scope":[{"field":"region","op":"eq","value":"ID"},{"field":"node_type","op":"eq","value":2}],
"coverage":{"complete":true,"columns":["region","node_type","service_type","enabled"]},
"rows":[{"region":"ID","node_type":2,"service_type":1,"enabled":1},{"region":"ID","node_type":2,"service_type":3,"enabled":0}],
"provenance":{"observed_at":"2026-10-28T00:00:00Z"}}]}`

// u03Counts 统计 JSON 树里指定 label 的 call 节点数（含候选/分支）。
func u03Counts(nodes []*action.CodeSeqNode, name string) int {
	n := 0
	for _, nd := range nodes {
		if nd.Kind == "call" && nd.Label == name {
			n++
		}
		n += u03Counts(nd.Nodes, name)
		n += u03Counts(nd.Else, name)
	}
	return n
}

func TestTodo26U03RuntimeGroupingCLI(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/flow\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), todo26U03Src)
	snap := filepath.Join(dir, "runtime.json")
	writeFile(t, snap, todo26U03Runtime)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "init")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit=%d", code)
	}
	for _, tc := range []struct {
		entry                string
		wantManual, wantAuto int
	}{
		{"Entry", 1, 0},
		{"EntryEmpty", 0, 0},
	} {
		code, out, errOut := runCLIOutErr(t, "query", "sequence",
			"symbol:go:example.com/flow:"+tc.entry, "--code", "--depth", "8",
			"--json", "--runtime-data", snap, "--repo", dir)
		if code != 0 {
			t.Fatalf("%s exit=%d stderr=%s", tc.entry, code, errOut)
		}
		var root action.CodeSeqNode
		if err := json.Unmarshal([]byte(out), &root); err != nil {
			t.Fatalf("%s JSON: %v\n%s", tc.entry, err, out)
		}
		manual := u03Counts(root.Nodes, "Manual")
		auto := u03Counts(root.Nodes, "Auto")
		if manual != tc.wantManual || auto != tc.wantAuto {
			t.Errorf("%s: Manual=%d Auto=%d（want %d/%d）",
				tc.entry, manual, auto, tc.wantManual, tc.wantAuto)
		}
	}
}

// 不完整 observation：不套值（两分支都保留）。
func TestTodo26U03IncompleteNoValueApplied(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/flow\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), todo26U03Src)
	snap := filepath.Join(dir, "runtime.json")
	writeFile(t, snap, strings.Replace(todo26U03Runtime, `"complete":true`, `"complete":false`, 1))
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "init")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit=%d", code)
	}
	code, out, errOut := runCLIOutErr(t, "query", "sequence",
		"symbol:go:example.com/flow:Entry", "--code", "--depth", "8",
		"--json", "--runtime-data", snap, "--repo", dir)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, errOut)
	}
	var root action.CodeSeqNode
	if err := json.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if u03Counts(root.Nodes, "Manual") != 1 || u03Counts(root.Nodes, "Auto") != 1 {
		t.Errorf("不完整事实应保留两分支: Manual=%d Auto=%d",
			u03Counts(root.Nodes, "Manual"), u03Counts(root.Nodes, "Auto"))
	}
}

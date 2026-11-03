//go:build integration

package integration

// todo28 T19：通用运行时事实边界与无快照读取发现（真实源码 → init → CLI）。

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

const t19Src = `package runtimeflow
import("context";"database/sql")
var DB *sql.DB
func Manual(){}
func Auto(){}
func Failure(){}
func Run(ctx context.Context){
 var enabled int
 err:=DB.QueryRowContext(ctx,"SELECT enabled FROM review_config WHERE region = 'ID' AND node_type = 2").Scan(&enabled)
 if err!=nil{Failure();return}
 if enabled==1{Manual()}else{Auto()}
}
func Plain(){
 var enabled int
 err:=DB.QueryRow("SELECT enabled FROM review_config WHERE region = 'ID' AND node_type = 2").Scan(&enabled)
 if err!=nil{Failure();return}
 if enabled==1{Manual()}else{Auto()}
}
func Unsupported(ctx context.Context){
 var enabled int
 err:=DB.QueryRowContext(ctx,"SELECT DISTINCT enabled FROM review_config WHERE region = 'ID' AND node_type = 2").Scan(&enabled)
 if err!=nil{Failure();return}
 if enabled==1{Manual()}else{Auto()}
}
`

const t19Incomplete = `{"version":1,"environment":"test","region":"ID","captured_at":"2026-10-28T00:00:00Z","bindings":[{"symbol":"symbol:go:example.com/runtimeflow:DB","database":"review"}],"databases":[{"id":"review","tables":[{"name":"review_config","scope":{"region":"ID","node_type":2},"complete":false,"rows":[{"region":"ID","node_type":2,"enabled":1}]}]}]}`

const t19CompleteEmpty = `{"version":1,"environment":"test","region":"ID","captured_at":"2026-10-28T00:00:00Z","bindings":[{"symbol":"symbol:go:example.com/runtimeflow:DB","database":"review"}],"databases":[{"id":"review","tables":[{"name":"review_config","scope":{"region":"ID","node_type":2},"complete":true,"rows":[]}]}]}`

func t19Fixture(t *testing.T, runtimeJSON string) (dir, rt string) {
	t.Helper()
	dir = t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/runtimeflow\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), t19Src)
	rt = filepath.Join(dir, "runtime.json")
	writeFile(t, rt, runtimeJSON)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir, rt
}

// t19Runtime 查询入口 → 第一条 runtime 证据。
func t19Runtime(t *testing.T, dir, rt, entry string) map[string]any {
	t.Helper()
	args := []string{"query", "sequence", "symbol:go:example.com/runtimeflow:" + entry,
		"--repo", dir, "--code", "--depth", "8", "--json"}
	if rt != "" {
		args = append(args, "--runtime-data", rt)
	}
	code, out := runCLIOut(t, args...)
	if code != 0 {
		t.Fatalf("query %s exit=%d\n%s", entry, code, out)
	}
	var res struct {
		Nodes []map[string]any `json:"Nodes"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("json: %v", err)
	}
	var found map[string]any
	var walk func(n map[string]any)
	walk = func(n map[string]any) {
		if found != nil {
			return
		}
		if ev, ok := n["evidence"].(map[string]any); ok {
			if r, ok := ev["runtime"].(map[string]any); ok {
				found = r
				return
			}
		}
		for _, key := range []string{"Nodes", "Else"} {
			if arr, ok := n[key].([]any); ok {
				for _, c := range arr {
					walk(c.(map[string]any))
				}
			}
		}
	}
	for _, n := range res.Nodes {
		walk(n)
	}
	return found
}

func t19Boundary(t *testing.T, rt map[string]any) map[string]any {
	t.Helper()
	b, ok := rt["boundary"].(map[string]any)
	if !ok {
		t.Fatalf("缺少 boundary: %+v", rt)
	}
	return b
}

// TestT19IncompleteRows E-T19：不完整事实 → missing_fact/incomplete_rows。
func TestT19IncompleteRows(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir, rtPath := t19Fixture(t, t19Incomplete)
	rt := t19Runtime(t, dir, rtPath, "Run")
	if rt == nil || rt["result"] != "unknown" {
		t.Fatalf("runtime: %+v", rt)
	}
	if rt["reason"] != "没有覆盖查询范围的完整 observation" {
		t.Fatalf("reason: %+v", rt["reason"])
	}
	b := t19Boundary(t, rt)
	if b["kind"] != "missing_fact" || b["code"] != "incomplete_rows" {
		t.Fatalf("boundary: %+v", b)
	}
	needs, _ := b["needs"].([]any)
	if len(needs) != 1 || needs[0] != "complete_rows" {
		t.Fatalf("needs: %+v", needs)
	}
	// Mermaid：同一边界（保留 Manual/Auto/Failure）。
	code, out := runCLIOut(t, "query", "sequence", "Run", "--repo", dir,
		"--code", "--depth", "8", "--runtime-data", rtPath, "--format", "mermaid")
	if code != 0 {
		t.Fatalf("mermaid exit=%d\n%s", code, out)
	}
	for _, want := range []string{"运行时边界 missing_fact/incomplete_rows", "table=review_config",
		"需要事实：complete_rows", "Manual", "Auto", "Failure"} {
		if !strings.Contains(out, want) {
			t.Fatalf("缺少 %q:\n%s", want, out)
		}
	}
}

// TestT19NoSnapshot 无快照 → missing_fact/snapshot_missing（字面量 SELECT）。
func TestT19NoSnapshot(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir, _ := t19Fixture(t, t19Incomplete)
	rt := t19Runtime(t, dir, "", "Run")
	if rt == nil {
		t.Fatal("无快照也应给出可采集的缺口")
	}
	b := t19Boundary(t, rt)
	if b["kind"] != "missing_fact" || b["code"] != "snapshot_missing" {
		t.Fatalf("boundary: %+v", b)
	}
	if rt["table"] != "review_config" {
		t.Fatalf("应保留源码已证明的表: %+v", rt)
	}
	if rt["symbol"] != "symbol:go:example.com/runtimeflow:DB" {
		t.Fatalf("应保留资源符号: %+v", rt["symbol"])
	}
}

// TestT19EmptyAndScope 完整空集 vs scope 不覆盖。
func TestT19EmptyAndScope(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir, rtPath := t19Fixture(t, t19CompleteEmpty)
	rt := t19Runtime(t, dir, rtPath, "Run")
	if rt == nil || rt["result"] != "empty" {
		t.Fatalf("empty runtime: %+v", rt)
	}
	if _, ok := rt["boundary"]; ok {
		t.Fatalf("完整空集不应有缺口 boundary: %+v", rt)
	}
	// 空结果 scope 改 MY → scope_not_covered（不当空结果）。
	const scope = `{"version":1,"environment":"test","region":"MY","captured_at":"2026-10-28T00:00:00Z","bindings":[{"symbol":"symbol:go:example.com/runtimeflow:DB","database":"review"}],"databases":[{"id":"review","tables":[{"name":"review_config","scope":{"region":"MY"},"complete":true,"rows":[]}]}]}`
	dir2, rt2 := t19Fixture(t, scope)
	got := t19Runtime(t, dir2, rt2, "Run")
	b := t19Boundary(t, got)
	if b["kind"] != "missing_fact" || b["code"] != "scope_not_covered" {
		t.Fatalf("boundary: %+v", b)
	}
}

// TestT19UnsupportedSql DISTINCT 等 → analysis_boundary/query_not_proven（无 needs）。
func TestT19UnsupportedSql(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir, rtPath := t19Fixture(t, t19Incomplete)
	for _, rt := range []string{rtPath, ""} {
		got := t19Runtime(t, dir, rt, "Unsupported")
		b := t19Boundary(t, got)
		if b["kind"] != "analysis_boundary" || b["code"] != "query_not_proven" {
			t.Fatalf("boundary: %+v", b)
		}
		if _, ok := b["needs"]; ok {
			t.Fatalf("静态语义未证明时不索要配置: %+v", b)
		}
	}
}

// TestT19BindingAndColumns 缺绑定/缺列：仍保留查询身份、给出准确缺口。
func TestT19BindingAndColumns(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	// binding 指向其它符号 → binding_missing，但仍保留源码 DB 符号与表身份。
	const missing = `{"version":1,"environment":"test","region":"ID","captured_at":"2026-10-28T00:00:00Z","bindings":[{"symbol":"symbol:go:example.com/runtimeflow:Other","database":"review"}],"databases":[{"id":"review","tables":[{"name":"review_config","scope":{"region":"ID","node_type":2},"complete":true,"rows":[{"region":"ID","node_type":2,"enabled":1}]}]}]}`
	dir, rt := t19Fixture(t, missing)
	got := t19Runtime(t, dir, rt, "Run")
	b := t19Boundary(t, got)
	if b["kind"] != "missing_fact" || b["code"] != "binding_missing" {
		t.Fatalf("boundary: %+v", b)
	}
	if got["table"] != "review_config" {
		t.Fatalf("缺绑定仍应保留查询身份: %+v", got)
	}
	// 完整观测但行缺 enabled 列 → columns_missing（不当 enabled=0）。
	const columns = `{"version":1,"environment":"test","region":"ID","captured_at":"2026-10-28T00:00:00Z","bindings":[{"symbol":"symbol:go:example.com/runtimeflow:DB","database":"review"}],"databases":[{"id":"review","tables":[{"name":"review_config","scope":{"region":"ID","node_type":2},"complete":true,"rows":[{"region":"ID","node_type":2}]}]}]}`
	dir2, rt2 := t19Fixture(t, columns)
	got2 := t19Runtime(t, dir2, rt2, "Run")
	b2 := t19Boundary(t, got2)
	if b2["kind"] != "missing_fact" || b2["code"] != "columns_missing" {
		t.Fatalf("boundary: %+v", b2)
	}
}

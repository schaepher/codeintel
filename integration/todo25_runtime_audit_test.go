//go:build integration

package integration

// todo25 §13.4 多轮审核：动态配置行决定人工/自动条件；Finish 次数来自代码
// 条件而非配置条数；每轮按 node 实参选择对应行。

import (
	"path/filepath"
	"strings"
	"testing"
)

const todo25AuditSrc = `package runtimeflow
import ("context"; "database/sql")
var DB *sql.DB
type Cfg struct { Enabled int; Auto int }
func Failure(){}
func Finish(node int){}
func Auto(node int){}
func Load(ctx context.Context, node int) (Cfg, error) {
    var c Cfg
    err := DB.QueryRowContext(ctx, "SELECT enabled,auto FROM review_config WHERE node_type = ?", node).Scan(&c.Enabled, &c.Auto)
    return c, err
}
func Audit(ctx context.Context, node int) {
    c, err := Load(ctx, node)
    if err != nil { Failure(); return }
    if c.Enabled == 1 { Finish(node) }
    if c.Auto == 1 { Auto(node) }
}
func AuditOne(ctx context.Context) { Audit(ctx, 2) }
func AuditTwo(ctx context.Context) { Audit(ctx, 3) }
`

const todo25AuditV2 = `{
  "version": 2,
  "sources": [{"id": "review", "kind": "database"}],
  "bindings": [{"target": {"symbol": "symbol:go:example.com/runtimeflow:DB"}, "source": "review"}],
  "observations": [{
    "source": "review",
    "resource": {"table": "review_config"},
    "scope": [{"field": "node_type", "op": "in", "value": [2, 3]}],
    "coverage": {"complete": true, "columns": ["node_type", "enabled", "auto"]},
    "rows": [
      {"node_type": 2, "enabled": 1, "auto": 0},
      {"node_type": 3, "enabled": 1, "auto": 1}
    ],
    "provenance": {"observed_at": "2026-10-28T00:00:00Z", "revision": "audit"}
  }]
}`

func TestTodo25RuntimeMultiRoundAudit(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/runtimeflow\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), todo25AuditSrc)
	snap := filepath.Join(dir, "runtime-v2.json")
	writeFile(t, snap, todo25AuditV2)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "init")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit=%d", code)
	}
	for _, tc := range []struct {
		target       string
		finish, auto int
	}{
		{"AuditOne", 1, 0},
		{"AuditTwo", 1, 1},
	} {
		root, _ := runtimeSeqTree(t, dir, "symbol:go:example.com/runtimeflow:"+tc.target, 12, snap)
		if got := countRuntimeLabel(root, "Finish"); got != tc.finish {
			t.Errorf("%s Finish=%d; want %d", tc.target, got, tc.finish)
		}
		if got := countRuntimeLabel(root, "Auto"); got != tc.auto {
			t.Errorf("%s Auto=%d; want %d (status=%q)", tc.target, got, tc.auto, runtimeBranchStatus(root, "c.Auto == 1"))
		}
		if got := countRuntimeLabel(root, "Failure"); got != 1 {
			t.Errorf("%s Failure=%d; want 1", tc.target, got)
		}
		// 两行配置不能被当成"固定两次 Finish"：每轮只按自身条件决定。
		if countRuntimeLabel(root, "Finish") == 2 {
			t.Errorf("%s 不应按配置行数报告固定 Finish 次数", tc.target)
		}
	}
	// node 未知（入口形参）→ 人工/自动条件保留未知，不得按快照行数推算。
	unknown := strings.Replace(todo25AuditSrc, "func AuditOne(ctx context.Context) { Audit(ctx, 2) }",
		"func AuditOne(ctx context.Context, node int) { Audit(ctx, node) }", 1)
	writeFile(t, filepath.Join(dir, "flow.go"), unknown)
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "unknown")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("re-init exit=%d", code)
	}
	root, _ := runtimeSeqTree(t, dir, "symbol:go:example.com/runtimeflow:AuditOne", 12, snap)
	if countRuntimeLabel(root, "Finish") == 0 || countRuntimeLabel(root, "Auto") == 0 {
		t.Errorf("未知 node 应保留人工/自动条件: Finish=%d Auto=%d",
			countRuntimeLabel(root, "Finish"), countRuntimeLabel(root, "Auto"))
	}
}

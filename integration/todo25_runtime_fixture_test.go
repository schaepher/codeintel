//go:build integration

package integration

// todo25 §13：运行时快照夹具与查询辅助（v1/v2 共用真实 init）。

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
)

const todo25RuntimeFlowSrc = `package runtimeflow
import (
    "context"
    "database/sql"
)
var DB *sql.DB
func Config(ctx context.Context, region string, node int) (int, error) {
    var enabled int
    err := DB.QueryRowContext(ctx,
        "SELECT enabled FROM review_config WHERE region = ? AND node_type = ?",
        region, node).Scan(&enabled)
    return enabled, err
}
func Manual() {}
func Auto() {}
func Failure() {}
func Run(ctx context.Context) {
    enabled, err := Config(ctx, "ID", %d)
    if err != nil { Failure(); return }
    if enabled == 1 { Manual() } else { Auto() }
}
func Static() {
    const enabled = 1
    if enabled == 1 { Manual() } else { Auto() }
}
func ConstShadow() {
    const true = false
    if true == false { Manual() } else { Auto() }
}
func TwoReads(ctx context.Context) {
    _, err := Config(ctx, "ID", 2)
    if err != nil { return }
    enabled, nextErr := Config(ctx, "ID", 2)
    if nextErr != nil { Failure() }
    if enabled == 1 { Manual() } else { Auto() }
}
func Rebind(ctx context.Context) {
    DB = nil
    enabled, err := Config(ctx, "ID", 2)
    if err != nil { return }
    if enabled == 1 { Manual() } else { Auto() }
}
func ReadChecked(ctx context.Context, region string, node int) (int, error) {
    enabled, err := Config(ctx, region, node)
    if err != nil { return 0, err }
    return enabled, nil
}
func RunChecked(ctx context.Context) {
    enabled, err := ReadChecked(ctx, "ID", 2)
    if err != nil { Failure(); return }
    if enabled == 1 { Manual() } else { Auto() }
}
func RunUnknown(ctx context.Context, region string) {
    enabled, err := Config(ctx, region, 2)
    if err != nil { Failure(); return }
    if enabled == 1 { Manual() } else { Auto() }
}
`

const todo25RuntimeV1 = `{"version":1,"environment":"test","region":"ID","captured_at":"2026-10-28T00:00:00Z","bindings":[{"symbol":"symbol:go:example.com/runtimeflow:DB","database":"review"}],"databases":[{"id":"review","tables":[{"name":"review_config","scope":{"region":"ID","node_type":2},"complete":true,"rows":[{"region":"ID","node_type":2,"enabled":1}]}]}]}`

const todo25RuntimeV2 = `{
  "version": 2,
  "context": {"environment": "test"},
  "sources": [{"id": "review", "kind": "database"}],
  "bindings": [{"target": {"symbol": "symbol:go:example.com/runtimeflow:DB"}, "source": "review"}],
  "observations": [{
    "source": "review",
    "resource": {"table": "review_config"},
    "scope": [
      {"field": "region", "op": "eq", "value": "ID"},
      {"field": "node_type", "op": "in", "value": [2, 3]}
    ],
    "coverage": {"complete": true, "columns": ["region", "node_type", "enabled"]},
    "rows": [
      {"region": "ID", "node_type": 2, "enabled": 1},
      {"region": "ID", "node_type": 3, "enabled": 0}
    ],
    "provenance": {"observed_at": "2026-10-28T00:00:00Z", "revision": "sample-42"}
  }]
}`

// writeRuntimeRepo 写 runtimeflow 源码（node 为 Run 的实参）+ 快照并真实 init。
func writeRuntimeRepo(t *testing.T, node int, snapshot string, snapshotName string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/runtimeflow\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), fmt.Sprintf(todo25RuntimeFlowSrc, node))
	snap := filepath.Join(dir, snapshotName)
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

func runtimeSeqTree(t *testing.T, dir, sym string, depth int, snapshot string, extra ...string) (*action.CodeSeqNode, string) {
	t.Helper()
	args := []string{"query", "sequence", sym, "--code", "--depth", itoaTest(depth), "--json", "--repo", dir}
	if snapshot != "" {
		args = append(args, "--runtime-data", snapshot)
	}
	args = append(args, extra...)
	code, out := runCLIOut(t, args...)
	if code != 0 {
		t.Fatalf("query sequence exit=%d\n%s", code, out)
	}
	var root action.CodeSeqNode
	if err := json.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("sequence JSON: %v\n%s", err, out)
	}
	return &root, out
}

func countRuntimeLabel(n *action.CodeSeqNode, label string) int {
	if n == nil {
		return 0
	}
	c := 0
	if n.Label == label {
		c = 1
	}
	for _, ch := range n.Nodes {
		c += countRuntimeLabel(ch, label)
	}
	for _, ch := range n.Else {
		c += countRuntimeLabel(ch, label)
	}
	return c
}

func runtimeBranchStatus(root *action.CodeSeqNode, label string) string {
	var status string
	walkSeq(root, func(n *action.CodeSeqNode) {
		if n.Kind == "branch" && n.Label == label && n.Scene != nil {
			status = n.Scene.Status
		}
	})
	return status
}

func runtimeEvidence(root *action.CodeSeqNode) *action.SeqRuntimeEvidence {
	var ev *action.SeqRuntimeEvidence
	walkSeq(root, func(n *action.CodeSeqNode) {
		if n.Evidence != nil && n.Evidence.Runtime != nil {
			ev = n.Evidence.Runtime
		}
	})
	return ev
}

func assertRuntimeCounts(t *testing.T, root *action.CodeSeqNode, manual, auto, failure int) {
	t.Helper()
	if got := countRuntimeLabel(root, "Manual"); got != manual {
		t.Errorf("Manual=%d; want %d", got, manual)
	}
	if got := countRuntimeLabel(root, "Auto"); got != auto {
		t.Errorf("Auto=%d; want %d", got, auto)
	}
	if got := countRuntimeLabel(root, "Failure"); got != failure {
		t.Errorf("Failure=%d; want %d", got, failure)
	}
}

func runtimeErrNoOutput(t *testing.T, args ...string) {
	t.Helper()
	code, out, errOut := runCLIOutErr(t, args...)
	if code == 0 || strings.TrimSpace(out) != "" {
		t.Errorf("应失败且 stdout 为空: exit=%d out=%q", code, out)
	}
	if !strings.Contains(errOut, "error") {
		t.Errorf("错误应写 stderr: %q", errOut)
	}
}

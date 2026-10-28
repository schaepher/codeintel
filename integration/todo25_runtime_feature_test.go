//go:build integration

package integration

// todo25 §13.5：无业务专用规则的通用性验收（example.com/feature）。

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

const todo25FeatureSrc = `package feature
import ("context"; "database/sql")
var DB *sql.DB
func Enabled(ctx context.Context, tenant, feature string) (int, error) {
    var enabled int
    err := DB.QueryRowContext(ctx,
        "SELECT enabled FROM feature_flag WHERE tenant_id = ? AND feature = ?",
        tenant, feature).Scan(&enabled)
    return enabled, err
}
func On(){}
func Off(){}
func Failure(){}
func Run(ctx context.Context) {
    enabled, err := Enabled(ctx, %q, %q)
    if err != nil { Failure(); return }
    if enabled == 1 { On() } else { Off() }
}
`

const todo25FeatureV2 = `{
  "version": 2,
  "context": {"environment": "test"},
  "sources": [{"id": "flags", "kind": "database"}],
  "bindings": [
    {"target": {"symbol": "symbol:go:example.com/feature:DB"}, "source": "flags"}
  ],
  "observations": [{
    "source": "flags",
    "resource": {"table": "feature_flag"},
    "scope": [
      {"field": "tenant_id", "op": "eq", "value": "tenant-a"},
      {"field": "feature", "op": "in", "value": ["alpha", "beta"]}
    ],
    "coverage": {"complete": true, "columns": ["tenant_id", "feature", "enabled"]},
    "rows": [
      {"tenant_id": "tenant-a", "feature": "alpha", "enabled": 1},
      {"tenant_id": "tenant-a", "feature": "beta", "enabled": 0}
    ],
    "provenance": {"observed_at": "2026-10-28T00:00:00Z", "revision": "snapshot-42"}
  }]
}`

func writeFeatureRepo(t *testing.T, tenant, feature string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/feature\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "feature.go"), fmt.Sprintf(todo25FeatureSrc, tenant, feature))
	snap := filepath.Join(dir, "runtime-v2.json")
	writeFile(t, snap, todo25FeatureV2)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "init")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit=%d", code)
	}
	return dir, snap
}

func TestTodo25RuntimeFeatureGenericV2(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir, snap := writeFeatureRepo(t, "tenant-a", "alpha")
	root, _ := runtimeSeqTree(t, dir, "symbol:go:example.com/feature:Run", 8, snap)
	if countRuntimeLabel(root, "On") != 1 || countRuntimeLabel(root, "Off") != 0 ||
		countRuntimeLabel(root, "Failure") != 1 {
		t.Fatalf("alpha 应 On=1 Off=0 Failure=1: On=%d Off=%d Failure=%d",
			countRuntimeLabel(root, "On"), countRuntimeLabel(root, "Off"), countRuntimeLabel(root, "Failure"))
	}
	ev := runtimeEvidence(root)
	if ev == nil || ev.Table != "feature_flag" || len(ev.Query) != 2 ||
		!strings.Contains(strings.Join(ev.Query, "|"), "tenant_id eq \"tenant-a\"") {
		t.Errorf("feature 快照证据不符: %+v", ev)
	}
	// JSON 与 Mermaid 同一分支：Mermaid 不得出现 Off。
	code, mer := runCLIOut(t, "query", "sequence", "symbol:go:example.com/feature:Run",
		"--code", "--depth", "8", "--runtime-data", snap, "--format", "mermaid", "--repo", dir)
	if code != 0 {
		t.Fatalf("mermaid exit=%d: %s", code, mer)
	}
	if !strings.Contains(mer, "On") || strings.Contains(mer, "Off") {
		t.Errorf("Mermaid 应与 JSON 同一分支（On 且无 Off）:\n%s", mer)
	}

	dirBeta, snapBeta := writeFeatureRepo(t, "tenant-a", "beta")
	rootBeta, _ := runtimeSeqTree(t, dirBeta, "symbol:go:example.com/feature:Run", 8, snapBeta)
	if countRuntimeLabel(rootBeta, "On") != 0 || countRuntimeLabel(rootBeta, "Off") != 1 {
		t.Errorf("beta 应 On=0 Off=1: On=%d Off=%d",
			countRuntimeLabel(rootBeta, "On"), countRuntimeLabel(rootBeta, "Off"))
	}

	dirGamma, snapGamma := writeFeatureRepo(t, "tenant-a", "gamma")
	rootGamma, _ := runtimeSeqTree(t, dirGamma, "symbol:go:example.com/feature:Run", 8, snapGamma)
	if countRuntimeLabel(rootGamma, "On") != 1 || countRuntimeLabel(rootGamma, "Off") != 1 {
		t.Errorf("gamma 不在 scope 内应保留未知（On/Off 各一次）: On=%d Off=%d",
			countRuntimeLabel(rootGamma, "On"), countRuntimeLabel(rootGamma, "Off"))
	}
	dirTenant, snapTenant := writeFeatureRepo(t, "tenant-z", "alpha")
	rootTenant, _ := runtimeSeqTree(t, dirTenant, "symbol:go:example.com/feature:Run", 8, snapTenant)
	if countRuntimeLabel(rootTenant, "On") != 1 || countRuntimeLabel(rootTenant, "Off") != 1 {
		t.Errorf("未知 tenant 应保留未知: On=%d Off=%d",
			countRuntimeLabel(rootTenant, "On"), countRuntimeLabel(rootTenant, "Off"))
	}

	// context 任意标签不改变结果。
	noCtx := strings.Replace(todo25FeatureV2, `  "context": {"environment": "test"},`+"\n", "", 1)
	dirN, snapN := writeFeatureRepo(t, "tenant-a", "alpha")
	writeFile(t, snapN, noCtx)
	rootN, _ := runtimeSeqTree(t, dirN, "symbol:go:example.com/feature:Run", 8, snapN)
	if countRuntimeLabel(rootN, "On") != 1 || countRuntimeLabel(rootN, "Off") != 0 {
		t.Errorf("context 缺失不应改变结果: On=%d Off=%d",
			countRuntimeLabel(rootN, "On"), countRuntimeLabel(rootN, "Off"))
	}
}

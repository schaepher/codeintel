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

const todo25FeatureDAOSrc = `package feature
import ("context"; "database/sql")
var DB *sql.DB
type FlagRow struct { TenantID string; Feature string; Enabled int }
type Filter struct { Tenant string; Features []string }
type DAO struct{ db *sql.DB }
func On(){}
func Off(){}
func Failure(){}
func NewDAO(db *sql.DB) *DAO { return &DAO{db: db} }
func (d *DAO) QueryByFilter(ctx context.Context, f Filter) ([]*FlagRow, error) {
    sql := "SELECT tenant_id,feature,enabled FROM feature_flag WHERE tenant_id = ? AND feature IN (?,?)"
    args := []any{f.Tenant, f.Features[0], f.Features[1]}
    rows, err := d.db.QueryContext(ctx, sql, args...)
    if err != nil { return nil, err }
    defer rows.Close()
    var out []*FlagRow
    for rows.Next() {
        r := &FlagRow{}
        if err := rows.Scan(&r.TenantID, &r.Feature, &r.Enabled); err != nil { return nil, err }
        out = append(out, r)
    }
    return out, nil
}
func Group(ctx context.Context, features []string) {
    d := NewDAO(DB)
    rows, err := d.QueryByFilter(ctx, Filter{Tenant: "tenant-a", Features: features})
    if err != nil { Failure(); return }
    m := make(map[string][]*FlagRow)
    for _, r := range rows { m[r.Feature] = append(m[r.Feature], r) }
    if len(m) == 2 { On() } else { Off() }
    if r := m["alpha"]; len(r) > 0 && r[0].Enabled == 1 { On() }
}
`

func TestTodo25RuntimeFeatureCollectionDAO(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/feature\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "feature.go"), todo25FeatureDAOSrc)
	snap := filepath.Join(dir, "runtime-v2.json")
	writeFile(t, snap, strings.Replace(todo25FeatureV2,
		`"resource": {"table": "feature_flag"}`, `"resource": {"table": "feature_flag"}`, 1))
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "init")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit=%d", code)
	}
	// Group 的 features 形参来自入口调用；建立两个入口常量。
	src := strings.Replace(todo25FeatureDAOSrc, "func Group(ctx context.Context, features []string) {",
		"func GroupBoth(ctx context.Context) { Group(ctx, []string{\"alpha\", \"beta\"}) }\nfunc GroupOne(ctx context.Context) { Group(ctx, []string{\"alpha\", \"alpha\"}) }\nfunc Group(ctx context.Context, features []string) {", 1)
	writeFile(t, filepath.Join(dir, "feature.go"), src)
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "entries")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("re-init exit=%d", code)
	}
	both, _ := runtimeSeqTree(t, dir, "symbol:go:example.com/feature:GroupBoth", 16, snap)
	if countRuntimeLabel(both, "On") != 2 || countRuntimeLabel(both, "Off") != 0 {
		t.Errorf("feature 两项分组应 On=2 Off=0: %d/%d",
			countRuntimeLabel(both, "On"), countRuntimeLabel(both, "Off"))
	}
	one, _ := runtimeSeqTree(t, dir, "symbol:go:example.com/feature:GroupOne", 16, snap)
	// len(m)==1 → 源码 else 分支 Off；alpha enabled=1 → 再走 On。
	if countRuntimeLabel(one, "On") != 1 || countRuntimeLabel(one, "Off") != 1 {
		t.Errorf("feature 仅 alpha 应 Off(Else)=1 + On(enabled)=1: %d/%d",
			countRuntimeLabel(one, "On"), countRuntimeLabel(one, "Off"))
	}
}

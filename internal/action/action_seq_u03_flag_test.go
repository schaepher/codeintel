package action

// U03：feature_flag(tenant_id,feature,enabled) 的 string 键分组（与
// review_config 同一实现，无业务名特判）。

import "testing"

// feature_flag(tenant_id,feature,enabled)：同一实现支持 string 键分组，
// 无业务名特判（alpha/beta 两元素、仅 alpha 命中）。
const u03FlagSrc = `package flow

import (
	"context"
	"database/sql"
)

var DB *sql.DB

type FlagRow struct {
	Feature string ` + "`db:\"feature\"`" + `
	Enabled int    ` + "`db:\"enabled\"`" + `
}

func Load(ctx context.Context, features []string) (map[string]int, error) {
	q := "SELECT feature, enabled FROM feature_flag WHERE tenant_id = ?"
	if len(features) > 0 {
		q += " AND feature IN (?, ?)"
	}
	rows, err := DB.QueryContext(ctx, q, "T1", features[0], features[1])
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]int)
	for rows.Next() {
		var r FlagRow
		if err := rows.Scan(&r.Feature, &r.Enabled); err != nil {
			return nil, err
		}
		out[r.Feature] = r.Enabled
	}
	return out, nil
}

func Manual() {}
func Auto()   {}

func Entry(ctx context.Context) {
	m, err := Load(ctx, []string{"alpha", "beta"})
	if err != nil {
		return
	}
	if m["alpha"] == 1 {
		Manual()
	} else {
		Auto()
	}
}
`

const u03FlagRuntime = `{"version":2,"sources":[{"id":"flags","kind":"database"}],
"bindings":[{"target":{"symbol":"symbol:go:example.com/flow:DB"},"source":"flags"}],
"observations":[{"source":"flags","resource":{"table":"feature_flag"},
"scope":[{"field":"tenant_id","op":"eq","value":"T1"}],
"coverage":{"complete":true,"columns":["tenant_id","feature","enabled"]},
"rows":[{"tenant_id":"T1","feature":"alpha","enabled":1},{"tenant_id":"T1","feature":"beta","enabled":0}],
"provenance":{"observed_at":"2026-10-28T00:00:00Z"}}]}`

func TestU03FeatureFlagStringKeys(t *testing.T) {
	acts, dir, snap := seedU03(t, u03FlagSrc, u03FlagRuntime)
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/flow:Entry", RepoAbs: dir, Depth: 8, RuntimeDataPath: snap})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(seqCallNodesNamed(root, "Manual")); got != 1 {
		t.Fatalf("alpha 命中应只走 Manual（got %d）: %v", got, labelsOf(root.Nodes))
	}
	if got := len(seqCallNodesNamed(root, "Auto")); got != 0 {
		t.Fatalf("beta 未命中不应走 Auto（got %d）", got)
	}
	ev := u03RuntimeEvidence(t, root)
	if ev == nil || ev.Table != "feature_flag" || ev.Rows != 2 {
		t.Fatalf("特征表应套用同一实现: %+v", ev)
	}
}

// U01：规范化后的重复 scope——IN 值去重并按稳定值语义排序后判重；
// 同源同表 scope 的 [2,3] 与 [3,2,2] 视为相同，拒绝两份 observation。
// 保留正常不同 scope 及原有类型区别（int 2 与 string "2" 不是同一 scope）。
package action

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeRuntimeDoc(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "runtime.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
	return p
}

func v2Obs(scope string, rows string, complete string) string {
	return `{"source":"review","resource":{"table":"review_config"},"scope":[` + scope + `],` +
		`"coverage":{"complete":` + complete + `,"columns":["node_type","enabled"]},` +
		`"rows":[` + rows + `],"provenance":{"observed_at":"2026-10-28T00:00:00Z"}}`
}

func v2Doc(obs ...string) string {
	return `{"version":2,"sources":[{"id":"review","kind":"database"}],"observations":[` +
		strings.Join(obs, ",") + `]}`
}

func inNodeType(values string) string {
	return `{"field":"node_type","op":"in","value":[` + values + `]}`
}

func TestRuntimeDuplicateNormalizedScopeRejected(t *testing.T) {
	row2 := `{"node_type":2,"enabled":1}`
	row3 := `{"node_type":3,"enabled":0}`
	body := v2Doc(
		v2Obs(inNodeType("2,3"), row2+","+row3, "true"),
		v2Obs(inNodeType("3,2,2"), row2+","+row3, "true"),
	)
	_, err := LoadRuntimeSnapshot(writeRuntimeDoc(t, body))
	if err == nil {
		t.Fatal("duplicate normalized scope should be rejected")
	}
	want := "runtime-data: duplicate normalized scope (source=review, table=review_config, node_type in [2,3])"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}

func TestRuntimeDuplicateScopeAcrossCompletenessRejected(t *testing.T) {
	// 两份部分 observation 也不得拼成一份完整结果。
	body := v2Doc(
		v2Obs(inNodeType("2,3"), `{"node_type":2,"enabled":1}`, "false"),
		v2Obs(inNodeType("2,3"), `{"node_type":3,"enabled":0}`, "true"),
	)
	if _, err := LoadRuntimeSnapshot(writeRuntimeDoc(t, body)); err == nil ||
		!strings.Contains(err.Error(), "duplicate normalized scope") {
		t.Fatalf("partial duplicate should be rejected, got %v", err)
	}
}

func TestRuntimeDistinctScopeAccepted(t *testing.T) {
	cases := map[string]string{
		"different values": v2Doc(
			v2Obs(inNodeType("2,3"), `{"node_type":2,"enabled":1}`, "true"),
			v2Obs(inNodeType("4,5"), `{"node_type":4,"enabled":0}`, "true"),
		),
		"different field": v2Doc(
			v2Obs(inNodeType("2,3"), `{"node_type":2,"enabled":1}`, "true"),
			v2Obs(`{"field":"enabled","op":"eq","value":1}`, `{"node_type":2,"enabled":1}`, "true"),
		),
		// 类型区别：int 2 与 string "2" 不是同一 scope。
		"type distinction": v2Doc(
			v2Obs(inNodeType("2"), `{"node_type":2,"enabled":1}`, "true"),
			v2Obs(inNodeType(`"2"`), `{"node_type":"2","enabled":1}`, "true"),
		),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadRuntimeSnapshot(writeRuntimeDoc(t, body)); err != nil {
				t.Fatalf("distinct scopes accepted? got %v", err)
			}
		})
	}
}

// 归一化结果本身要稳定：IN 值顺序不影响 scope 值顺序（同一 scope 判定用）。
func TestRuntimeScopeNormalizationStable(t *testing.T) {
	body := v2Doc(v2Obs(inNodeType("3,2,3"), `{"node_type":2,"enabled":1}`, "true"))
	snap, err := LoadRuntimeSnapshot(writeRuntimeDoc(t, body))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(snap.Observations) != 1 {
		t.Fatalf("observations = %d", len(snap.Observations))
	}
	sc := snap.Observations[0].Scope
	if len(sc) != 1 || sc[0].Value.kind != rtSlice {
		t.Fatalf("scope = %+v", sc)
	}
	got := sc[0].Value.rtDisplay()
	if got != "[2,3]" {
		t.Fatalf("normalized in list = %s, want [2,3]", got)
	}
}

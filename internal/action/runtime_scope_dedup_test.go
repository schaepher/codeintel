// U01/T19b：规范化后的重复 scope——IN 值去重并按稳定值语义排序后仍判为
// 同一身份；保留两份 observation（加载不拒绝），读取端在只有一条完整观测
// 可回答查询时才使用，多条候选返回 ambiguous_observations——重复/冲突观测
// 不会被拼成一份完整结果。
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

// 规范化后的相同 scope 保留两份（不拼接、不在加载阶段拒绝）。
func TestRuntimeDuplicateNormalizedScopeRetained(t *testing.T) {
	row2 := `{"node_type":2,"enabled":1}`
	row3 := `{"node_type":3,"enabled":0}`
	body := v2Doc(
		v2Obs(inNodeType("2,3"), row2+","+row3, "true"),
		v2Obs(inNodeType("3,2,2"), row2+","+row3, "true"),
	)
	snap, err := LoadRuntimeSnapshot(writeRuntimeDoc(t, body))
	if err != nil {
		t.Fatalf("重复 scope 不应在加载阶段拒绝: %v", err)
	}
	if len(snap.Observations) != 2 {
		t.Fatalf("observations = %d, want 2", len(snap.Observations))
	}
	if snap.Observations[0].identityKey() != snap.Observations[1].identityKey() {
		t.Error("归一化后的 scope 应视为同一身份")
	}
}

// 两份部分 observation 同样保留，但读取端不会拼接成完整结果。
func TestRuntimeDuplicateScopeAcrossCompletenessRetained(t *testing.T) {
	body := v2Doc(
		v2Obs(inNodeType("2,3"), `{"node_type":2,"enabled":1}`, "false"),
		v2Obs(inNodeType("2,3"), `{"node_type":3,"enabled":0}`, "true"),
	)
	snap, err := LoadRuntimeSnapshot(writeRuntimeDoc(t, body))
	if err != nil {
		t.Fatalf("partial duplicate 不应在加载阶段拒绝: %v", err)
	}
	if len(snap.Observations) != 2 {
		t.Fatalf("observations = %d, want 2", len(snap.Observations))
	}
	matches := 0
	for _, obs := range snap.Observations {
		if obs.Complete {
			matches++
		}
	}
	if matches != 1 {
		t.Fatalf("完整观测 = %d, want 1（不得拼成两份）", matches)
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

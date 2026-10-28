package action

// U03：运行时集合聚合 + 动态 SQL 过滤 + model/tag 表列映射。
//
// 验收形态（同一实现，无业务名特判）：
//   - review_config(region,node_type,service_type,enabled)：ID、2、[1,3]
//     两服务行 → 分组 map len=2，仅 service1 为 1；
//   - feature_flag(tenant_id,feature,enabled)：alpha/beta 两元素、仅 alpha；
//   - make(map) 完整空 → len=0（不当作未知）；
//   - 覆盖不完整/缺失事实 → 不套值（保持两分支）。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

const u03FlowSrc = `package flow

import (
	"context"
	"database/sql"
)

var DB *sql.DB

type Row struct {
	ServiceType int ` + "`db:\"service_type\"`" + `
	Enabled     int ` + "`db:\"enabled\"`" + `
}

func Load(ctx context.Context, services []int, node int) (map[int]int, error) {
	q := "SELECT service_type, enabled FROM review_config WHERE region = ? AND node_type = ?"
	if len(services) > 0 {
		q += " AND service_type IN (?, ?)"
	}
	rows, err := DB.QueryContext(ctx, q, "ID", node, services[0], services[1])
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int]int)
	for rows.Next() {
		var r Row
		if err := rows.Scan(&r.ServiceType, &r.Enabled); err != nil {
			return nil, err
		}
		out[r.ServiceType] = r.Enabled
	}
	return out, nil
}

func Manual()  {}
func Auto()    {}
func Failure() {}
func Empty()   {}

func Entry(ctx context.Context) {
	m, err := Load(ctx, []int{1, 3}, 2)
	if err != nil {
		Failure()
		return
	}
	if len(m) == 0 {
		Empty()
		return
	}
	if m[1] == 1 {
		Manual()
	} else {
		Auto()
	}
}

func EntryEmpty(ctx context.Context) {
	m, err := Load(ctx, []int{9, 10}, 2)
	if err != nil {
		Failure()
		return
	}
	if len(m) == 0 {
		Empty()
		return
	}
	if m[1] == 1 {
		Manual()
	} else {
		Auto()
	}
}
`

const u03Runtime = `{"version":2,"sources":[{"id":"review","kind":"database"}],
"bindings":[{"target":{"symbol":"symbol:go:example.com/flow:DB"},"source":"review"}],
"observations":[{"source":"review","resource":{"table":"review_config"},
"scope":[{"field":"region","op":"eq","value":"ID"},{"field":"node_type","op":"eq","value":2}],
"coverage":{"complete":true,"columns":["region","node_type","service_type","enabled"]},
"rows":[{"region":"ID","node_type":2,"service_type":1,"enabled":1},{"region":"ID","node_type":2,"service_type":3,"enabled":0}],
"provenance":{"observed_at":"2026-10-28T00:00:00Z"}}]}`

func seedU03(t *testing.T, src, snapshot string) (*Actions, string, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/flow\n\ngo 1.26.2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "flow.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	snap := filepath.Join(dir, "runtime.json")
	if err := os.WriteFile(snap, []byte(snapshot), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	pkg := "symbol:go:example.com/flow:"
	line := func(prefix string) int {
		for i, l := range strings.Split(src, "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), prefix) {
				return i + 1
			}
		}
		return 0
	}
	nodes := []*domain.CodeEntity{
		{ID: domain.CanonicalID(pkg + "Load"), Kind: domain.KindFunction, Name: "Load",
			FilePath: "flow.go", LineStart: line("func Load(")},
	}
	for _, fn := range []string{"Manual", "Auto", "Failure", "Empty", "Entry", "EntryEmpty"} {
		nodes = append(nodes, &domain.CodeEntity{ID: domain.CanonicalID(pkg + fn),
			Kind: domain.KindFunction, Name: fn, FilePath: "flow.go", LineStart: line("func " + fn + "(")})
	}
	var facts []*domain.Fact
	for _, caller := range []struct{ fn, callee string }{
		{"Entry", "Load"}, {"EntryEmpty", "Load"},
		{"Entry", "Manual"}, {"Entry", "Auto"}, {"Entry", "Failure"}, {"Entry", "Empty"},
		{"EntryEmpty", "Manual"}, {"EntryEmpty", "Auto"}, {"EntryEmpty", "Failure"}, {"EntryEmpty", "Empty"},
	} {
		var ln int
		if caller.callee == "Load" {
			ln = line("m, err := Load(")
		} else {
			ln = line(caller.callee + "()")
		}
		facts = append(facts, &domain.Fact{SourceID: domain.CanonicalID(pkg + caller.fn),
			TargetID: domain.CanonicalID(pkg + caller.callee), Kind: domain.FactCalls,
			Confidence: 0.8, Metadata: map[string]any{"line_num": ln}})
	}
	if _, err := r.SaveBatchStats(nodes, facts, nil); err != nil {
		t.Fatalf("save: %v", err)
	}
	return New(r), dir, snap
}

func u03RuntimeEvidence(t *testing.T, root *CodeSeqNode) *SeqRuntimeEvidence {
	t.Helper()
	var ev *SeqRuntimeEvidence
	walkSeqAll(root, func(n *CodeSeqNode) {
		if n.Evidence != nil && n.Evidence.Runtime != nil && ev == nil {
			ev = n.Evidence.Runtime
		}
	})
	return ev
}

func TestU03GroupedMapFromSnapshot(t *testing.T) {
	acts, dir, snap := seedU03(t, u03FlowSrc, u03Runtime)
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/flow:Entry", RepoAbs: dir, Depth: 8, RuntimeDataPath: snap})
	if err != nil {
		t.Fatal(err)
	}
	manual := len(seqCallNodesNamed(root, "Manual"))
	auto := len(seqCallNodesNamed(root, "Auto"))
	failure := len(seqCallNodesNamed(root, "Failure"))
	if manual != 1 || auto != 0 || failure != 1 {
		t.Fatalf("分组 map 结果：Manual=%d Auto=%d Failure=%d（want 1/0/1）", manual, auto, failure)
	}
	// 动态 SQL（条件 +=）应被求值：IN 谓词与行数进入证据。
	ev := u03RuntimeEvidence(t, root)
	if ev == nil || ev.Rows != 2 {
		t.Fatalf("运行时证据缺行数: %+v", ev)
	}
	found := false
	for _, q := range ev.Query {
		if strings.Contains(q, "service_type") && strings.Contains(q, "in") {
			found = true
		}
	}
	if !found {
		t.Errorf("查询证据应含 service_type IN: %v", ev.Query)
	}
}

// 完整空 map：len(m)==0（非 nil）→ 只走 Empty（不猜 Manual/Auto）。
func TestU03EmptyMapComplete(t *testing.T) {
	acts, dir, snap := seedU03(t, u03FlowSrc, u03Runtime)
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/flow:EntryEmpty", RepoAbs: dir, Depth: 8, RuntimeDataPath: snap})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(seqCallNodesNamed(root, "Empty")); got != 1 {
		t.Fatalf("空 map 应判定 len=0（Empty=%d）: %v", got, labelsOf(root.Nodes))
	}
	if got := len(seqCallNodesNamed(root, "Manual")) + len(seqCallNodesNamed(root, "Auto")); got != 0 {
		t.Fatalf("空 map 后不应再判业务分支（got %d）", got)
	}
}

// seqContainsCall 分支子树是否包含指定调用。
func seqContainsCall(n *CodeSeqNode, label string) bool {
	return len(seqCallNodesNamed(n, label)) > 0
}

// 不完整/缺失事实：不套值（两分支都保留）。
func TestU03IncompleteOrMissingFactsUnknown(t *testing.T) {
	cases := map[string]string{
		"incomplete": strings.Replace(u03Runtime, `"complete":true`, `"complete":false`, 1),
		"wrong-binding": strings.Replace(u03Runtime,
			"symbol:go:example.com/flow:DB", "symbol:go:example.com/flow:OtherDB", 1),
		"scope-mismatch": strings.Replace(u03Runtime, `"value":"ID"`, `"value":"MY"`, 1),
	}
	for name, snap := range cases {
		t.Run(name, func(t *testing.T) {
			acts, dir, snapPath := seedU03(t, u03FlowSrc, snap)
			root, err := acts.CodeSequence(CodeSequenceRequest{
				Target: "symbol:go:example.com/flow:Entry", RepoAbs: dir, Depth: 8, RuntimeDataPath: snapPath})
			if err != nil {
				t.Fatal(err)
			}
			// 不套值 = 两个业务分支都保留（未知），且没有 taken 的场景判定。
			manual := len(seqCallNodesNamed(root, "Manual"))
			auto := len(seqCallNodesNamed(root, "Auto"))
			if !(manual == 1 && auto == 1) {
				t.Errorf("事实不完整应保留两分支（Manual=%d Auto=%d）", manual, auto)
			}
			for _, n := range seqFindNodes(root, func(n *CodeSeqNode) bool {
				return n.Kind == "branch"
			}) {
				if n.Scene != nil && n.Scene.Status == SceneTaken && seqContainsCall(n, "Manual") {
					t.Errorf("业务分支不得因不完整事实判定 taken: %+v", n.Label)
				}
			}
		})
	}
}

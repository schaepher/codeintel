package action

// T19b/T19c：运行时缺口优先级（最接近有效匹配）与无 Context 的 SQL 读取
// 参数位置（database/sql QueryRow/Query 的 SQL 实参是 0）。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

const t19bSrc = `package gap
import ("context"; "database/sql")
var DB *sql.DB
func Entry() {
    var enabled int
    _ = DB.QueryRow("SELECT enabled FROM config WHERE tenant = ?", "a").Scan(&enabled)
}
func ContextEntry(ctx context.Context) {
    var enabled int
    _ = DB.QueryRowContext(ctx,"SELECT enabled FROM config WHERE tenant = 'a'").Scan(&enabled)
}
`

const t19bRuntime = `{"version":2,"sources":[{"id":"flags","kind":"database"}],"bindings":[{"target":{"symbol":"symbol:go:example.com/gap:DB"},"source":"flags"}],"observations":[{"source":"flags","provenance":{"observed_at":"2026-10-28T00:00:00Z","revision":"fixture"},"resource":{"table":"config"},"scope":[{"field":"tenant","op":"eq","value":"a"}],"coverage":{"complete":false,"columns":["tenant","enabled"]},"rows":[]},{"source":"flags","provenance":{"observed_at":"2026-10-28T00:00:00Z","revision":"fixture"},"resource":{"table":"config"},"scope":[],"coverage":{"complete":true,"columns":["tenant"]},"rows":[{"tenant":"a"}]},{"source":"flags","provenance":{"observed_at":"2026-10-28T00:00:00Z","revision":"fixture"},"resource":{"table":"config"},"scope":[{"field":"tenant","op":"eq","value":"b"}],"coverage":{"complete":true,"columns":["tenant","enabled"]},"rows":[{"tenant":"b","enabled":1}]}]}`

// seedT19Gap 建 example.com/gap 夹具（真实 AST + 索引节点/调用边）。
func seedT19Gap(t *testing.T, runtimeJSON string) (*Actions, string, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module example.com/gap\n\ngo 1.26.2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "flow.go"), []byte(t19bSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	snap := filepath.Join(dir, "runtime.json")
	if err := os.WriteFile(snap, []byte(runtimeJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	pkg := "symbol:go:example.com/gap:"
	line := func(prefix string) int {
		for i, l := range strings.Split(t19bSrc, "\n") {
			if strings.Contains(l, prefix) {
				return i + 1
			}
		}
		return 0
	}
	pos := func(name string) int {
		idx := strings.Index(t19bSrc, name)
		if idx < 0 {
			return 0
		}
		return idx + strings.Index(t19bSrc[idx:], "(")
	}
	nodes := []*domain.CodeEntity{
		{ID: domain.CanonicalID(pkg + "Entry"), Kind: domain.KindFunction, Name: "Entry",
			FilePath: "flow.go", LineStart: line("func Entry(")},
		{ID: domain.CanonicalID(pkg + "ContextEntry"), Kind: domain.KindFunction, Name: "ContextEntry",
			FilePath: "flow.go", LineStart: line("func ContextEntry(")},
	}
	facts := []*domain.Fact{
		{SourceID: domain.CanonicalID(pkg + "Entry"), TargetID: "symbol:go:database/sql:(DB).QueryRow",
			Kind: domain.FactCalls, Confidence: 0.8,
			Metadata: map[string]any{"line_num": line("DB.QueryRow("), "pos": pos("DB.QueryRow(")}},
		{SourceID: domain.CanonicalID(pkg + "Entry"), TargetID: "symbol:go:database/sql:(Row).Scan",
			Kind: domain.FactCalls, Confidence: 0.8,
			Metadata: map[string]any{"line_num": line("DB.QueryRow("), "pos": pos(".Scan(&enabled)\n")}},
		{SourceID: domain.CanonicalID(pkg + "ContextEntry"), TargetID: "symbol:go:database/sql:(DB).QueryRowContext",
			Kind: domain.FactCalls, Confidence: 0.8,
			Metadata: map[string]any{"line_num": line("DB.QueryRowContext("), "pos": pos("DB.QueryRowContext(")}},
		{SourceID: domain.CanonicalID(pkg + "ContextEntry"), TargetID: "symbol:go:database/sql:(Row).Scan",
			Kind: domain.FactCalls, Confidence: 0.8,
			Metadata: map[string]any{"line_num": line("DB.QueryRowContext("), "pos": pos(".Scan(&enabled)\n")}},
	}
	if _, err := r.SaveBatchStats(nodes, facts, nil); err != nil {
		t.Fatalf("save: %v", err)
	}
	return New(r), dir, snap
}

// t19BoundaryCodes 树里全部运行时边界 code（去重）。
func t19BoundaryCodes(root *CodeSeqNode) []string {
	seen := map[string]bool{}
	var out []string
	var walk func(n *CodeSeqNode)
	walk = func(n *CodeSeqNode) {
		if n == nil {
			return
		}
		if n.Evidence != nil && n.Evidence.Runtime != nil && n.Evidence.Runtime.Boundary != nil {
			code := n.Evidence.Runtime.Boundary.Code
			if !seen[code] {
				seen[code] = true
				out = append(out, code)
			}
		}
		for _, c := range n.Nodes {
			walk(c)
		}
		for _, c := range n.Else {
			walk(c)
		}
	}
	walk(root)
	return out
}

// T19b：多条同资源/同表观测都不满足查询时，报告最接近有效匹配的缺口。
func TestT19bGapPriority(t *testing.T) {
	acts, dir, snap := seedT19Gap(t, t19bRuntime)
	for _, entry := range []string{"ContextEntry", "Entry"} {
		root, err := acts.CodeSequence(CodeSequenceRequest{
			Target: "symbol:go:example.com/gap:" + entry, RepoAbs: dir, Depth: 12,
			RuntimeDataPath: snap})
		if err != nil {
			t.Fatalf("%s: %v", entry, err)
		}
		codes := t19BoundaryCodes(root)
		if len(codes) != 1 || codes[0] != SeqRuntimeCodeScope {
			t.Fatalf("%s 缺口应为 scope_not_covered（got %v）: %v", entry, codes, labelsOf(root.Nodes))
		}
	}
}

// T19b：输入顺序不改变结论；换用有效观测时使用它；重复有效观测 → ambiguous。
func TestT19bObservationOrderAndAmbiguity(t *testing.T) {
	// 三条观测换序（scope 在前/行集在前）：仍为 scope_not_covered。
	ordered := strings.Replace(t19bRuntime,
		`"observations":[{"source":"flags","provenance":{"observed_at":"2026-10-28T00:00:00Z","revision":"fixture"},"resource":{"table":"config"},"scope":[{"field":"tenant","op":"eq","value":"a"}],"coverage":{"complete":false,"columns":["tenant","enabled"]},"rows":[]},{"source":"flags","provenance":{"observed_at":"2026-10-28T00:00:00Z","revision":"fixture"},"resource":{"table":"config"},"scope":[],"coverage":{"complete":true,"columns":["tenant"]},"rows":[{"tenant":"a"}]},{"source":"flags","provenance":{"observed_at":"2026-10-28T00:00:00Z","revision":"fixture"},"resource":{"table":"config"},"scope":[{"field":"tenant","op":"eq","value":"b"}],"coverage":{"complete":true,"columns":["tenant","enabled"]},"rows":[{"tenant":"b","enabled":1}]}]}`,
		`"observations":[{"source":"flags","provenance":{"observed_at":"2026-10-28T00:00:00Z","revision":"fixture"},"resource":{"table":"config"},"scope":[{"field":"tenant","op":"eq","value":"b"}],"coverage":{"complete":true,"columns":["tenant","enabled"]},"rows":[{"tenant":"b","enabled":1}]},{"source":"flags","provenance":{"observed_at":"2026-10-28T00:00:00Z","revision":"fixture"},"resource":{"table":"config"},"scope":[],"coverage":{"complete":true,"columns":["tenant"]},"rows":[{"tenant":"a"}]},{"source":"flags","provenance":{"observed_at":"2026-10-28T00:00:00Z","revision":"fixture"},"resource":{"table":"config"},"scope":[{"field":"tenant","op":"eq","value":"a"}],"coverage":{"complete":false,"columns":["tenant","enabled"]},"rows":[]}]}`, 1)
	acts, dir, snap := seedT19Gap(t, ordered)
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/gap:ContextEntry", RepoAbs: dir, Depth: 12,
		RuntimeDataPath: snap})
	if err != nil {
		t.Fatal(err)
	}
	if codes := t19BoundaryCodes(root); len(codes) != 1 || codes[0] != SeqRuntimeCodeScope {
		t.Fatalf("换序后应仍为 scope_not_covered: %v", codes)
	}

	// 把第三条 scope 改成 tenant=a → 有有效观测，使用它（无缺口证据）。
	valid := strings.Replace(t19bRuntime, `"value":"b"`, `"value":"a"`, 1)
	acts2, dir2, snap2 := seedT19Gap(t, valid)
	root2, err := acts2.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/gap:ContextEntry", RepoAbs: dir2, Depth: 12,
		RuntimeDataPath: snap2})
	if err != nil {
		t.Fatal(err)
	}
	if codes := t19BoundaryCodes(root2); len(codes) != 0 {
		t.Fatalf("存在有效观测时不应报缺口: %v", codes)
	}

	// 复制这条有效观测 → ambiguous_observations（不任选一条）。
	dup := strings.Replace(valid, `"observations":[`, `"observations":[`+validObservation(valid)+`,`, 1)
	acts3, dir3, snap3 := seedT19Gap(t, dup)
	root3, err := acts3.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/gap:ContextEntry", RepoAbs: dir3, Depth: 12,
		RuntimeDataPath: snap3})
	if err != nil {
		t.Fatal(err)
	}
	if codes := t19BoundaryCodes(root3); len(codes) != 1 || codes[0] != SeqRuntimeCodeAmbiguous {
		t.Fatalf("两条有效观测应为 ambiguous_observations: %v", codes)
	}
}

// validObservation 从快照里取第三条（有效）观测的 JSON 原文。
func validObservation(snap string) string {
	i := strings.LastIndex(snap, `{"source":"flags"`)
	if i < 0 {
		return ""
	}
	return snap[i : len(snap)-2] // 去掉 "]}"
}

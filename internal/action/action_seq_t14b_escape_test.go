package action

// T14b：取址逃逸持续失效（指针别名/括号/字段/下标/包级变量）与
// CodeSequenceRequest.RuntimeData 同时接受路径与已解析快照。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

const t14bPointerSrc = `package pointerflow
func Manual(){}
func Auto(){}
func mutate(p *int){*p=0}
func Run(){n:=1;p:=&n;mutate(p);if n==1{Manual()}else{Auto()}}
func ReassignAfter(){n:=1;p:=&n;mutate(p);n=1;if n==1{Manual()}else{Auto()}}
func IfBefore(){n:=1;if n==1{Manual()}else{Auto()};p:=&n;mutate(p)}
func NoAddress(){n:=1;if n==1{Manual()}else{Auto()}}
func Parenthesized(){n:=1;p:=&(n);mutate(p);if n==1{Manual()}else{Auto()}}
func FieldAddress(){s:=struct{N int}{N:1};p:=&s.N;mutate(p);if s.N==1{Manual()}else{Auto()}}
func IndexAddress(){items:=[]int{1};p:=&items[0];mutate(p);if items[0]==1{Manual()}else{Auto()}}
var G int
func GlobalAddress(){G=1;p:=&G;mutate(p);if G==1{Manual()}else{Auto()}}
`

const t14bEmptyRuntime = `{"version":2,"sources":[],"bindings":[],"observations":[]}`

func seedT14bPointer(t *testing.T) (*Actions, string, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module example.com/pointerflow\n\ngo 1.26.2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "flow.go"), []byte(t14bPointerSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	snap := filepath.Join(dir, "runtime.json")
	if err := os.WriteFile(snap, []byte(t14bEmptyRuntime), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	pkg := "symbol:go:example.com/pointerflow:"
	line := func(name string) int {
		for i, l := range strings.Split(t14bPointerSrc, "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), "func "+name+"(") {
				return i + 1
			}
		}
		return 0
	}
	var nodes []*domain.CodeEntity
	var facts []*domain.Fact
	for _, fn := range []string{"Manual", "Auto", "mutate", "Run", "ReassignAfter",
		"IfBefore", "NoAddress", "Parenthesized", "FieldAddress", "IndexAddress", "GlobalAddress"} {
		nodes = append(nodes, &domain.CodeEntity{ID: domain.CanonicalID(pkg + fn),
			Kind: domain.KindFunction, Name: fn, FilePath: "flow.go", LineStart: line(fn)})
	}
	for _, entry := range []string{"Run", "ReassignAfter", "IfBefore", "NoAddress",
		"Parenthesized", "FieldAddress", "IndexAddress", "GlobalAddress"} {
		for _, callee := range []string{"Manual", "Auto", "mutate"} {
			facts = append(facts, &domain.Fact{SourceID: domain.CanonicalID(pkg + entry),
				TargetID: domain.CanonicalID(pkg + callee), Kind: domain.FactCalls, Confidence: 0.8,
				Metadata: map[string]any{"line_num": line(entry)}})
		}
	}
	if _, err := r.SaveBatchStats(nodes, facts, nil); err != nil {
		t.Fatalf("save: %v", err)
	}
	return New(r), dir, snap
}

func t14bBranches(t *testing.T, acts *Actions, dir, snap, entry string) (int, int) {
	t.Helper()
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/pointerflow:" + entry, RepoAbs: dir, Depth: 12,
		RuntimeDataPath: snap})
	if err != nil {
		t.Fatalf("%s: %v", entry, err)
	}
	return len(seqCallNodesNamed(root, "Manual")), len(seqCallNodesNamed(root, "Auto"))
}

// 取址别名（含括号/字段/下标/包级）后持续失效；重新赋值不恢复。
func TestT14bAddressEscapePersists(t *testing.T) {
	acts, dir, snap := seedT14bPointer(t)
	for _, entry := range []string{"Run", "ReassignAfter", "Parenthesized", "FieldAddress",
		"IndexAddress", "GlobalAddress"} {
		manual, auto := t14bBranches(t, acts, dir, snap, entry)
		if manual != 1 || auto != 1 {
			t.Errorf("%s: 取址后应保持未知（两分支），got Manual=%d Auto=%d", entry, manual, auto)
		}
	}
}

// 反例：取址之前的判断不被倒推污染；没有取址时保持确定分支。
func TestT14bAddressEscapeNoBackwardContamination(t *testing.T) {
	acts, dir, snap := seedT14bPointer(t)
	for _, entry := range []string{"IfBefore", "NoAddress"} {
		manual, auto := t14bBranches(t, acts, dir, snap, entry)
		if manual != 1 || auto != 0 {
			t.Errorf("%s: 不应被取址污染（got Manual=%d Auto=%d）", entry, manual, auto)
		}
	}
}

// RuntimeData 同时接收路径与 LoadRuntimeData 的已解析对象；场景与快照 hash 相同。
func TestT14bRuntimeDataObjectEntry(t *testing.T) {
	acts, dir, snap := seedT14bPointer(t)
	data, err := LoadRuntimeData(snap)
	if err != nil {
		t.Fatal(err)
	}
	byPath, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/pointerflow:IfBefore", RepoAbs: dir, Depth: 12,
		RuntimeDataPath: snap, Evidence: true})
	if err != nil {
		t.Fatal(err)
	}
	byObj, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/pointerflow:IfBefore", RepoAbs: dir, Depth: 12,
		RuntimeData: data, Evidence: true})
	if err != nil {
		t.Fatalf("RuntimeData 对象入口: %v", err)
	}
	sp, so := t14bSceneSources(byPath), t14bSceneSources(byObj)
	if len(sp) == 0 || strings.Join(sp, "|") != strings.Join(so, "|") {
		t.Fatalf("路径与对象入口的场景来源不一致: %v vs %v", sp, so)
	}
	if byObj.Meta == nil || byObj.Meta.Evidence == nil || byObj.Meta.Evidence.RuntimeHash != data.Hash {
		t.Fatalf("对象入口应记录同一快照 hash: %+v", byObj.Meta)
	}
	// typed-nil 等同没有配置：保留原 error 分支（Entry 不存在 → 明确错误）。
	var typedNil *RuntimeSnapshot
	if _, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/pointerflow:Missing", RepoAbs: dir, Depth: 2,
		RuntimeData: typedNil}); err == nil {
		t.Fatal("typed-nil 不应触发运行时求值；缺失入口仍应报错")
	}
}

// 路径与对象同时给出：一致接受；冲突明确失败且 runtime-data 前缀只出现一次。
func TestT14bRuntimeDataConflict(t *testing.T) {
	acts, dir, snap := seedT14bPointer(t)
	data, err := LoadRuntimeData(snap)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/pointerflow:IfBefore", RepoAbs: dir, Depth: 12,
		RuntimeDataPath: snap, RuntimeData: data}); err != nil {
		t.Fatalf("一致的对象与路径应接受: %v", err)
	}
	other := filepath.Join(dir, "other.json")
	if err := os.WriteFile(other, []byte(`{"version":2,"sources":[],"bindings":[],"observations":[],"context":{"k":1}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	otherSnap, err := LoadRuntimeData(other)
	if err != nil {
		t.Fatal(err)
	}
	_, err = acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/pointerflow:IfBefore", RepoAbs: dir, Depth: 12,
		RuntimeDataPath: snap, RuntimeData: otherSnap})
	if err == nil {
		t.Fatal("不同快照的路径/对象入口应失败")
	}
	if strings.Count(err.Error(), "runtime-data:") != 1 {
		t.Fatalf("错误前缀应只出现一次: %q", err.Error())
	}
	// Runtime 与 RuntimeData 对象同时给出也必须一致。
	if _, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/pointerflow:IfBefore", RepoAbs: dir, Depth: 12,
		Runtime: otherSnap, RuntimeData: data}); err == nil {
		t.Fatal("两个不同的已解析快照应失败")
	}
}

// t14bSceneSources 树里运行时分支依据来源（含快照 hash 前缀）。
func t14bSceneSources(root *CodeSeqNode) []string {
	var out []string
	var walk func(n *CodeSeqNode)
	walk = func(n *CodeSeqNode) {
		if n == nil {
			return
		}
		if n.Scene != nil && n.Scene.Source != "" {
			out = append(out, n.Scene.Source)
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

// SQL 字符串指针别名使旧 SELECT 失效；未逃逸对照组仍用快照；同名局部隔离。
func TestT14bSQLStringAliasAndShadow(t *testing.T) {
	src := `package flow

import (
	"context"
	"database/sql"
)

var DB *sql.DB

func mutateStr(p *string) { *p = "SELECT 1" }

func SQLAlias(ctx context.Context) {
	q := "SELECT enabled FROM review_config WHERE region = ? AND node_type = ?"
	p := &q
	mutateStr(p)
	var enabled int
	if err := DB.QueryRowContext(ctx, q, "ID", 2).Scan(&enabled); err != nil {
		return
	}
	if enabled == 1 {
		Manual()
	} else {
		Auto()
	}
}

func SQLNoAlias(ctx context.Context) {
	q := "SELECT enabled FROM review_config WHERE region = ? AND node_type = ?"
	var enabled int
	if err := DB.QueryRowContext(ctx, q, "ID", 2).Scan(&enabled); err != nil {
		return
	}
	if enabled == 1 {
		Manual()
	} else {
		Auto()
	}
}

func mutate(p *int) { *p = 0 }

func ShadowEscape() {
	n := 1
	{
		n := 2
		p := &n
		mutate(p)
		if n == 2 {
			Manual()
		} else {
			Auto()
		}
	}
	if n == 1 {
		Manual()
	} else {
		Auto()
	}
}
`
	acts, dir, snap := seedT14b(t, src)
	// 未逃逸：快照 enabled=1 → 只走 Manual。
	manual, auto := t14bBranchesModule(t, acts, dir, snap, "example.com/flow", "SQLNoAlias")
	if manual != 1 || auto != 0 {
		t.Fatalf("SQLNoAlias 应使用快照（Manual=%d Auto=%d）", manual, auto)
	}
	// SQL 字符串取址逃逸：保持 unknown（两分支），不套旧 SELECT 的值。
	manual, auto = t14bBranchesModule(t, acts, dir, snap, "example.com/flow", "SQLAlias")
	if manual != 1 || auto != 1 {
		t.Fatalf("SQLAlias 应保持 unknown（Manual=%d Auto=%d）", manual, auto)
	}
	// 同名局部隔离：内层失效、外层确定。
	manual, auto = t14bBranchesModule(t, acts, dir, snap, "example.com/flow", "ShadowEscape")
	if manual != 2 || auto != 1 {
		t.Fatalf("ShadowEscape 内层失效/外层确定（Manual=%d Auto=%d，want 2/1）", manual, auto)
	}
}

func t14bBranchesModule(t *testing.T, acts *Actions, dir, snap, module, entry string) (int, int) {
	t.Helper()
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:" + module + ":" + entry, RepoAbs: dir, Depth: 12,
		RuntimeDataPath: snap})
	if err != nil {
		t.Fatalf("%s: %v", entry, err)
	}
	return len(seqCallNodesNamed(root, "Manual")), len(seqCallNodesNamed(root, "Auto"))
}

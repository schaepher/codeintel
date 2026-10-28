package action

// T14：链式接收者调用按准确目标展开（DB.QueryRowContext → Scan）；运行时
// 快照下 `rewrite(&enabled)` 使标量证明失效（两个业务分支都保留）。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

const t14Src = `package flow
import("context";"database/sql")
var DB *sql.DB
func Config(ctx context.Context)(int,error){
 var enabled int
 err:=DB.QueryRowContext(ctx,"SELECT enabled FROM review_config WHERE region = ? AND node_type = ?","ID",2).Scan(&enabled)
 return enabled,err
}
func Manual(){}
func Auto(){}
func rewrite(v *int){*v=0}
func Mutate(ctx context.Context){
 enabled,err:=Config(ctx);if err!=nil{return}
 rewrite(&enabled)
 if enabled==1{Manual()}else{Auto()}
}
`

const t14Runtime = `{"version":1,"environment":"test","region":"ID","captured_at":"2026-10-28T00:00:00Z","bindings":[{"symbol":"symbol:go:example.com/flow:DB","database":"review"}],"databases":[{"id":"review","tables":[{"name":"review_config","scope":{"region":"ID","node_type":2},"complete":true,"rows":[{"region":"ID","node_type":2,"enabled":1}]}]}]}`

func seedT14(t *testing.T) (*Actions, string, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/flow\n\ngo 1.26.2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "flow.go"), []byte(t14Src), 0o644); err != nil {
		t.Fatal(err)
	}
	snap := filepath.Join(dir, "runtime.json")
	if err := os.WriteFile(snap, []byte(t14Runtime), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	pkg := "symbol:go:example.com/flow:"
	nodes := []*domain.CodeEntity{
		{ID: domain.CanonicalID(pkg + "Config"), Kind: domain.KindFunction, Name: "Config", FilePath: "flow.go", LineStart: 4},
		{ID: domain.CanonicalID(pkg + "rewrite"), Kind: domain.KindFunction, Name: "rewrite", FilePath: "flow.go", LineStart: 9},
		{ID: domain.CanonicalID(pkg + "Manual"), Kind: domain.KindFunction, Name: "Manual", FilePath: "flow.go", LineStart: 10},
		{ID: domain.CanonicalID(pkg + "Auto"), Kind: domain.KindFunction, Name: "Auto", FilePath: "flow.go", LineStart: 11},
		{ID: domain.CanonicalID(pkg + "Mutate"), Kind: domain.KindFunction, Name: "Mutate", FilePath: "flow.go", LineStart: 12},
		{ID: "symbol:go:database/sql:(DB).QueryRowContext", Kind: domain.KindMethod,
			Name: "(DB).QueryRowContext", Properties: map[string]any{"signature": "func (db *DB) QueryRowContext(ctx context.Context, query string, args ...any) *Row"}},
		{ID: "symbol:go:database/sql:(Row).Scan", Kind: domain.KindMethod, Name: "(Row).Scan"},
	}
	facts := []*domain.Fact{
		{SourceID: domain.CanonicalID(pkg + "Mutate"), TargetID: domain.CanonicalID(pkg + "Config"),
			Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": 14}},
		{SourceID: domain.CanonicalID(pkg + "Mutate"), TargetID: domain.CanonicalID(pkg + "rewrite"),
			Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": 15}},
		{SourceID: domain.CanonicalID(pkg + "Mutate"), TargetID: domain.CanonicalID(pkg + "Manual"),
			Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": 16}},
		{SourceID: domain.CanonicalID(pkg + "Mutate"), TargetID: domain.CanonicalID(pkg + "Auto"),
			Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": 16}},
		{SourceID: domain.CanonicalID(pkg + "Config"), TargetID: "symbol:go:database/sql:(DB).QueryRowContext",
			Kind: domain.FactCalls, Confidence: 0.8,
			Metadata: map[string]any{"line_num": 6, "pos": t14Pos(t, "QueryRowContext")}},
		{SourceID: domain.CanonicalID(pkg + "Config"), TargetID: "symbol:go:database/sql:(Row).Scan",
			Kind: domain.FactCalls, Confidence: 0.8,
			Metadata: map[string]any{"line_num": 6, "pos": t14Pos(t, "Scan")}},
	}
	if _, err := r.SaveBatchStats(nodes, facts, nil); err != nil {
		t.Fatalf("save: %v", err)
	}
	return New(r), dir, snap
}

// t14Pos 目标调用左括号的文件偏移（与索引边的 pos 口径一致）。
func t14Pos(t *testing.T, name string) int {
	t.Helper()
	idx := strings.Index(t14Src, name)
	if idx < 0 {
		t.Fatalf("fixture 缺 %s", name)
	}
	open := strings.Index(t14Src[idx:], "(")
	return idx + open
}

func TestCodeSequenceChainedReceiverCall(t *testing.T) {
	acts, dir, _ := seedT14(t)
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "symbol:go:example.com/flow:Config",
		RepoAbs: dir, Depth: 3})
	if err != nil {
		t.Fatal(err)
	}
	var labels []string
	for _, n := range root.Nodes {
		if n.Kind == "call" {
			labels = append(labels, n.Label)
		}
	}
	joined := strings.Join(labels, " | ")
	if !strings.Contains(joined, "DB.QueryRowContext") {
		t.Fatalf("链式接收者调用应展开: %v", labels)
	}
	if strings.Index(joined, "DB.QueryRowContext") > strings.Index(joined, ".Scan") {
		t.Errorf("接收者调用应先于外层 Scan: %v", labels)
	}
	for _, n := range root.Nodes {
		if n.Kind == "call" && n.Label == "DB.QueryRowContext" {
			if n.Type != "sql.DB" {
				t.Errorf("DB 参与者类型 = %q, want sql.DB（接收者身份）", n.Type)
			}
		}
	}
}

func TestCodeSequenceRuntimeInvalidatesAcrossPointerWrite(t *testing.T) {
	acts, dir, snap := seedT14(t)
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/flow:Mutate", RepoAbs: dir, Depth: 3,
		RuntimeDataPath: snap})
	if err != nil {
		t.Fatal(err)
	}
	manual := len(seqCallNodesNamed(root, "Manual"))
	auto := len(seqCallNodesNamed(root, "Auto"))
	if manual != 1 || auto != 1 {
		t.Fatalf("rewrite(&enabled) 后两个业务分支都应保留: Manual=%d Auto=%d", manual, auto)
	}
	for _, n := range seqFindNodes(root, func(n *CodeSeqNode) bool {
		return n.Kind == "branch" && n.Scene != nil && n.Scene.Status == SceneTaken
	}) {
		t.Errorf("指针改写后不得沿用旧标量证明（不应 taken）: %+v", n.Scene)
	}
}

// 兼容入口：LoadRuntimeData 与 LoadRuntimeSnapshot 同一引擎（同 hash）。
func TestRuntimeDataCompatibilityEntry(t *testing.T) {
	_, _, snap := seedT14(t)
	a, err := LoadRuntimeData(snap)
	if err != nil {
		t.Fatalf("LoadRuntimeData: %v", err)
	}
	b, err := LoadRuntimeSnapshot(snap)
	if err != nil {
		t.Fatalf("LoadRuntimeSnapshot: %v", err)
	}
	if a.Hash != b.Hash || len(a.Observations) != len(b.Observations) {
		t.Fatalf("兼容入口与统一引擎结果不一致: %+v vs %+v", a.Hash, b.Hash)
	}
}

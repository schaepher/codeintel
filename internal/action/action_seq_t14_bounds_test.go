package action

// T14 剩余边界：多列 SELECT/Scan（数量/顺序/类型）、标量多行冲突、相同重复行、
// 未证明语义的 SQL 形态拒绝套值、*sql.DB 声明证明。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

const t14bRuntime = `{"version":1,"environment":"test","region":"ID","captured_at":"2026-10-28T00:00:00Z","bindings":[{"symbol":"symbol:go:example.com/flow:DB","database":"review"}],"databases":[{"id":"review","tables":[{"name":"review_config","scope":{"region":"ID","node_type":2},"complete":true,"rows":[{"region":"ID","node_type":2,"enabled":1}]}]}]}`

// seedT14b 单文件夹具：Entry + Config + rewrite/其他辅助函数。
func seedT14b(t *testing.T, src string) (*Actions, string, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/flow\n\ngo 1.26.2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "flow.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	snap := filepath.Join(dir, "runtime.json")
	if err := os.WriteFile(snap, []byte(t14bRuntime), 0o644); err != nil {
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
	var nodes []*domain.CodeEntity
	var facts []*domain.Fact
	for _, name := range []string{"Config", "Manual", "Auto", "Failure", "Entry", "Other"} {
		if !strings.Contains(src, "func "+name+"(") {
			continue
		}
		nodes = append(nodes, &domain.CodeEntity{ID: domain.CanonicalID(pkg + name),
			Kind: domain.KindFunction, Name: name, FilePath: "flow.go", LineStart: line("func " + name + "(")})
	}
	// 调用边：Entry → Config/Manual/Auto/Failure（行号取首次出现）
	for _, callee := range []string{"Config", "Manual", "Auto", "Failure"} {
		ln := line(callee + "(")
		if ln == 0 {
			continue
		}
		facts = append(facts, &domain.Fact{SourceID: domain.CanonicalID(pkg + "Entry"),
			TargetID: domain.CanonicalID(pkg + callee), Kind: domain.FactCalls,
			Confidence: 0.8, Metadata: map[string]any{"line_num": ln}})
	}
	if _, err := r.SaveBatchStats(nodes, facts, nil); err != nil {
		t.Fatalf("save: %v", err)
	}
	return New(r), dir, snap
}

// 多列 SELECT/Scan：按列顺序写入多个目标，返回第二列参与分支。
func TestT14MultiColumnScan(t *testing.T) {
	src := `package flow

import (
	"context"
	"database/sql"
)

var DB *sql.DB

func Config(ctx context.Context) (int, int, error) {
	var enabled, seen int
	err := DB.QueryRowContext(ctx, "SELECT enabled, node_type FROM review_config WHERE region = ? AND node_type = ?", "ID", 2).
		Scan(&enabled, &seen)
	return enabled, seen, err
}

func Manual()  {}
func Auto()    {}
func Failure() {}

func Entry(ctx context.Context) {
	enabled, seen, err := Config(ctx)
	if err != nil {
		Failure()
		return
	}
	_ = enabled
	if seen == 2 {
		Manual()
	} else {
		Auto()
	}
}
`
	acts, dir, snap := seedT14b(t, src)
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/flow:Entry", RepoAbs: dir, Depth: 6, RuntimeDataPath: snap})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(seqCallNodesNamed(root, "Manual")); got != 1 {
		t.Fatalf("第二列 seen=2 应走 Manual（got %d）: %v", got, labelsOf(root.Nodes))
	}
	if got := len(seqCallNodesNamed(root, "Auto")); got != 0 {
		t.Errorf("不应走 Auto（got %d）", got)
	}
	if got := len(seqCallNodesNamed(root, "Failure")); got != 1 {
		t.Errorf("错误分支应保留（Failure=%d）", got)
	}
}

// Scan 目标类型不兼容（第二列写入 string）→ 不裁剪业务分支。
func TestT14ScanTypeMismatchKeepsBranches(t *testing.T) {
	src := `package flow

import (
	"context"
	"database/sql"
)

var DB *sql.DB

func Config(ctx context.Context) (int, string, error) {
	var enabled int
	var seen string
	err := DB.QueryRowContext(ctx, "SELECT enabled, node_type FROM review_config WHERE region = ? AND node_type = ?", "ID", 2).
		Scan(&enabled, &seen)
	return enabled, seen, err
}

func Manual()  {}
func Auto()    {}
func Failure() {}

func Entry(ctx context.Context) {
	enabled, seen, err := Config(ctx)
	if err != nil {
		Failure()
		return
	}
	_ = seen
	if enabled == 1 {
		Manual()
	} else {
		Auto()
	}
}
`
	acts, dir, snap := seedT14b(t, src)
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/flow:Entry", RepoAbs: dir, Depth: 6, RuntimeDataPath: snap})
	if err != nil {
		t.Fatal(err)
	}
	if len(seqCallNodesNamed(root, "Manual")) != 1 || len(seqCallNodesNamed(root, "Auto")) != 1 {
		t.Errorf("类型不兼容不应裁剪业务分支: %v", labelsOf(root.Nodes))
	}
}

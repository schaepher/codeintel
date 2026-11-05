//go:build integration

package integration

// todo30 U04d：标准 SQL 读取结果身份及返回槽传播——真实源码 → init → CLI。
// 验收：ReadNode 的 return 槽携带 read-1/Row/QueryRowContext/slot=0；覆盖、
// 取址、星号投影、自定义同名方法不保留身份；辅助函数两次调用身份不同。

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

const u30ReadSrc = `package flow
import ("context"; "database/sql")
var DB *sql.DB
type fakeDB struct{}
func (*fakeDB) QueryRowContext(context.Context, string, ...any) *sql.Row { return nil }
var Fake fakeDB

func ReadNode(ctx context.Context, id string) *sql.Row {
	row := DB.QueryRowContext(ctx, "SELECT kind FROM nodes WHERE id=?", id)
	alias := row
	return alias
}
func Overwrite(ctx context.Context, id string) *sql.Row {
	row := DB.QueryRowContext(ctx, "SELECT kind FROM nodes WHERE id=?", id)
	row = nil
	return row
}
func Escaped(ctx context.Context, id string) *sql.Row {
	row := DB.QueryRowContext(ctx, "SELECT kind FROM nodes WHERE id=?", id)
	p := &row
	_ = p
	return row
}
func Star(ctx context.Context, id string) *sql.Row {
	row := DB.QueryRowContext(ctx, "SELECT * FROM nodes WHERE id=?", id)
	return row
}
func Custom(ctx context.Context, id string) *sql.Row {
	row := Fake.QueryRowContext(ctx, "SELECT kind FROM nodes WHERE id=?", id)
	return row
}
func open(ctx context.Context, id string) *sql.Row {
	return DB.QueryRowContext(ctx, "SELECT kind FROM nodes WHERE id=?", id)
}
func Two(ctx context.Context) (*sql.Row, *sql.Row) {
	return open(ctx, "a"), open(ctx, "b")
}
`

func u30ReadRepo(t *testing.T) string {
	t.Helper()
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/flow\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), u30ReadSrc)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

// u30ReadResults 入口 → return 槽 read_result 列表 + evidence.storage_read.result 列表。
func u30ReadResults(t *testing.T, dir, entry, depth, segments string) (returns, reads []map[string]any) {
	t.Helper()
	args := []string{"query", "sequence", "symbol:go:example.com/flow:" + entry,
		"--repo", dir, "--code", "--depth", depth, "--json"}
	if segments != "" {
		args = append(args, "--continue-segments", segments)
	}
	code, out := runCLIOut(t, args...)
	if code != 0 {
		t.Fatalf("%s exit=%d\n%s", entry, code, out)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("%s JSON: %v\n%s", entry, err, out)
	}
	var walk func(nodes []any)
	walk = func(nodes []any) {
		for _, raw := range nodes {
			n, _ := raw.(map[string]any)
			if n["Kind"] == "return" {
				origins, _ := n["value_origins"].([]any)
				for _, o := range origins {
					om, _ := o.(map[string]any)
					if rr, _ := om["read_result"].(map[string]any); rr != nil {
						returns = append(returns, rr)
					}
				}
			}
			if ev, _ := n["evidence"].(map[string]any); ev != nil {
				if sr, _ := ev["storage_read"].(map[string]any); sr != nil {
					if rr, _ := sr["result"].(map[string]any); rr != nil {
						reads = append(reads, rr)
					}
				}
			}
			if sub, ok := n["Nodes"].([]any); ok {
				walk(sub)
			}
			if sub, ok := n["Else"].([]any); ok {
				walk(sub)
			}
		}
	}
	nodes, _ := root["Nodes"].([]any)
	walk(nodes)
	return returns, reads
}

func TestU30U04dReadResultIdentity(t *testing.T) {
	dir := u30ReadRepo(t)
	returns, _ := u30ReadResults(t, dir, "ReadNode", "12", "")
	if len(returns) != 1 {
		t.Fatalf("ReadNode return read_result 数 = %d（want 1）: %v", len(returns), returns)
	}
	r := returns[0]
	if r["id"] != "read-1" || r["type"] != "Row" ||
		r["symbol"] != "symbol:go:database/sql:(DB).QueryRowContext" || r["slot"] != float64(0) {
		t.Fatalf("读取结果身份不符: %v", r)
	}

	// 覆盖 / 取址 / 星号 / 自定义同名方法 → 不保留读取身份。
	for _, entry := range []string{"Overwrite", "Escaped", "Star", "Custom"} {
		if returns, _ := u30ReadResults(t, dir, entry, "12", ""); len(returns) != 0 {
			t.Fatalf("%s 不应保留 return read_result: %v", entry, returns)
		}
	}

	// 辅助函数两次调用 → 两个读取身份。
	_, reads := u30ReadResults(t, dir, "Two", "12", "")
	seen := map[string]bool{}
	for _, r := range reads {
		seen[r["id"].(string)] = true
	}
	if !seen["read-1"] || !seen["read-2"] {
		t.Fatalf("两次调用应有两个读取身份（read-1/read-2）: %v", reads)
	}

	// depth=1 + 续查与完整深度：返回槽身份一致。
	full, _ := u30ReadResults(t, dir, "ReadNode", "12", "")
	shallow, _ := u30ReadResults(t, dir, "ReadNode", "1", "32")
	if len(full) != 1 || len(shallow) != 1 ||
		full[0]["id"] != shallow[0]["id"] || full[0]["symbol"] != shallow[0]["symbol"] ||
		full[0]["slot"] != shallow[0]["slot"] || full[0]["type"] != shallow[0]["type"] {
		t.Fatalf("depth1+续查 与 depth12 读取身份不一致:\nfull=%v\nshallow=%v", full, shallow)
	}
}

//go:build integration

package integration

// todo28 U04m：SQL Result.RowsAffected 关联准确的原始写入（真实源码 → init → CLI）。

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

const u04mSrc = `package sqlresult
import "database/sql"
var DB *sql.DB
func Hit() {}
func Entry() {
 result, err := DB.Exec("UPDATE nodes SET state=? WHERE id=?", 1, 2)
 if err != nil { return }
 rows, countErr := result.RowsAffected()
 if countErr != nil { return }
 if rows > 0 { Hit() }
}
func Alias() {
 result, err := DB.Exec("UPDATE nodes SET state=? WHERE id=?", 1, 2)
 if err != nil { return }
 alias := result
 rows, countErr := alias.RowsAffected()
 if countErr != nil { return }
 if rows > 0 { Hit() }
}
func UnknownParam(r sql.Result) { rows, _ := r.RowsAffected(); if rows > 0 { Hit() } }
func Overwritten() {
 result, err := DB.Exec("UPDATE nodes SET state=? WHERE id=?", 1, 2)
 if err != nil { return }
 result = nil
 rows, _ := result.RowsAffected()
 if rows > 0 { Hit() }
}
`

func u04mFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/sqlresult\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), u04mSrc)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

// u04mConditions 查询入口 → row_count_condition 投影 + 写入错误身份。
func u04mConditions(t *testing.T, dir, entry string) (conds []map[string]any, writeID any) {
	t.Helper()
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/sqlresult:"+entry,
		"--repo", dir, "--code", "--depth", "1", "--continue-segments", "32",
		"--evidence", "--json")
	if code != 0 {
		t.Fatalf("query %s exit=%d\n%s", entry, code, out)
	}
	var res struct {
		Nodes []map[string]any `json:"Nodes"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("json: %v", err)
	}
	var walk func(n map[string]any)
	walk = func(n map[string]any) {
		if rc, ok := n["row_count_condition"].(map[string]any); ok {
			item := map[string]any{"when_true": rc["when_true"], "when_false": rc["when_false"]}
			if r, ok := rc["result"].(map[string]any); ok {
				item["slot"] = r["slot"]
				if a, ok := r["access"].(map[string]any); ok {
					item["symbol"] = a["symbol"]
					item["read_error"] = a["error_outcome"]
				}
				if w, ok := r["write"].(map[string]any); ok {
					item["call_id"] = w["call_id"]
				}
			}
			conds = append(conds, item)
		}
		if ev, ok := n["evidence"].(map[string]any); ok {
			if st, ok := ev["storage"].(map[string]any); ok {
				if er, ok := st["error_result"].(map[string]any); ok {
					writeID = er["call_id"]
				}
			}
		}
		for _, key := range []string{"Nodes", "Else"} {
			if arr, ok := n[key].([]any); ok {
				for _, c := range arr {
					walk(c.(map[string]any))
				}
			}
		}
	}
	for _, n := range res.Nodes {
		walk(n)
	}
	return conds, writeID
}

// TestU04mRowsAffectedProjection E-U04m 投影（深浅续查一致）。
func TestU04mRowsAffectedProjection(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04mFixture(t)
	conds, writeID := u04mConditions(t, dir, "Entry")
	if len(conds) != 1 {
		t.Fatalf("conditions = %+v", conds)
	}
	c := conds[0]
	if c["symbol"] != "symbol:go:database/sql:(Result).RowsAffected" || c["slot"] != float64(0) ||
		c["read_error"] != "unproven" || c["when_true"] != "rows_gt_zero" ||
		c["when_false"] != "rows_le_zero" {
		t.Fatalf("condition: %+v", c)
	}
	if c["call_id"] != writeID {
		t.Fatalf("计数条件应引用同一写入: cond=%v write=%v", c["call_id"], writeID)
	}
	// depth=12 同口径。
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/sqlresult:Entry",
		"--repo", dir, "--code", "--depth", "12", "--continue-segments", "32", "--evidence", "--json")
	if code != 0 {
		t.Fatalf("depth12 exit=%d\n%s", code, out)
	}
	var res struct {
		Nodes []map[string]any `json:"Nodes"`
	}
	json.Unmarshal([]byte(out), &res)
	found := false
	var walk func(n map[string]any)
	walk = func(n map[string]any) {
		if _, ok := n["row_count_condition"]; ok {
			found = true
		}
		for _, key := range []string{"Nodes", "Else"} {
			if arr, ok := n[key].([]any); ok {
				for _, c := range arr {
					walk(c.(map[string]any))
				}
			}
		}
	}
	for _, n := range res.Nodes {
		walk(n)
	}
	if !found {
		t.Fatalf("depth12 缺少 row_count_condition")
	}
}

// TestU04mAliasAndRejections 别名保留；未知/覆写不沿用旧写入。
func TestU04mAliasAndRejections(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04mFixture(t)
	if conds, _ := u04mConditions(t, dir, "Alias"); len(conds) != 1 {
		t.Fatalf("Alias conditions = %+v", conds)
	}
	for _, entry := range []string{"UnknownParam", "Overwritten"} {
		if conds, _ := u04mConditions(t, dir, entry); len(conds) != 0 {
			t.Fatalf("%s 不应有行数条件: %+v", entry, conds)
		}
	}
}

// TestU04mMermaidNote 图内影响行数注记（含 access 身份）。
func TestU04mMermaidNote(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04mFixture(t)
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/sqlresult:Entry",
		"--repo", dir, "--code", "--depth", "1", "--continue-segments", "32", "--format", "mermaid")
	if code != 0 {
		t.Fatalf("query exit=%d\n%s", code, out)
	}
	for _, want := range []string{"写入影响行数 write-1 via (Result).RowsAffected[0]",
		"then=rows_gt_zero", "计数读取错误与事务提交未证明"} {
		if !strings.Contains(out, want) {
			t.Fatalf("缺少 %q:\n%s", want, out)
		}
	}
}

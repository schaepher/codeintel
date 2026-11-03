//go:build integration

package integration

// todo28 U04n：标准 SQL 事务身份、操作与独立错误槽（真实源码 → init → CLI）。

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

const u04nSrc = `package txflow
import "database/sql"
var DB *sql.DB
func Entry() error {
 tx, err := DB.Begin()
 if err != nil { return err }
 defer tx.Rollback()
 _, err = tx.Exec("UPDATE nodes SET state=? WHERE id=?", 1, 2)
 if err != nil { return err }
 err = tx.Commit()
 if err != nil { return err }
 return nil
}
func Start() (*sql.Tx, error) { return DB.Begin() }
func Finish(tx *sql.Tx) error {
 err := tx.Commit()
 if err != nil { return err }
 return nil
}
func WrappedEntry() error {
 tx, err := Start()
 if err != nil { return err }
 err = Finish(tx)
 if err != nil { return err }
 return nil
}
`

func u04nFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/txflow\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), u04nSrc)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

func u04nQuery(t *testing.T, dir, entry, depth, segments string) (events, conds []map[string]any, out string) {
	t.Helper()
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/txflow:"+entry,
		"--repo", dir, "--code", "--depth", depth, "--continue-segments", segments,
		"--evidence", "--json")
	if code != 0 {
		t.Fatalf("query %s exit=%d\n%s", entry, code, out)
	}
	var res struct {
		Nodes []map[string]any `json:"Nodes"`
		Else  []map[string]any `json:"Else"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("json: %v", err)
	}
	var walk func(n map[string]any)
	walk = func(n map[string]any) {
		if ev, ok := n["evidence"].(map[string]any); ok {
			if tr, ok := ev["transaction"].(map[string]any); ok {
				item := map[string]any{"action": tr["action"], "exec": tr["exec"], "outcome": tr["outcome"]}
				if o, ok := tr["origin"].(map[string]any); ok {
					item["tx"] = o["id"]
				}
				if b, ok := tr["boundary"]; ok {
					item["boundary"] = b
				}
				events = append(events, item)
			}
		}
		if tc, ok := n["transaction_condition"].(map[string]any); ok {
			item := map[string]any{"when_true": tc["when_true"], "when_false": tc["when_false"]}
			if r, ok := tc["result"].(map[string]any); ok {
				item["operation"] = r["operation"]
				item["slot"] = r["slot"]
				item["call_id"] = r["call_id"]
			}
			conds = append(conds, item)
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
	return events, conds, out
}

// TestU04nTransactionEvents E-U04n 事件投影（源码顺序、tx-1、错误槽）。
func TestU04nTransactionEvents(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04nFixture(t)
	events, conds, _ := u04nQuery(t, dir, "Entry", "1", "32")
	want := []struct{ action, exec string }{
		{"begin", ""}, {"rollback", "defer"}, {"write", ""}, {"commit", ""},
	}
	if len(events) != len(want) {
		t.Fatalf("events = %+v", events)
	}
	for i, w := range want {
		if events[i]["action"] != w.action || events[i]["tx"] != "tx-1" ||
			events[i]["outcome"] != "unproven" {
			t.Fatalf("event[%d] = %+v（want %+v）", i, events[i], w)
		}
		if w.exec != "" && events[i]["exec"] != w.exec {
			t.Fatalf("event[%d] exec = %+v（want %s）", i, events[i]["exec"], w.exec)
		}
	}
	if len(conds) != 2 {
		t.Fatalf("conditions = %+v", conds)
	}
	if conds[0]["operation"] != "begin" || conds[0]["slot"] != float64(1) ||
		conds[0]["when_true"] != "error_non_nil" || conds[0]["when_false"] != "error_nil" {
		t.Fatalf("begin condition = %+v", conds[0])
	}
	if conds[1]["operation"] != "commit" || conds[1]["slot"] != float64(0) {
		t.Fatalf("commit condition = %+v", conds[1])
	}
	if conds[0]["call_id"] == conds[1]["call_id"] {
		t.Fatalf("不同事务操作必须有不同身份: %+v", conds)
	}
	// depth=12 同口径。
	events12, _, _ := u04nQuery(t, dir, "Entry", "12", "32")
	if len(events12) != len(events) {
		t.Fatalf("depth12 events = %+v", events12)
	}
}

// TestU04nWrapper 包装器（Start/Finish）传递事务与错误槽。
func TestU04nWrapper(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04nFixture(t)
	events, conds, _ := u04nQuery(t, dir, "WrappedEntry", "12", "32")
	var actions []string
	for _, e := range events {
		actions = append(actions, e["action"].(string))
	}
	joined := strings.Join(actions, ",")
	if !strings.Contains(joined, "begin") || !strings.Contains(joined, "commit") {
		t.Fatalf("包装器应包含 begin/commit: %v", actions)
	}
	if len(conds) < 2 {
		t.Fatalf("包装器应保留 Begin/Commit 条件: %+v", conds)
	}
}

// TestU04nMermaid 文本/Mermaid 事务注记。
func TestU04nMermaid(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04nFixture(t)
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/txflow:Entry",
		"--repo", dir, "--code", "--depth", "1", "--continue-segments", "32", "--format", "mermaid")
	if code != 0 {
		t.Fatalf("query exit=%d\n%s", code, out)
	}
	for _, want := range []string{"事务 tx-1 begin（outcome=unproven）", "事务 tx-1 write（outcome=unproven）",
		"事务 tx-1 commit（outcome=unproven）", "事务 tx-1 rollback（outcome=unproven）", "执行结果未证明"} {
		if !strings.Contains(out, want) {
			t.Fatalf("缺少 %q:\n%s", want, out)
		}
	}
}

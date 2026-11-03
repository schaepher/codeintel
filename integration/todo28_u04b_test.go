//go:build integration

package integration

// todo28 U04b：SQL 写入的结构化证据（真实源码 → init → CLI）。
//
// 覆盖：UPDATE 的 SET 占位符在 WHERE 之前编号、SQL 字符串内逗号不切列、
// 局部 DB 资源边界、动态 SQL query_not_proven、占位符与实参数量不符。

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

const u04bSrc = `package storageflow
import "database/sql"
var DB *sql.DB
func Save(id string) {
 DB.Exec("UPDATE nodes SET kind=? WHERE id=? AND region='ID'", "service", id)
}
func Insert(id string) { DB.Exec("INSERT INTO nodes(id,kind) VALUES(?,?)", id, "a,b") }
func Local(db *sql.DB) { db.Exec("INSERT INTO nodes(id) VALUES(?)", "n") }
func Dynamic(query string) { DB.Exec(query, "INSERT INTO nodes(id) VALUES(?)") }
func Mismatch(id string) { DB.Exec("INSERT INTO nodes(id,kind) VALUES(?,?)", id) }
`

func u04bFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/storageflow\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), u04bSrc)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

// u04bStorage 投影出本次时序里的 storage 证据（按 evidence.symbol_id 过滤）。
func u04bStorage(t *testing.T, dir, entry, symbol string) []map[string]any {
	t.Helper()
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/storageflow:"+entry,
		"--repo", dir, "--code", "--depth", "12", "--evidence", "--json")
	if code != 0 {
		t.Fatalf("query exit=%d\n%s", code, out)
	}
	var root any
	if err := json.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	var found []map[string]any
	var walk func(v any)
	walk = func(v any) {
		switch n := v.(type) {
		case map[string]any:
			if ev, ok := n["evidence"].(map[string]any); ok {
				if id, _ := ev["symbol_id"].(string); id == symbol {
					if st, ok := ev["storage"].(map[string]any); ok {
						found = append(found, st)
					}
				}
			}
			for _, c := range n {
				walk(c)
			}
		case []any:
			for _, c := range n {
				walk(c)
			}
		}
	}
	walk(root)
	return found
}

// TestU04bStorageUpdateProjection E-U04b 的 UPDATE 投影（含资源声明）。
func TestU04bStorageUpdateProjection(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04bFixture(t)
	got := u04bStorage(t, dir, "Save", "symbol:go:database/sql:(DB).Exec")
	if len(got) != 1 {
		t.Fatalf("storage 数量 = %d: %+v", len(got), got)
	}
	st := got[0]
	if st["operation"] != "update" || st["table"] != "nodes" {
		t.Fatalf("operation/table: %+v", st)
	}
	writes, _ := st["writes"].([]any)
	if len(writes) != 1 {
		t.Fatalf("writes: %+v", writes)
	}
	w := writes[0].(map[string]any)
	if w["column"] != "kind" {
		t.Fatalf("write column: %+v", w)
	}
	wv := w["value"].(map[string]any)
	if wv["kind"] != "argument" || wv["argument"] != float64(0) || wv["expression"] != `"service"` {
		t.Fatalf("write value: %+v", wv)
	}
	preds, _ := st["predicates"].([]any)
	if len(preds) != 2 {
		t.Fatalf("predicates: %+v", preds)
	}
	p0 := preds[0].(map[string]any)
	p0v := p0["value"].(map[string]any)
	if p0["column"] != "id" || p0v["argument"] != float64(1) || p0v["expression"] != "id" {
		t.Fatalf("predicate[0]: %+v", p0)
	}
	p1 := preds[1].(map[string]any)
	p1v := p1["value"].(map[string]any)
	if p1["column"] != "region" || p1v["kind"] != "literal" || p1v["argument"] != float64(-1) || p1v["sql"] != "'ID'" {
		t.Fatalf("predicate[1]: %+v", p1)
	}
	if st["resource_declaration"] != "symbol:go:example.com/storageflow:DB" {
		t.Fatalf("resource_declaration: %+v", st["resource_declaration"])
	}
	if st["outcome"] != "unproven" || st["boundary"] != "record_identity_unproven" {
		t.Fatalf("outcome/boundary: %+v", st)
	}
}

// TestU04bStorageInsertCommaString 字符串内逗号不切列。
func TestU04bStorageInsertCommaString(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04bFixture(t)
	got := u04bStorage(t, dir, "Insert", "symbol:go:database/sql:(DB).Exec")
	if len(got) != 1 {
		t.Fatalf("storage 数量 = %d", len(got))
	}
	writes := got[0]["writes"].([]any)
	if len(writes) != 2 {
		t.Fatalf("writes: %+v", writes)
	}
	if writes[1].(map[string]any)["column"] != "kind" {
		t.Fatalf("columns: %+v", writes)
	}
	if writes[1].(map[string]any)["value"].(map[string]any)["expression"] != `"a,b"` {
		t.Fatalf("expression: %+v", writes[1])
	}
}

// TestU04bStorageLocalResource 局部参数 DB：无 resource_declaration。
func TestU04bStorageLocalResource(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04bFixture(t)
	got := u04bStorage(t, dir, "Local", "symbol:go:database/sql:(DB).Exec")
	if len(got) != 1 {
		t.Fatalf("storage 数量 = %d", len(got))
	}
	if _, ok := got[0]["resource_declaration"]; ok {
		t.Fatalf("局部 DB 不得有 resource_declaration: %+v", got[0])
	}
	if got[0]["boundary"] != "resource_identity_unproven" {
		t.Fatalf("boundary: %+v", got[0])
	}
	if got[0]["table"] != "nodes" {
		t.Fatalf("table: %+v", got[0])
	}
}

// TestU04bStorageDynamicQuery 动态 SQL：query_not_proven，无表列。
func TestU04bStorageDynamicQuery(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04bFixture(t)
	got := u04bStorage(t, dir, "Dynamic", "symbol:go:database/sql:(DB).Exec")
	if len(got) != 1 {
		t.Fatalf("storage 数量 = %d", len(got))
	}
	if got[0]["boundary"] != "query_not_proven" {
		t.Fatalf("boundary: %+v", got[0])
	}
	if _, ok := got[0]["table"]; ok {
		t.Fatalf("动态 SQL 不得有 table: %+v", got[0])
	}
}

// TestU04bStorageArgumentCount 占位符与实参数量不符 → argument_count_unproven。
func TestU04bStorageArgumentCount(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04bFixture(t)
	got := u04bStorage(t, dir, "Mismatch", "symbol:go:database/sql:(DB).Exec")
	if len(got) != 1 {
		t.Fatalf("storage 数量 = %d", len(got))
	}
	if got[0]["boundary"] != "argument_count_unproven" {
		t.Fatalf("boundary: %+v", got[0])
	}
	if _, ok := got[0]["table"]; ok {
		t.Fatalf("数量不符不得保留 table: %+v", got[0])
	}
}

// TestU04bLogNotWrite 日志调用（含 INSERT 字面量）不是写入、也不产生诊断。
func TestU04bLogNotWrite(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/logflow\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), `package logflow
func Log(string, ...any) {}
func Entry() { Log("INSERT INTO nodes(id) VALUES (?)", "x") }
`)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/logflow:Entry",
		"--repo", dir, "--code", "--depth", "12", "--format", "mermaid")
	if code != 0 {
		t.Fatalf("query exit=%d\n%s", code, out)
	}
	if strings.Contains(out, "持久化边界") {
		t.Fatalf("日志调用不得产生持久化诊断:\n%s", out)
	}
}

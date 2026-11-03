//go:build integration

package integration

// todo28 U04e：读取选择器与符号键来源（storage_read，真实源码 → init → CLI）。
//
// 覆盖：明确列投影 + AND 等式、谓词参数来源（别名/拼接）、资源声明；
// 动态 SQL/实参数量不符/复杂形态不保留表列；自研 QueryRow 不输出读取证据；
// Mermaid 读取边界 Note（与 JSON 同源、不依赖 Stop.Detail）。

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

const u04eSrc = `package readflow
import "database/sql"
var DB *sql.DB
func Load(id string){DB.QueryRow("SELECT id,kind FROM nodes WHERE id=? AND region='ID'",id)}
func Review(id string){alias:=id;Load(alias+"-service")}
func PyQuery(query string){DB.QueryRow(query,"x")}
func Mismatch(id string){DB.QueryRow("SELECT id FROM nodes WHERE id=?",id,"extra")}
func Star(){DB.QueryRow("SELECT * FROM nodes")}
func Join(id string){DB.QueryRow("SELECT nodes.id FROM nodes JOIN other ON nodes.id=other.id WHERE nodes.id=?",id)}
func Ordered(){DB.QueryRow("SELECT id FROM nodes ORDER BY id")}
type Fake struct{}
func(*Fake) QueryRow(...any){}
func Custom(f *Fake){f.QueryRow("SELECT id FROM nodes")}
func ExecSelect(){DB.Exec("SELECT id FROM nodes")}
`

func u04eFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/readflow\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), u04eSrc)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

// u04eReads 查询入口 → storage_read 对象（按 evidence.symbol_id 过滤）。
func u04eReads(t *testing.T, dir, entry, symbol string, depth, segments string) []map[string]any {
	t.Helper()
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/readflow:"+entry,
		"--repo", dir, "--code", "--depth", depth, "--continue-segments", segments,
		"--evidence", "--json")
	if code != 0 {
		t.Fatalf("query %s exit=%d\n%s", entry, code, out)
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
					if rd, ok := ev["storage_read"].(map[string]any); ok {
						found = append(found, rd)
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

// TestU04eReadProjection E-U04e 的 QueryRow 投影（列/谓词/来源/资源声明）。
func TestU04eReadProjection(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04eFixture(t)
	got := u04eReads(t, dir, "Review", "symbol:go:database/sql:(DB).QueryRow", "12", "32")
	if len(got) != 1 {
		t.Fatalf("storage_read 数量 = %d", len(got))
	}
	rd := got[0]
	if rd["table"] != "nodes" || rd["outcome"] != "unproven" ||
		rd["boundary"] != "record_identity_unproven" ||
		rd["resource_declaration"] != "symbol:go:example.com/readflow:DB" {
		t.Fatalf("storage_read: %+v", rd)
	}
	cols, _ := rd["columns"].([]any)
	if len(cols) != 2 || cols[0] != "id" || cols[1] != "kind" {
		t.Fatalf("columns: %+v", cols)
	}
	preds, _ := rd["predicates"].([]any)
	if len(preds) != 2 {
		t.Fatalf("predicates: %+v", preds)
	}
	p0 := preds[0].(map[string]any)
	v0 := p0["value"].(map[string]any)
	origin, _ := v0["origin"].(map[string]any)
	if p0["column"] != "id" || v0["kind"] != "argument" || v0["argument"] != float64(0) ||
		v0["expression"] != "id" || origin["kind"] != "concat" {
		t.Fatalf("predicate[0]: %+v", p0)
	}
	parts, _ := origin["parts"].([]any)
	if len(parts) != 2 {
		t.Fatalf("origin parts: %+v", origin)
	}
	head := parts[0].(map[string]any)
	if head["kind"] != "parameter" || head["symbol"] != "symbol:go:example.com/readflow:Review" ||
		head["parameter"] != float64(0) {
		t.Fatalf("origin part[0]: %+v", head)
	}
	p1 := preds[1].(map[string]any)
	v1 := p1["value"].(map[string]any)
	if p1["column"] != "region" || v1["kind"] != "literal" || v1["sql"] != "'ID'" {
		t.Fatalf("predicate[1]: %+v", p1)
	}
	// depth=1 + 续查同口径。
	got1 := u04eReads(t, dir, "Review", "symbol:go:database/sql:(DB).QueryRow", "1", "32")
	if len(got1) != 1 || got1[0]["table"] != "nodes" {
		t.Fatalf("depth1 storage_read: %+v", got1)
	}
}

// TestU04eMermaidReadBoundary 图内读取边界与 JSON 同源。
func TestU04eMermaidReadBoundary(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04eFixture(t)
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/readflow:Review",
		"--repo", dir, "--code", "--depth", "1", "--continue-segments", "32", "--format", "mermaid")
	if code != 0 {
		t.Fatalf("query exit=%d\n%s", code, out)
	}
	want := "读取边界：SELECT id,kind FROM nodes WHERE id=id AND region='ID'（读取结果与记录身份未证明）"
	if !strings.Contains(out, want) {
		t.Fatalf("缺少读取边界 Note:\n%s", out)
	}
}

// TestU04eRejections 动态/数量不符/复杂形态不保留表列；自研 QueryRow 无证据。
func TestU04eRejections(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04eFixture(t)
	cases := []struct{ entry, boundary string }{
		{"PyQuery", "query_not_proven"},
		{"Mismatch", "argument_count_unproven"},
		{"Star", "sql_shape_unproven"},
		{"Join", "sql_shape_unproven"},
		{"Ordered", "sql_shape_unproven"},
	}
	for _, c := range cases {
		got := u04eReads(t, dir, c.entry, "symbol:go:database/sql:(DB).QueryRow", "12", "32")
		if len(got) != 1 {
			t.Fatalf("%s storage_read 数量 = %d", c.entry, len(got))
		}
		if got[0]["boundary"] != c.boundary {
			t.Fatalf("%s boundary = %+v（want %s）", c.entry, got[0]["boundary"], c.boundary)
		}
		if _, ok := got[0]["table"]; ok {
			t.Fatalf("%s 不得保留 table: %+v", c.entry, got[0])
		}
	}
	// 自研 QueryRow 与 DB.Exec(SELECT...) 都不是读取证据。
	for _, entry := range []string{"Custom", "ExecSelect"} {
		got := u04eReads(t, dir, entry, "symbol:go:database/sql:(DB).QueryRow", "12", "32")
		if len(got) != 0 {
			t.Fatalf("%s 不应有 storage_read: %+v", entry, got)
		}
	}
}

//go:build integration

package integration

// todo28 U04h：SQL 写入错误返回槽与条件的同源身份（真实源码 → init → CLI）。
//
// 覆盖：`_,err := Exec` / `var _,err =` / 别名 / 比较极性与否定 / 覆盖/逃逸/
// 遮蔽失效 / 同名自定义 Exec / 两次调用身份隔离 / depth1+续查同源。

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

const u04hSrc = `package writeresult
import("context";"database/sql";"errors")
var DB *sql.DB
func Saved(){}
func Failed(){}
func Entry(id string){_,err:=DB.Exec("INSERT INTO nodes(id) VALUES (?)",id);alias:=err;if alias==nil{Saved()}else{Failed()}}
func Unknown()error{return errors.New("unknown")}
func Context(ctx context.Context,id string){_,err:=DB.ExecContext(ctx,"INSERT INTO nodes(id) VALUES (?)",id);if err==nil{Saved()}else{Failed()}}
func Var(id string){var _,err=DB.Exec("INSERT INTO nodes(id) VALUES (?)",id);if nil!=err{Failed()}else{Saved()}}
func Reassigned(id string){_,err:=DB.Exec("INSERT INTO nodes(id) VALUES (?)",id);err=Unknown();if err==nil{Saved()}}
func Escaped(id string){_,err:=DB.Exec("INSERT INTO nodes(id) VALUES (?)",id);p:=&err;_=p;if err==nil{Saved()}}
func Conditional(id string,flag bool){_,err:=DB.Exec("INSERT INTO nodes(id) VALUES (?)",id);if flag{err=Unknown()};if err==nil{Saved()}}
func Shadow(id string){_,err:=DB.Exec("INSERT INTO nodes(id) VALUES (?)",id);{var err error;if err==nil{Saved()}};if err==nil{Saved()}}
func ShadowNil(id string){_,err:=DB.Exec("INSERT INTO nodes(id) VALUES (?)",id);var nil error;if err==nil{Saved()}}
func Repeated(id string){_,a:=DB.Exec("INSERT INTO nodes(id) VALUES (?)",id);_,b:=DB.Exec("UPDATE nodes SET id = ?",id);if a==nil{Saved()};if b==nil{Saved()}}
func Tx(tx *sql.Tx,id string){_,err:=tx.Exec("INSERT INTO nodes(id) VALUES (?)",id);if !(err!=nil){Saved()}}
func Check(err error){if err==nil{Saved()}}
func Forward(id string){_,err:=DB.Exec("INSERT INTO nodes(id) VALUES (?)",id);Check(err)}
func Helper(id string){_,err:=DB.Exec("INSERT INTO nodes(id) VALUES (?)",id);if err==nil{Saved()}}
func RepeatedHelper(){Helper("left");Helper("right")}
func CopiedEscape(id string){_,err:=DB.Exec("INSERT INTO nodes(id) VALUES (?)",id);copy:=err;p:=&err;_=p;if copy==nil{Saved()}}
type Custom struct{}
func(Custom)Exec(string,...any)(int,error){return 0,Unknown()}
func Lookalike(id string){_,err:=Custom{}.Exec("INSERT INTO nodes(id) VALUES (?)",id);if err==nil{Saved()}}
`

func u04hFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/writeresult\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), u04hSrc)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

// u04hResult 查询入口 → writes/conditions 投影。
func u04hResult(t *testing.T, dir, entry, depth, segments string) (writes, conds []map[string]any) {
	t.Helper()
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/writeresult:"+entry,
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
		t.Fatalf("json: %v\n%s", err, out)
	}
	var walk func(n map[string]any)
	walk = func(n map[string]any) {
		if st, ok := n["evidence"].(map[string]any); ok {
			if storage, ok := st["storage"].(map[string]any); ok {
				item := map[string]any{}
				if er, ok := storage["error_result"].(map[string]any); ok {
					item["call_id"] = er["call_id"]
					item["slot"] = er["slot"]
				} else {
					item["call_id"] = nil
					item["slot"] = nil
				}
				writes = append(writes, item)
			}
		}
		if wc, ok := n["write_condition"].(map[string]any); ok {
			cond := map[string]any{"when_true": wc["when_true"], "when_false": wc["when_false"]}
			if r, ok := wc["result"].(map[string]any); ok {
				cond["call_id"] = r["call_id"]
				cond["slot"] = r["slot"]
			}
			conds = append(conds, cond)
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
	return writes, conds
}

// TestU04hEntryProjection E-U04h 的 Entry 投影（call_id/slot/极性）。
func TestU04hEntryProjection(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04hFixture(t)
	for _, args := range [][2]string{{"12", "32"}, {"1", "32"}} {
		writes, conds := u04hResult(t, dir, "Entry", args[0], args[1])
		if len(writes) != 1 || writes[0]["call_id"] != "write-1" || writes[0]["slot"] != float64(1) {
			t.Fatalf("writes(depth=%s) = %+v", args[0], writes)
		}
		if len(conds) != 1 {
			t.Fatalf("conditions(depth=%s) = %+v", args[0], conds)
		}
		c := conds[0]
		if c["call_id"] != "write-1" || c["slot"] != float64(1) ||
			c["when_true"] != "error_nil" || c["when_false"] != "error_non_nil" {
			t.Fatalf("condition(depth=%s) = %+v", args[0], c)
		}
	}
}

// TestU04hPositiveMatrix 极性/var/别名/否定/形参透传/两次调用。
func TestU04hPositiveMatrix(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04hFixture(t)
	// Var：nil!=err → then=error_non_nil。
	_, conds := u04hResult(t, dir, "Var", "12", "32")
	if len(conds) != 1 || conds[0]["when_true"] != "error_non_nil" || conds[0]["when_false"] != "error_nil" {
		t.Fatalf("Var conditions = %+v", conds)
	}
	// Tx：!(err!=nil) → then=error_nil。
	_, conds = u04hResult(t, dir, "Tx", "12", "32")
	if len(conds) != 1 || conds[0]["when_true"] != "error_nil" {
		t.Fatalf("Tx conditions = %+v", conds)
	}
	// Forward：错误经形参传给 Check → 条件引用同一写入。
	writes, conds := u04hResult(t, dir, "Forward", "12", "32")
	if len(writes) != 1 || len(conds) != 1 || conds[0]["call_id"] != writes[0]["call_id"] {
		t.Fatalf("Forward writes=%+v conditions=%+v", writes, conds)
	}
	// Shadow：内层遮蔽变量的条件不引用；外层仍引用写入。
	_, conds = u04hResult(t, dir, "Shadow", "12", "32")
	if len(conds) != 1 {
		t.Fatalf("Shadow 应只有外层一个条件: %+v", conds)
	}
	// Repeated：两次调用各自绑定（call_id 不同）。
	_, conds = u04hResult(t, dir, "Repeated", "12", "32")
	if len(conds) != 2 || conds[0]["call_id"] == conds[1]["call_id"] {
		t.Fatalf("Repeated conditions = %+v", conds)
	}
	// CopiedEscape：拷贝时点的来源保留（原变量取址不影响拷贝）。
	_, conds = u04hResult(t, dir, "CopiedEscape", "12", "32")
	if len(conds) != 1 {
		t.Fatalf("CopiedEscape conditions = %+v", conds)
	}
	// RepeatedHelper：两次 helper 调用各自一个条件。
	_, conds = u04hResult(t, dir, "RepeatedHelper", "12", "32")
	if len(conds) != 2 || conds[0]["call_id"] == conds[1]["call_id"] {
		t.Fatalf("RepeatedHelper conditions = %+v", conds)
	}
}

// TestU04hInvalidations 覆盖/逃逸/条件改写/遮蔽 nil/自定义 Exec → 无绑定。
func TestU04hInvalidations(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04hFixture(t)
	for _, entry := range []string{"Reassigned", "Escaped", "Conditional", "ShadowNil", "Lookalike"} {
		_, conds := u04hResult(t, dir, entry, "12", "32")
		if len(conds) != 0 {
			t.Fatalf("%s 不应有错误槽条件: %+v", entry, conds)
		}
	}
}

// TestU04hMermaidAndText 图/文本里的写入错误槽与极性。
func TestU04hMermaidAndText(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04hFixture(t)
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/writeresult:Entry",
		"--repo", dir, "--code", "--depth", "12", "--continue-segments", "32", "--format", "mermaid")
	if code != 0 {
		t.Fatalf("query exit=%d\n%s", code, out)
	}
	for _, want := range []string{"写入错误结果 write-1[1]（outcome=unproven）",
		"写入错误结果 write-1[1]：then=error_nil；else=error_non_nil（事务提交与影响行数未证明）"} {
		if !strings.Contains(out, want) {
			t.Fatalf("缺少 %q:\n%s", want, out)
		}
	}
	code, text := runCLIOut(t, "query", "sequence", "symbol:go:example.com/writeresult:Entry",
		"--repo", dir, "--code", "--depth", "12", "--continue-segments", "32")
	if code != 0 {
		t.Fatalf("text query exit=%d\n%s", code, text)
	}
	if !strings.Contains(text, "写入错误结果 write-1[1]") || !strings.Contains(text, "then=error_nil") {
		t.Fatalf("文本缺少错误槽/极性:\n%s", text)
	}
}

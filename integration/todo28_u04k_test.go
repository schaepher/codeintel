//go:build integration

package integration

// todo28 U04k/U04l：返回槽来源的传播（真实源码 → init → CLI）。
//
// U04k：Save() (int64,error) { return Session.Insert("node") } → 调用方
// rows/err 条件引用同一次写入（深浅续查一致）；标量转发（sqlreturned）保留
// 入口参数来源与写入错误身份。
// U04l：`if err != nil { return err }; return nil` 的错误守卫下，错误槽等价
// 于该写入错误槽（原始 return nil 的 value_origins 保留字面 nil）。

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

const u04kOrmMod = `module github.com/xormplus/xorm

go 1.26.2
`

const u04kOrmSrc = `package xorm
type Session struct{}
func (*Session) Insert(...any) (int64, error) { return 0, nil }
`

const u04kAppMod = `module example.com/returned

go 1.26.2
require github.com/xormplus/xorm v0.0.0
replace github.com/xormplus/xorm => ../orm
`

const u04kAppSrc = `package returned
import "github.com/xormplus/xorm"
var Session *xorm.Session
func Hit() {}
func Save() (int64, error) { return Session.Insert("node") }
func Entry() {
 rows, err := Save()
 if err != nil { Hit() }
 if rows > 0 { Hit() }
}
func Named() (rows int64, err error) { rows, err = Session.Insert("node"); return }
func NamedEntry(){_,err:=Named();if err!=nil{Hit()}}
`

func u04kFixture(t *testing.T, appMod, ormSrc, appSrc string) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "orm", "go.mod"), u04kOrmMod)
	writeFile(t, filepath.Join(root, "orm", "orm.go"), ormSrc)
	writeFile(t, filepath.Join(root, "app", "go.mod"), appMod)
	writeFile(t, filepath.Join(root, "app", "flow.go"), appSrc)
	dir := filepath.Join(root, "app")
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

// u04kProjection 查询入口 → 错误/行数条件与写入来源。
func u04kProjection(t *testing.T, dir, module, entry, depth, segments string) (conds, writes []map[string]any) {
	t.Helper()
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:"+module+":"+entry,
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
		if wc, ok := n["write_condition"].(map[string]any); ok {
			item := map[string]any{"when_true": wc["when_true"], "when_false": wc["when_false"]}
			if r, ok := wc["result"].(map[string]any); ok {
				item["slot"] = r["slot"]
				item["call_id"] = r["call_id"]
			}
			conds = append(conds, item)
		}
		if rc, ok := n["row_count_condition"].(map[string]any); ok {
			item := map[string]any{"when_true": rc["when_true"], "when_false": rc["when_false"]}
			if r, ok := rc["result"].(map[string]any); ok {
				item["slot"] = r["slot"]
				if w, ok := r["write"].(map[string]any); ok {
					item["call_id"] = w["call_id"]
				}
			}
			conds = append(conds, item)
		}
		if ev, ok := n["evidence"].(map[string]any); ok {
			if orm, ok := ev["orm"].(map[string]any); ok {
				item := map[string]any{}
				if er, ok := orm["error_result"].(map[string]any); ok {
					item["error_call_id"] = er["call_id"]
				}
				if rc, ok := orm["row_count_result"].(map[string]any); ok {
					item["row_call_id"] = rc["write"].(map[string]any)["call_id"]
				}
				writes = append(writes, item)
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
	return conds, writes
}

// TestU04kReturnSlotConditions E-U04k：深浅两种入口都得到错误+行数条件。
func TestU04kReturnSlotConditions(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04kFixture(t, u04kAppMod, u04kOrmSrc, u04kAppSrc)
	for _, args := range [][2]string{{"12", "32"}, {"1", "32"}} {
		conds, writes := u04kProjection(t, dir, "example.com/returned", "Entry", args[0], args[1])
		if len(writes) != 1 {
			t.Fatalf("writes(depth=%s) = %+v", args[0], writes)
		}
		if len(conds) != 2 {
			t.Fatalf("conditions(depth=%s) = %+v", args[0], conds)
		}
		errCond, rowCond := conds[0], conds[1]
		if errCond["slot"] != float64(1) || errCond["when_true"] != "error_non_nil" ||
			errCond["when_false"] != "error_nil" {
			t.Fatalf("error condition(depth=%s) = %+v", args[0], errCond)
		}
		if rowCond["slot"] != float64(0) || rowCond["when_true"] != "rows_gt_zero" {
			t.Fatalf("row condition(depth=%s) = %+v", args[0], rowCond)
		}
		if errCond["call_id"] != writes[0]["error_call_id"] || rowCond["call_id"] != writes[0]["row_call_id"] {
			t.Fatalf("条件与写入身份不一致: conds=%+v writes=%+v", conds, writes)
		}
	}
}

// TestU04kNamedBareReturn 具名裸 return 传播错误槽。
func TestU04kNamedBareReturn(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04kFixture(t, u04kAppMod, u04kOrmSrc, u04kAppSrc)
	conds, writes := u04kProjection(t, dir, "example.com/returned", "NamedEntry", "12", "32")
	if len(writes) != 1 || len(conds) != 1 {
		t.Fatalf("conds=%+v writes=%+v", conds, writes)
	}
	if conds[0]["slot"] != float64(1) || conds[0]["call_id"] != writes[0]["error_call_id"] {
		t.Fatalf("condition = %+v", conds[0])
	}
}

// u04lSrc 错误守卫返回等价（U04l）。
const u04lSrc = `package guardreturned
import "database/sql"
var DB *sql.DB
func Hit() {}
func Save() error {
 _, err := DB.Exec("INSERT INTO nodes(id) VALUES (?)", 1)
 if err != nil { return err }
 return nil
}
func Entry() {
 err := Save()
 if err != nil { Hit() }
}
`

// TestU04lGuardReturnEquivalence 错误守卫下错误槽等价（深浅一致）。
func TestU04lGuardReturnEquivalence(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/guardreturned\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), u04lSrc)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	for _, args := range [][2]string{{"1", "32"}, {"12", "32"}} {
		code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/guardreturned:Entry",
			"--repo", dir, "--code", "--depth", args[0], "--continue-segments", args[1],
			"--evidence", "--json")
		if code != 0 {
			t.Fatalf("query exit=%d\n%s", code, out)
		}
		var res struct {
			Nodes []map[string]any `json:"Nodes"`
		}
		if err := json.Unmarshal([]byte(out), &res); err != nil {
			t.Fatalf("json: %v", err)
		}
		// 顶层（Entry 自己的语句）与 Save 内部的条件都必须引用同一写入；
		// 验收投影只数顶层一条（E-U04l 的 .Nodes[] 口径）。
		var top, nested []map[string]any
		var walk func(n map[string]any, topLevel bool)
		walk = func(n map[string]any, topLevel bool) {
			if wc, ok := n["write_condition"].(map[string]any); ok {
				item := map[string]any{"when_true": wc["when_true"], "when_false": wc["when_false"]}
				if r, ok := wc["result"].(map[string]any); ok {
					item["slot"] = r["slot"]
					item["call_id"] = r["call_id"]
				}
				if topLevel {
					top = append(top, item)
				} else {
					nested = append(nested, item)
				}
			}
			if n["Kind"] == "call" {
				return // 嵌套调用内部的条件另行统计
			}
			for _, key := range []string{"Nodes", "Else"} {
				if arr, ok := n[key].([]any); ok {
					for _, c := range arr {
						walk(c.(map[string]any), false)
					}
				}
			}
		}
		for _, n := range res.Nodes {
			if arr, ok := n["Nodes"].([]any); ok {
				for _, c := range arr {
					walk(c.(map[string]any), true)
				}
			}
		}
		if len(top) != 1 || top[0]["slot"] != float64(1) ||
			top[0]["when_true"] != "error_non_nil" || top[0]["when_false"] != "error_nil" {
			t.Fatalf("depth=%s top conditions = %+v", args[0], top)
		}
		for _, c := range nested {
			if c["call_id"] != top[0]["call_id"] {
				t.Fatalf("内部条件 call_id 不一致: %+v vs %+v", c, top[0])
			}
		}
	}
}

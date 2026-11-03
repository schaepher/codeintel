//go:build integration

package integration

// todo28 U04j：XORM 影响行数返回槽与条件（真实源码 → init → CLI）。
//
// 覆盖：slot 0 与错误槽共享 call_id、>0 / <=0 / >=0 / <0 / ==0 / !=0 及
// 左右颠倒/取反、别名/形参透传；比较前覆写/取址/错误签名变体不附加旧条件。

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

const u04jOrmSrc = `package xorm
type Session struct{}
func (*Session) Insert(...any) (int64, error) { return 0, nil }
func (*Session) Update(...any) (int64, error) { return 0, nil }
`

const u04jAppMod = `module example.com/count

go 1.26.2
require github.com/xormplus/xorm v0.0.0
replace github.com/xormplus/xorm => ../orm
`

const u04jAppSrc = `package count
import "github.com/xormplus/xorm"
var Session *xorm.Session
func Hit() {}
func Count() {
 rows, err := Session.Insert("node")
 _ = err
 copy := rows
 if copy > 0 { Hit() }
}
func Reversed(){rows,_:=Session.Insert("node");if 0<rows{Hit()}}
func Negated(){rows,_:=Session.Insert("node");if !(rows<=0){Hit()}}
func GE(){rows,_:=Session.Insert("node");if rows>=0{Hit()}}
func LT(){rows,_:=Session.Insert("node");if rows<0{Hit()}}
func EQ(){rows,_:=Session.Insert("node");if rows==0{Hit()}}
func NE(){rows,_:=Session.Insert("node");if rows!=0{Hit()}}
func Overwritten(){rows,_:=Session.Insert("node");rows=0;if rows>0{Hit()}}
func Escaped(){rows,_:=Session.Insert("node");p:=&rows;_=p;if rows>0{Hit()}}
func Param(n int64){if n>0{Hit()}}
func Forward(){rows,_:=Session.Insert("node");Param(rows)}
func Two(){a,_:=Session.Insert("node");b,_:=Session.Update("node");if a>0{Hit()};if b>0{Hit()}}
`

func u04jFixture(t *testing.T, ormSrc, appSrc string) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "orm", "go.mod"), "module github.com/xormplus/xorm\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(root, "orm", "orm.go"), ormSrc)
	writeFile(t, filepath.Join(root, "app", "go.mod"), u04jAppMod)
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

func u04jResult(t *testing.T, dir, entry string) (writes, conds []map[string]any) {
	t.Helper()
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/count:"+entry,
		"--repo", dir, "--code", "--depth", "12", "--continue-segments", "32",
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
		if ev, ok := n["evidence"].(map[string]any); ok {
			if orm, ok := ev["orm"].(map[string]any); ok {
				item := map[string]any{"outcome": orm["outcome"]}
				if rc, ok := orm["row_count_result"].(map[string]any); ok {
					item["slot"] = rc["slot"]
					if w, ok := rc["write"].(map[string]any); ok {
						item["call_id"] = w["call_id"]
						item["write_slot"] = w["slot"]
					}
				} else {
					item["slot"] = nil
				}
				writes = append(writes, item)
			}
		}
		if rc, ok := n["row_count_condition"].(map[string]any); ok {
			cond := map[string]any{"when_true": rc["when_true"], "when_false": rc["when_false"]}
			if r, ok := rc["result"].(map[string]any); ok {
				cond["slot"] = r["slot"]
				if w, ok := r["write"].(map[string]any); ok {
					cond["call_id"] = w["call_id"]
				}
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

// TestU04jProjection E-U04j 的 Count 投影（slot 0 + rows_gt_zero）。
func TestU04jProjection(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04jFixture(t, u04jOrmSrc, u04jAppSrc)
	for _, depth := range []string{"12", "1"} {
		writes, conds := u04jResult(t, dir, "Count")
		if len(writes) != 1 || writes[0]["slot"] != float64(0) ||
			writes[0]["call_id"] != "write-1" || writes[0]["write_slot"] != float64(1) ||
			writes[0]["outcome"] != "unproven" {
			t.Fatalf("writes(depth=%s) = %+v", depth, writes)
		}
		if len(conds) != 1 || conds[0]["slot"] != float64(0) ||
			conds[0]["when_true"] != "rows_gt_zero" || conds[0]["when_false"] != "rows_le_zero" ||
			conds[0]["call_id"] != "write-1" {
			t.Fatalf("conditions(depth=%s) = %+v", depth, conds)
		}
	}
}

// TestU04jComparisonMatrix 运算符/左右颠倒/取反/形参透传/两次调用。
func TestU04jComparisonMatrix(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04jFixture(t, u04jOrmSrc, u04jAppSrc)
	want := map[string][2]string{
		"Reversed": {SeqRowsGT, SeqRowsLE},
		"Negated":  {SeqRowsGT, SeqRowsLE},
		"GE":       {SeqRowsGE, SeqRowsLT},
		"LT":       {SeqRowsLT, SeqRowsGE},
		"EQ":       {SeqRowsEQ, SeqRowsNE},
		"NE":       {SeqRowsNE, SeqRowsEQ},
		"Forward":  {SeqRowsGT, SeqRowsLE},
	}
	for entry, pair := range want {
		_, conds := u04jResult(t, dir, entry)
		if len(conds) != 1 || conds[0]["when_true"] != pair[0] || conds[0]["when_false"] != pair[1] {
			t.Fatalf("%s conditions = %+v（want %v）", entry, conds, pair)
		}
	}
	_, conds := u04jResult(t, dir, "Two")
	if len(conds) != 2 || conds[0]["call_id"] == conds[1]["call_id"] {
		t.Fatalf("Two conditions = %+v", conds)
	}
}

// TestU04jInvalidations 覆写/取址/错误签名 → 不附加旧行数条件。
func TestU04jInvalidations(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04jFixture(t, u04jOrmSrc, u04jAppSrc)
	for _, entry := range []string{"Overwritten", "Escaped"} {
		_, conds := u04jResult(t, dir, entry)
		if len(conds) != 0 {
			t.Fatalf("%s 不应有行数条件: %+v", entry, conds)
		}
	}
	// 错误签名 (error,int64)：行数槽与条件都不成立。
	badOrm := "package xorm\ntype Session struct{}\nfunc (*Session) Insert(...any) (error, int64) { return nil, 0 }\n"
	badApp := strings.Replace(u04jAppSrc, `rows, err := Session.Insert("node")`, `err, rows := Session.Insert("node")`, 1)
	badApp = strings.Replace(badApp, `rows,_:=Session.Insert("node")`, `_,rows:=Session.Insert("node")`, -1)
	badDir := u04jFixture(t, badOrm, badApp)
	_, conds := u04jResult(t, badDir, "Count")
	if len(conds) != 0 {
		t.Fatalf("错误签名不应有行数条件: %+v", conds)
	}
}

// 行数比较结果名（与 action 常量一致，避免测试包导入内部常量）。
const (
	SeqRowsGT = "rows_gt_zero"
	SeqRowsLE = "rows_le_zero"
	SeqRowsGE = "rows_ge_zero"
	SeqRowsLT = "rows_lt_zero"
	SeqRowsEQ = "rows_eq_zero"
	SeqRowsNE = "rows_ne_zero"
)

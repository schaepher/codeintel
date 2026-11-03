//go:build integration

package integration

// todo28 U04i：XORM 写入返回签名与错误条件（真实源码 → init → CLI）。
//
// 覆盖：(int64,error)/(具名返回) 证明、错误槽 1 与条件同源；交换签名、
// 遮蔽 error/int64、第二返回非内置 error 均不输出；构建标签排除文件不遮蔽。

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

const u04iAppMod = `module example.com/ormresult

go 1.26.2

require github.com/xormplus/xorm v0.0.0
replace github.com/xormplus/xorm => ../orm
`

const u04iAppFlow = `package ormresult
import "github.com/xormplus/xorm"
var Session *xorm.Session
func Saved(){}
func Failed(){}
func Insert(){_,err:=Session.Insert("node");copy:=err;if copy==nil{Saved()}else{Failed()}}
func Update(){_,err:=Session.Update("node");if err==nil{Saved()}else{Failed()}}
func Delete(){var _,err=Session.Delete("node");if err==nil{Saved()}else{Failed()}}
`

func u04iOrmSrc(sig string) string {
	return `package xorm
type Session struct{}
func(*Session)Insert(...any)` + sig + `
func(*Session)Update(...any)` + sig + `
func(*Session)Delete(...any)` + sig + `
`
}

func u04iFixture(t *testing.T, ormSrc, extra string) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "orm", "go.mod"), "module github.com/xormplus/xorm\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(root, "orm", "orm.go"), ormSrc)
	if extra != "" {
		writeFile(t, filepath.Join(root, "orm", "shadow.go"), extra)
	}
	writeFile(t, filepath.Join(root, "app", "go.mod"), u04iAppMod)
	writeFile(t, filepath.Join(root, "app", "flow.go"), u04iAppFlow)
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

// u04iResult 查询入口 → writes（orm 错误槽/签名）与 conditions。
func u04iResult(t *testing.T, dir, entry, depth, segments string) (writes, conds []map[string]any) {
	t.Helper()
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/ormresult:"+entry,
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
		if ev, ok := n["evidence"].(map[string]any); ok {
			if orm, ok := ev["orm"].(map[string]any); ok {
				item := map[string]any{"outcome": orm["outcome"]}
				if er, ok := orm["error_result"].(map[string]any); ok {
					item["slot"] = er["slot"]
					item["call_id"] = er["call_id"]
					if ss, ok := er["signature_source"].(map[string]any); ok {
						item["signature"] = ss["symbol_id"]
					}
				} else {
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

// TestU04iInsertProjection E-U04i 的 Insert 投影。
func TestU04iInsertProjection(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04iFixture(t, u04iOrmSrc("(int64,error)"), "")
	for _, args := range [][2]string{{"12", "32"}, {"1", "32"}} {
		writes, conds := u04iResult(t, dir, "Insert", args[0], args[1])
		if len(writes) != 1 || writes[0]["slot"] != float64(1) || writes[0]["outcome"] != "unproven" {
			t.Fatalf("writes(depth=%s) = %+v", args[0], writes)
		}
		if writes[0]["signature"] != "symbol:go:github.com/xormplus/xorm:(Session).Insert" {
			t.Fatalf("signature: %+v", writes[0])
		}
		if len(conds) != 1 || conds[0]["slot"] != float64(1) ||
			conds[0]["when_true"] != "error_nil" || conds[0]["when_false"] != "error_non_nil" {
			t.Fatalf("conditions(depth=%s) = %+v", args[0], conds)
		}
		if conds[0]["call_id"] != writes[0]["call_id"] {
			t.Fatalf("condition 与写入身份不一致: %+v vs %+v", conds[0], writes[0])
		}
	}
}

// TestU04iOpsAndNamedReturns Update/Delete + 具名返回仍证明同一错误槽。
func TestU04iOpsAndNamedReturns(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04iFixture(t, u04iOrmSrc("(rows int64, err error)"), "")
	for _, entry := range []string{"Insert", "Update", "Delete"} {
		writes, conds := u04iResult(t, dir, entry, "12", "32")
		if len(writes) != 1 || writes[0]["slot"] != float64(1) {
			t.Fatalf("%s writes = %+v", entry, writes)
		}
		if len(conds) != 1 || conds[0]["slot"] != float64(1) {
			t.Fatalf("%s conditions = %+v", entry, conds)
		}
	}
}

// TestU04iSignatureRejections 交换签名/遮蔽 error/int64/非内置 error → 无证据。
func TestU04iSignatureRejections(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	cases := []struct {
		name  string
		sig   string
		extra string
	}{
		{"swapped", "(error,int64)", ""},
		{"shadow error", "(int64,error)", "package xorm\ntype error *int\n"},
		{"shadow int64", "(int64,error)", "package xorm\ntype int64 string\n"},
		{"pointer error", "(int64,*error)", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := u04iFixture(t, u04iOrmSrc(c.sig), c.extra)
			writes, conds := u04iResult(t, dir, "Insert", "12", "32")
			if len(conds) != 0 {
				t.Fatalf("%s 不应有错误槽条件: %+v", c.name, conds)
			}
			for _, w := range writes {
				if w["slot"] != nil {
					t.Fatalf("%s 不应证明错误槽: %+v", c.name, w)
				}
			}
		})
	}
}

// TestU04iExcludedShadowNotShading 构建标签排除的文件不遮蔽当前签名。
func TestU04iExcludedShadowNotShading(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	extra := "//go:build never_shadow_fixture\n\npackage xorm\ntype error *int\n"
	dir := u04iFixture(t, u04iOrmSrc("(int64,error)"), extra)
	writes, conds := u04iResult(t, dir, "Insert", "12", "32")
	if len(writes) != 1 || writes[0]["slot"] != float64(1) {
		t.Fatalf("writes = %+v", writes)
	}
	if len(conds) != 1 || conds[0]["when_true"] != "error_nil" {
		t.Fatalf("conditions = %+v", conds)
	}
}

// TestU04iMermaid 文本/Mermaid 同时给出错误槽与极性。
func TestU04iMermaid(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04iFixture(t, u04iOrmSrc("(int64,error)"), "")
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/ormresult:Insert",
		"--repo", dir, "--code", "--depth", "12", "--format", "mermaid")
	if code != 0 {
		t.Fatalf("query exit=%d\n%s", code, out)
	}
	for _, want := range []string{"写入错误结果 write-1[1]", "then=error_nil"} {
		if !strings.Contains(out, want) {
			t.Fatalf("缺少 %q:\n%s", want, out)
		}
	}
}

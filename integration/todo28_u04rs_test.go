//go:build integration

package integration

// todo28 U04r/U04s：外部类型声明文件的 receiver 字段 + 显式匿名字段访问与
// 标准库方法终端身份（真实源码 → init → CLI）。

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func u04rsFixture(t *testing.T, coreTx, leafTx, runExtra, appSrc string) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "fields", "go.mod"), "module example.com/fields\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(root, "fields", "core", "tx.go"), coreTx)
	if leafTx != "" {
		writeFile(t, filepath.Join(root, "fields", "leaf", "tx.go"), leafTx)
	}
	writeFile(t, filepath.Join(root, "fields", "run.go"), `package fields
func Run(){var s Session;s.Commit()}
func(s *Session)Commit(){s.tx.Commit()}
`+runExtra)
	writeFile(t, filepath.Join(root, "fields", "types.go"), `package fields
import core "example.com/fields/core"
type Session struct{tx *core.Tx}
type Contract interface{Commit()}
type Dynamic struct{tx Contract}
`)
	writeFile(t, filepath.Join(root, "app", "go.mod"), `module example.com/fieldapp

go 1.26.2
require example.com/fields v0.0.0
replace example.com/fields => ../fields
`)
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

func u04rsCall(t *testing.T, dir, entry, label string) (map[string]any, map[string]any, map[string]any) {
	t.Helper()
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/fieldapp:"+entry,
		"--repo", dir, "--code", "--depth", "2", "--continue-segments", "16",
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
	var ev, stop, tx map[string]any
	var walk func(n map[string]any)
	walk = func(n map[string]any) {
		if ev != nil {
			return
		}
		if lbl, _ := n["Label"].(string); lbl == label {
			ev, _ = n["evidence"].(map[string]any)
			stop, _ = n["stop"].(map[string]any)
			if ev != nil {
				tx, _ = ev["transaction"].(map[string]any)
			}
			return
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
	return ev, stop, tx
}

// TestU04rFieldDeclFile 字段类型来自结构体声明文件的 import。
func TestU04rFieldDeclFile(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	coreTx := "package core\ntype Tx struct{}\nfunc(*Tx)Commit(){Hit()}\nfunc Hit(){}\n"
	appSrc := `package fieldapp
import "example.com/fields"
func Entry(){fields.Run()}
func UnknownEntry(){fields.Unknown()}
`
	dir := u04rsFixture(t, coreTx, "", `func Unknown(){var s Dynamic;s.Commit()}
func(s *Dynamic)Commit(){s.tx.Commit()}
`, appSrc)
	ev, stop, _ := u04rsCall(t, dir, "Entry", "s.tx.Commit")
	if ev == nil || ev["symbol_id"] != "symbol:go:example.com/fields/core:(Tx).Commit" {
		t.Fatalf("字段类型应来自声明文件: ev=%+v stop=%+v", ev, stop)
	}
	// 接口字段 → unknown_receiver（不借具体实现）。
	ev, stop, _ = u04rsCall(t, dir, "UnknownEntry", "s.tx.Commit")
	if ev != nil && ev["symbol_id"] != nil {
		t.Fatalf("接口字段不得给实现: %+v", ev)
	}
	if stop == nil || stop["code"] != "unknown_receiver" {
		t.Fatalf("接口字段 stop: %+v", stop)
	}
}

// TestU04sAnonFieldAndStdlib 显式匿名字段 + 标准库方法终端身份。
func TestU04sAnonFieldAndStdlib(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	coreTx := "package core\nimport \"example.com/fields/leaf\"\ntype Tx struct{*leaf.Tx}\nfunc(tx *Tx)Commit(){tx.Tx.Commit();Hit()}\nfunc Hit(){}\n"
	leafTx := "package leaf\ntype Tx struct{}\nfunc(*Tx)Commit(){}\n"
	extra := `func Standard(){var tx StandardTx;tx.Finish()}
func(tx *StandardTx)Finish(){tx.Tx.Commit()}
`
	appSrc := `package fieldapp
import "example.com/fields"
func Entry(){fields.Run()}
func StandardEntry(){fields.Standard()}
`
	dir := u04rsFixture(t, coreTx, leafTx, extra, appSrc)
	writeFile(t, filepath.Join(dir, "..", "fields", "sql.go"), `package fields
import "database/sql"
type StandardTx struct{*sql.Tx}
`)
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("reinit exit = %d", code)
	}
	ev, stop, _ := u04rsCall(t, dir, "Entry", "tx.Tx.Commit")
	if ev == nil || ev["symbol_id"] != "symbol:go:example.com/fields/leaf:(Tx).Commit" ||
		stop == nil || stop["code"] != "no_calls" {
		t.Fatalf("匿名字段应解析到 leaf.Tx.Commit: ev=%+v stop=%+v", ev, stop)
	}
	ev, stop, tx := u04rsCall(t, dir, "StandardEntry", "tx.Tx.Commit")
	if ev == nil || ev["symbol_id"] != "symbol:go:database/sql:(Tx).Commit" {
		t.Fatalf("标准库方法身份: ev=%+v stop=%+v", ev, stop)
	}
	if stop == nil || stop["code"] != "external_target" {
		t.Fatalf("标准库方法应为 external_target: %+v", stop)
	}
	if tx == nil || tx["boundary"] != "transaction_origin_unproven" {
		t.Fatalf("缺 Begin 来源应保留 transaction_origin_unproven: %+v", tx)
	}
}

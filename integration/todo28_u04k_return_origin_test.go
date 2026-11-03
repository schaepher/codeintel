//go:build integration

package integration

// todo28 U04k：标量/错误返回槽来源传播（补充验收）。

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

// u04kSQLSrc 标量转发（U04k 补充验收）：Key 的形参不是新入口。
const u04kSQLSrc = `package sqlreturned
import "database/sql"
var DB *sql.DB
func Hit() {}
func Key(id string) string { return id }
func Save(id string) error {
 _, err := DB.Exec("INSERT INTO nodes(id) VALUES (?)", id)
 return err
}
func Entry(id string) {
 key := Key(id)
 err := Save(key)
 if err != nil { Hit() }
}
`

// TestU04kScalarReturnOrigin 深浅两入口：写库值 origin 仍是入口参数 0，
// 调用方错误条件引用同一次写入。
func TestU04kScalarReturnOrigin(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/sqlreturned\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), u04kSQLSrc)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	for _, args := range [][2]string{{"1", "32"}, {"12", "32"}} {
		code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/sqlreturned:Entry",
			"--repo", dir, "--code", "--depth", args[0], "--continue-segments", args[1],
			"--evidence", "--json")
		if code != 0 {
			t.Fatalf("query exit=%d\n%s", code, out)
		}
		var res struct {
			Nodes []map[string]any `json:"Nodes"`
			Else  []map[string]any `json:"Else"`
		}
		if err := json.Unmarshal([]byte(out), &res); err != nil {
			t.Fatalf("json: %v", err)
		}
		var writeOrigin map[string]any
		var writeCallID any
		var condCallID any
		var walk func(n map[string]any)
		walk = func(n map[string]any) {
			if ev, ok := n["evidence"].(map[string]any); ok {
				if st, ok := ev["storage"].(map[string]any); ok {
					if er, ok := st["error_result"].(map[string]any); ok {
						writeCallID = er["call_id"]
					}
					if writes, ok := st["writes"].([]any); ok && len(writes) > 0 {
						if v, ok := writes[0].(map[string]any)["value"].(map[string]any); ok {
							writeOrigin, _ = v["origin"].(map[string]any)
						}
					}
				}
			}
			if wc, ok := n["write_condition"].(map[string]any); ok {
				if r, ok := wc["result"].(map[string]any); ok {
					condCallID = r["call_id"]
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
		if writeOrigin == nil {
			t.Fatalf("depth=%s 缺少写入值 origin", args[0])
		}
		if writeOrigin["kind"] != "parameter" || writeOrigin["parameter"] != float64(0) ||
			writeOrigin["symbol"] != "symbol:go:example.com/sqlreturned:Entry" {
			t.Fatalf("depth=%s write origin = %+v", args[0], writeOrigin)
		}
		if writeCallID == nil || condCallID != writeCallID {
			t.Fatalf("depth=%s 条件与写入身份不一致: %v vs %v", args[0], condCallID, writeCallID)
		}
	}
}

// u04lNegSrc U04l 反例：两个不同写入分支 / 未知返回 / caller 覆写。
const u04lNegSrc = `package guardneg
import "database/sql"
var DB *sql.DB
func Hit() {}
func TwoWrites(flag bool) error {
 if flag { _, err := DB.Exec("INSERT INTO nodes(id) VALUES (?)", 1); if err != nil { return err } }
 _, err := DB.Exec("UPDATE nodes SET id = ?", 1)
 return err
}
func UnknownReturn() error { return errFromElsewhere() }
func errFromElsewhere() error { return nil }
func CallerOverwrite() {
 err := TwoWrites(true)
 err = UnknownReturn()
 if err != nil { Hit() }
}
`

// TestU04lNegations 不同写入错误/未知返回/调用方覆写不传播旧错误槽。
func TestU04lNegations(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/guardneg\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), u04lNegSrc)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/guardneg:CallerOverwrite",
		"--repo", dir, "--code", "--depth", "12", "--continue-segments", "32",
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
	var conds int
	var walk func(n map[string]any)
	walk = func(n map[string]any) {
		if _, ok := n["write_condition"].(map[string]any); ok {
			conds++
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
	// TwoWrites 内只有一个错误检查分支（另一种写入无条件返回）；调用方的
	// err 已被 UnknownReturn 覆盖，不得再引用旧写入错误槽。
	if conds != 1 {
		t.Fatalf("条件数 = %d（期望 1：仅 TwoWrites 内部的错误检查）", conds)
	}
}

//go:build integration

package integration

// todo28 U04q：外部方法实际声明 owner、嵌入路径与 receiver 环境
//（真实源码 → init → CLI）。

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

const u04qWrap = `package wrap
import "example.com/framework/core"
type Tx struct{*core.Tx}
type Alias = Tx
type Override struct{*core.Tx}
func(*Override)CommitTrans(){}
type Direct struct{}
func(*Direct)CommitTrans(){}
type Near struct{Tx;Direct}
type Left struct{*core.Tx}
type Right struct{*core.Tx}
type Diamond struct{Left;Right}
type Field struct{*core.Tx;CommitTrans func()}
type CommitTrans struct{}
type EmbeddedField struct{CommitTrans;*core.Tx}
type Contract interface{CommitTrans()}
type Unknown struct{Contract}
type Cycle struct{*Cycle}
`

const u04qCore = `package core
type Tx struct{}
func(tx *Tx)CommitTrans(){tx.finish()}
func(*Tx)finish(){Hit()}
func Hit(){}
`

func u04qFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "framework", "go.mod"), "module example.com/framework\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(root, "framework", "core", "types.go"), u04qCore)
	writeFile(t, filepath.Join(root, "framework", "wrap", "types.go"), u04qWrap)
	writeFile(t, filepath.Join(root, "framework", "flow.go"), `package framework
import "example.com/framework/wrap"
func Run(){var tx wrap.Tx;tx.CommitTrans()}
func Override(){var tx wrap.Override;tx.CommitTrans()}
func Near(){var tx wrap.Near;tx.CommitTrans()}
func Diamond(){var tx wrap.Diamond;tx.CommitTrans()}
func Field(){var tx wrap.Field;tx.CommitTrans()}
func Embedded(){var tx wrap.EmbeddedField;tx.CommitTrans()}
func Unknown(){var tx wrap.Unknown;tx.CommitTrans()}
`)
	writeFile(t, filepath.Join(root, "app", "go.mod"), `module example.com/promotedapp

go 1.26.2
require example.com/framework v0.0.0
replace example.com/framework => ../framework
`)
	writeFile(t, filepath.Join(root, "app", "flow.go"), `package promotedapp
import "example.com/framework"
func Entry(){framework.Run()}
func OverrideEntry(){framework.Override()}
func NearEntry(){framework.Near()}
func DiamondEntry(){framework.Diamond()}
func FieldEntry(){framework.Field()}
func EmbeddedEntry(){framework.Embedded()}
func UnknownEntry(){framework.Unknown()}
`)
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

func u04qCall(t *testing.T, dir, entry string) (map[string]any, map[string]any) {
	t.Helper()
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/promotedapp:"+entry,
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
	var ev, stop map[string]any
	var walk func(n map[string]any)
	walk = func(n map[string]any) {
		if ev != nil {
			return
		}
		if lbl, _ := n["Label"].(string); lbl == "tx.CommitTrans" {
			ev, _ = n["evidence"].(map[string]any)
			stop, _ = n["stop"].(map[string]any)
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
	return ev, stop
}

// TestU04qPromotedOwner E-U04q：嵌入方法选择实际 owner 与 method_path。
func TestU04qPromotedOwner(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04qFixture(t)
	ev, stop := u04qCall(t, dir, "Entry")
	if ev == nil || ev["symbol_id"] != "symbol:go:example.com/framework/core:(Tx).CommitTrans" {
		t.Fatalf("应选择 core.Tx owner: ev=%+v stop=%+v", ev, stop)
	}
	path, _ := ev["method_path"].([]any)
	if len(path) != 2 || path[0] != "symbol:go:example.com/framework/wrap:Tx" ||
		path[1] != "symbol:go:example.com/framework/core:Tx" {
		t.Fatalf("method_path: %+v", path)
	}
	if ev["reason"] != "external-promoted-method" {
		t.Fatalf("reason: %+v", ev["reason"])
	}
	// Override：自身声明优先。
	ev, _ = u04qCall(t, dir, "OverrideEntry")
	if ev == nil || ev["symbol_id"] != "symbol:go:example.com/framework/wrap:(Override).CommitTrans" {
		t.Fatalf("Override 应选自身: %+v", ev)
	}
	// Near：第一层 Direct 优先于第二层 core.Tx。
	ev, _ = u04qCall(t, dir, "NearEntry")
	if ev == nil || ev["symbol_id"] != "symbol:go:example.com/framework/wrap:(Direct).CommitTrans" {
		t.Fatalf("Near 应选第一层 Direct: %+v", ev)
	}
}

// TestU04qAmbiguousAndShadow 菱形/字段遮蔽/嵌入接口 → 不展开。
func TestU04qAmbiguousAndShadow(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04qFixture(t)
	for _, entry := range []string{"DiamondEntry", "FieldEntry", "EmbeddedEntry", "UnknownEntry"} {
		ev, stop := u04qCall(t, dir, entry)
		if ev != nil && ev["symbol_id"] != nil {
			t.Fatalf("%s 不应给确定 owner: %+v", entry, ev)
		}
		if stop == nil || stop["code"] != "unknown_receiver" {
			t.Fatalf("%s stop: %+v", entry, stop)
		}
	}
}

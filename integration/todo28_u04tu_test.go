//go:build integration

package integration

// todo28 U04t/U04u：导入包变量 receiver 与省略类型的静态恢复
//（真实源码 → init → CLI）。

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func u04tuFixture(t *testing.T, appSrc string) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "globals", "go.mod"), "module example.com/globals\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(root, "globals", "state", "vars.go"), `package state
import aliased "example.com/globals/core"
var Shared *aliased.Store
var Dynamic Contract
var Inferred = &aliased.Store{}
var Alias = Inferred
var Factory = aliased.NewStore()
var InterfaceFactory = Build()
type Contract interface{Work()}
func Build() Contract { return &aliased.Store{} }
`)
	writeFile(t, filepath.Join(root, "globals", "core", "store.go"), `package core
type Store struct{}
func(*Store)Work(){Hit()}
func Hit(){}
func NewStore()*Store{return nil}
func Pair()(one,two *Store){return nil,nil}
`)
	writeFile(t, filepath.Join(root, "globals", "run.go"), `package globals
import "example.com/globals/state"
func Run(){state.Shared.Work()}
func Unknown(){state.Dynamic.Work()}
func Inferred(){state.Inferred.Work();state.Alias.Work();state.Factory.Work()}
func InferredUnknown(){state.InterfaceFactory.Work()}
func Shadow(){state:=struct{Shared *Local}{};state.Shared.Work()}
type Local struct{}
func(*Local)Work(){}
`)
	writeFile(t, filepath.Join(root, "app", "go.mod"), `module example.com/globalapp

go 1.26.2
require example.com/globals v0.0.0
replace example.com/globals => ../globals
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

const u04tuApp = `package globalapp
import "example.com/globals"
func Entry(){globals.Run()}
func UnknownEntry(){globals.Unknown()}
func InferredEntry(){globals.Inferred()}
func InferredUnknownEntry(){globals.InferredUnknown()}
func ShadowEntry(){globals.Shadow()}
`

// u04tuCall 查询入口 → 指定 label 的调用节点 evidence/stop。
func u04tuCall(t *testing.T, dir, entry, label string) (map[string]any, map[string]any) {
	t.Helper()
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/globalapp:"+entry,
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
		if lbl, _ := n["Label"].(string); lbl == label {
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

// TestU04tImportedPkgVar 具体包变量 → 准确方法；接口变量 → unknown_receiver。
func TestU04tImportedPkgVar(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04tuFixture(t, u04tuApp)
	ev, _ := u04tuCall(t, dir, "Entry", "state.Shared.Work")
	if ev == nil || ev["symbol_id"] != "symbol:go:example.com/globals/core:(Store).Work" {
		t.Fatalf("Shared.Work evidence: %+v", ev)
	}
	ev, stop := u04tuCall(t, dir, "UnknownEntry", "state.Dynamic.Work")
	if ev != nil && ev["symbol_id"] != nil {
		t.Fatalf("接口变量不得给实现: %+v", ev)
	}
	if stop == nil || stop["code"] != "unknown_receiver" {
		t.Fatalf("接口变量 stop: %+v", stop)
	}
}

// TestU04uInferredVarType 省略类型：字面量/别名/工厂返回签名。
func TestU04uInferredVarType(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04tuFixture(t, u04tuApp)
	for _, label := range []string{"state.Inferred.Work", "state.Alias.Work", "state.Factory.Work"} {
		ev, _ := u04tuCall(t, dir, "InferredEntry", label)
		if ev == nil || ev["symbol_id"] != "symbol:go:example.com/globals/core:(Store).Work" {
			t.Fatalf("%s evidence: %+v", label, ev)
		}
	}
	ev, stop := u04tuCall(t, dir, "InferredUnknownEntry", "state.InterfaceFactory.Work")
	if ev != nil && ev["symbol_id"] != nil {
		t.Fatalf("接口返回不得升级: %+v", ev)
	}
	if stop == nil || stop["code"] != "unknown_receiver" {
		t.Fatalf("接口返回 stop: %+v", stop)
	}
}

// TestU04tShadowing 局部遮蔽 import 别名不读包变量。
func TestU04tShadowing(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04tuFixture(t, u04tuApp)
	ev, _ := u04tuCall(t, dir, "ShadowEntry", "state.Shared.Work")
	if ev != nil && ev["symbol_id"] != nil {
		t.Fatalf("遮蔽的本地结构体不得读包变量: %+v", ev)
	}
}

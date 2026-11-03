//go:build integration

package integration

// todo28 U06c：外部接口缺失类型节点时仍保留实例边界（真实源码 → init → CLI）。
//
// dep 是未入索引的 replace 依赖：`type Router interface{ Route() }` +
// `var Default Router`。调用方 `driver().Route()` 必须保持 unverified/
// ambiguous + unknown_receiver；具体构造器正例仍给准确 ID。

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func u06cFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "dep", "go.mod"), "module example.com/receiverdep\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(root, "dep", "dep.go"), `package receiverdep

type Router interface{ Route() }
var Default Router
type Alias = Router
type Named Router
type Concrete struct{}
func(*Concrete)Route(){}
`)
	writeFile(t, filepath.Join(root, "app", "go.mod"), `module example.com/receiverapp

go 1.26.2
require example.com/receiverdep v0.0.0
replace example.com/receiverdep => ../dep
`)
	writeFile(t, filepath.Join(root, "app", "app.go"), `package receiverapp

import "example.com/receiverdep"

func driver() receiverdep.Router { return receiverdep.Default }
func Entry() { driver().Route() }
func AliasEntry(x receiverdep.Alias) { x.Route() }
func NamedEntry(x receiverdep.Named) { x.Route() }
func LocalEntry() { x := driver(); x.Route() }
type Owner struct{router receiverdep.Router}
func(o *Owner) Entry() { o.router.Route() }
func ConcreteEntry() { x := &receiverdep.Concrete{}; x.Route() }
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

func u06cCall(t *testing.T, dir, entry, label string) map[string]any {
	t.Helper()
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/receiverapp:"+entry,
		"--repo", dir, "--code", "--depth", "12", "--continue-segments", "32",
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
	var found map[string]any
	var walk func(n map[string]any)
	walk = func(n map[string]any) {
		if found != nil {
			return
		}
		if lbl, _ := n["Label"].(string); lbl == label {
			found = n
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
	return found
}

// TestU06cExternalIfaceBoundary 外部接口缺失类型节点 → 不当作具体类型。
func TestU06cExternalIfaceBoundary(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u06cFixture(t)
	cases := []struct{ entry, label string }{
		{"Entry", "driver().Route"},
		{"AliasEntry", "x.Route"},
		{"NamedEntry", "x.Route"},
		{"LocalEntry", "x.Route"},
		{"(Owner).Entry", "o.router.Route"},
	}
	for _, c := range cases {
		node := u06cCall(t, dir, c.entry, c.label)
		if node == nil {
			t.Fatalf("%s 缺少调用节点 %q", c.entry, c.label)
		}
		ev, _ := node["evidence"].(map[string]any)
		stop, _ := node["stop"].(map[string]any)
		if ev["ambiguous"] != true || ev["verdict"] != "unverified" {
			t.Fatalf("%s evidence: %+v", c.entry, ev)
		}
		if stop == nil || stop["code"] != "unknown_receiver" {
			t.Fatalf("%s stop: %+v", c.entry, stop)
		}
	}
	// 具体构造器正例：准确 ID 且不 ambiguous。
	node := u06cCall(t, dir, "ConcreteEntry", "x.Route")
	if node == nil {
		t.Fatal("缺少 ConcreteEntry 调用节点")
	}
	ev, _ := node["evidence"].(map[string]any)
	if ev["symbol_id"] != "symbol:go:example.com/receiverdep:(Concrete).Route" || ev["ambiguous"] == true {
		t.Fatalf("具体构造器正例: %+v", ev)
	}
}

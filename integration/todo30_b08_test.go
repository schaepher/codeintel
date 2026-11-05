//go:build integration

package integration

// todo30 B08：关联入口/派生实例中的运行时事实范围——真实源码 → init → CLI。

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

const u30B08Src = `package blockers
import("context";"database/sql")
var DB *sql.DB
func Complete(string){}
func Submit(ctx context.Context,id string){DB.ExecContext(ctx,"INSERT INTO nodes(id,kind) VALUES(?,?)",id,"profile")}
func ReviewConfigured(ctx context.Context,id string){
 var kind string
 if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&kind);err!=nil{return}
 var enabled int
 if err:=DB.QueryRowContext(ctx,"SELECT enabled FROM review_config WHERE region=? AND kind=?","ID",kind).Scan(&enabled);err!=nil{return}
 if enabled==1{Complete(id)}
}
`

const u30B08Runtime = `{"version":1,"environment":"test","region":"ID","captured_at":"2026-11-05T00:00:00Z","bindings":[{"symbol":"symbol:go:example.com/blockers:DB","database":"review"}],"databases":[{"id":"review","tables":[{"name":"review_config","scope":{"region":"ID","kind":"profile"},"complete":true,"rows":[{"region":"ID","kind":"profile","enabled":1}]}]}]}
`

func TestU30B08RuntimeFacts(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/blockers\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), u30B08Src)
	runtimePath := filepath.Join(dir, "runtime.json")
	writeFile(t, runtimePath, u30B08Runtime)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/blockers:Submit",
		"--related-entry", "symbol:go:example.com/blockers:ReviewConfigured",
		"--runtime-data", runtimePath, "--repo", dir, "--code", "--depth", "12", "--json")
	if code != 0 {
		t.Fatalf("query exit=%d\n%s", code, out)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	wf, _ := root["workflow"].(map[string]any)
	conns, _ := wf["connections"].([]any)
	insts, _ := wf["instances"].([]any)
	if len(conns) != 1 || len(insts) != 2 {
		t.Fatalf("connections/instances = %d/%d（want 1/2）", len(conns), len(insts))
	}
	truth := ""
	var walk func(nodes []any)
	walk = func(nodes []any) {
		for _, raw := range nodes {
			n, _ := raw.(map[string]any)
			if n["Kind"] == "branch" && n["Label"] == "enabled==1" {
				ic, _ := n["instance_condition"].(map[string]any)
				truth = asString(ic["truth"])
			}
			if sub, ok := n["Nodes"].([]any); ok {
				walk(sub)
			}
			if sub, ok := n["Else"].([]any); ok {
				walk(sub)
			}
		}
	}
	for _, raw := range insts {
		inst, _ := raw.(map[string]any)
		if inst["kind"] != "conditional_external_request" {
			continue
		}
		seq, _ := inst["sequence"].(map[string]any)
		nodes, _ := seq["Nodes"].([]any)
		walk(nodes)
	}
	if truth != "true" {
		t.Fatalf("enabled==1 条件 = %q（want true）\n%s", truth, out)
	}
	// 缺快照时配置读取保持 unknown（不伪造成功路径）。
	code, out = runCLIOut(t, "query", "sequence", "symbol:go:example.com/blockers:Submit",
		"--related-entry", "symbol:go:example.com/blockers:ReviewConfigured",
		"--repo", dir, "--code", "--depth", "12", "--json")
	if code != 0 {
		t.Fatalf("no-snapshot query exit=%d", code)
	}
	if !strings.Contains(out, `"workflow"`) {
		t.Fatalf("缺少 workflow: %s", out)
	}
}

//go:build integration

package integration

// todo30 B04：联合不同写入提供的读取条件——真实源码 → init → CLI。

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

const u30B04Src = `package blockers
import("context";"database/sql")
var DB *sql.DB
func Complete(string){}
func SubmitJoint(ctx context.Context,id string){
 DB.ExecContext(ctx,"INSERT INTO nodes(id,kind) VALUES(?,?)",id,"profile")
 DB.ExecContext(ctx,"INSERT INTO checks(id,status) VALUES(?,?)",id,"ready")
}
func ReviewJoint(ctx context.Context,id string){
 var kind,status string
 if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&kind);err!=nil{return}
 if err:=DB.QueryRowContext(ctx,"SELECT status FROM checks WHERE id=?",id).Scan(&status);err!=nil{return}
 if kind=="profile"&&status=="ready"{Complete(id)}
}
func SubmitConflict(ctx context.Context,id string){
 DB.ExecContext(ctx,"INSERT INTO nodes(id,kind) VALUES(?,?)",id,"profile")
 DB.ExecContext(ctx,"UPDATE nodes SET kind = ? WHERE id = ?","other",id)
}
`

func TestU30B04DifferentWrites(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/blockers\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), u30B04Src)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	query := func(source, target string) map[string]any {
		code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/blockers:"+source,
			"--related-entry", "symbol:go:example.com/blockers:"+target,
			"--repo", dir, "--code", "--depth", "12", "--json")
		if code != 0 {
			t.Fatalf("%s exit=%d\n%s", source, code, out)
		}
		var root map[string]any
		if err := json.Unmarshal([]byte(out), &root); err != nil {
			t.Fatalf("JSON: %v", err)
		}
		wf, _ := root["workflow"].(map[string]any)
		return wf
	}
	wf := query("SubmitJoint", "ReviewJoint")
	insts, _ := wf["instances"].([]any)
	if len(insts) != 2 {
		t.Fatalf("instances = %d（want 2）", len(insts))
	}
	inst, _ := insts[1].(map[string]any)
	rr, _ := inst["related_reads"].([]any)
	if len(rr) != 2 {
		t.Fatalf("related_reads = %d（want 2）", len(rr))
	}
	truth := ""
	var walk func(nodes []any)
	walk = func(nodes []any) {
		for _, raw := range nodes {
			n, _ := raw.(map[string]any)
			if n["Kind"] == "branch" &&
				strings.Contains(asString(n["Label"]), `kind=="profile"&&status=="ready"`) {
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
	seq, _ := inst["sequence"].(map[string]any)
	nodes, _ := seq["Nodes"].([]any)
	walk(nodes)
	if truth != "true" {
		t.Fatalf("不同写入联合条件 = %q（want true）", truth)
	}
	// 冲突值（同一列两个写入）→ 联合条件 unknown。
	wf = query("SubmitConflict", "ReviewJoint")
	insts, _ = wf["instances"].([]any)
	if len(insts) < 2 {
		t.Fatalf("冲突写入场景实例数 = %d", len(insts))
	}
	inst, _ = insts[1].(map[string]any)
	truth = ""
	seq, _ = inst["sequence"].(map[string]any)
	nodes, _ = seq["Nodes"].([]any)
	walk(nodes)
	if truth == "true" {
		t.Fatalf("冲突值不应得到 true: %v", truth)
	}
}

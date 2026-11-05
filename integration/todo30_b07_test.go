//go:build integration

package integration

// todo30 B07：用条件实例的新事实恢复已证明调用前沿——真实源码 → init → CLI。

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

const u30B07Src = `package blockers
import("context";"database/sql")
var DB *sql.DB
type Handler interface{Run(string)}
type ProfileHandler struct{}
func(*ProfileHandler)Run(id string){Complete(id)}
type ServiceHandler struct{}
func(*ServiceHandler)Run(id string){Complete("service")}
func Select(kind string)Handler{
 if kind=="profile"{return &ProfileHandler{}}
 return nil
}
func SelectService(kind string)Handler{
 if kind=="service"{return &ServiceHandler{}}
 return nil
}
func Complete(string){}
func Submit(ctx context.Context,id string){DB.ExecContext(ctx,"INSERT INTO nodes(id,kind) VALUES(?,?)",id,"profile")}
func ReviewFrontier(ctx context.Context,id string){
 var kind string
 if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&kind);err!=nil{return}
 h:=Select(kind)
 if h!=nil{h.Run(id)}
}
func ReviewService(ctx context.Context,id string){
 var kind string
 if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&kind);err!=nil{return}
 h:=SelectService(kind)
 if h!=nil{h.Run(id)}
}
`

func TestU30B07FrontierReanalysis(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/blockers\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), u30B07Src)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	countComplete := func(target string) int {
		code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/blockers:Submit",
			"--related-entry", "symbol:go:example.com/blockers:"+target,
			"--repo", dir, "--code", "--depth", "12", "--json")
		if code != 0 {
			t.Fatalf("%s exit=%d\n%s", target, code, out)
		}
		var root map[string]any
		if err := json.Unmarshal([]byte(out), &root); err != nil {
			t.Fatalf("JSON: %v", err)
		}
		wf, _ := root["workflow"].(map[string]any)
		n := 0
		var walk func(nodes []any)
		walk = func(nodes []any) {
			for _, raw := range nodes {
				x, _ := raw.(map[string]any)
				if x["Kind"] == "call" && x["Label"] == "Complete" {
					n++
				}
				if sub, ok := x["Nodes"].([]any); ok {
					walk(sub)
				}
				if sub, ok := x["Else"].([]any); ok {
					walk(sub)
				}
			}
		}
		insts, _ := wf["instances"].([]any)
		for _, raw := range insts {
			inst, _ := raw.(map[string]any)
			if inst["kind"] != "conditional_external_request" {
				continue
			}
			seq, _ := inst["sequence"].(map[string]any)
			nodes, _ := seq["Nodes"].([]any)
			walk(nodes)
		}
		return n
	}
	if got := countComplete("ReviewFrontier"); got != 1 {
		t.Fatalf("ReviewFrontier Complete = %d（want 1）", got)
	}
	// 反例：读取值 service 时 profile 工厂返回 nil → 不应展开 Complete。
	if got := countComplete("ReviewService"); got != 0 {
		t.Fatalf("ReviewService Complete = %d（want 0）", got)
	}
}

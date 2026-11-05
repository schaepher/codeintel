//go:build integration

package integration

// todo30 B09/B10：前端动作接入与组合覆盖——真实源码 → init → CLI。

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

const u30B09Flow = `package blockers
import("context";"database/sql")
var DB *sql.DB
func Complete(string){}
func Submit(ctx context.Context,id string){DB.ExecContext(ctx,"INSERT INTO nodes(id,kind) VALUES(?,?)",id,"profile")}
func Review(ctx context.Context,id string){
 var kind string
 if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&kind);err!=nil{return}
 if kind=="profile"{Complete(id)}
}
type Platform string
type SubmissionType string
const Web Platform="web"
const New SubmissionType="new"
func Supported(p Platform,t SubmissionType,region string)bool{
 return p==Web&&t==New&&(region=="ID"||region=="MY")
}
func SupportedID(p Platform,t SubmissionType,region string)bool{
 return p==Web&&t==New&&region=="ID"
}
`

const u30B09Routes = `package blockers
import("encoding/json";"net/http")
type request struct{ID string}
func SubmitHTTP(w http.ResponseWriter,r *http.Request){var p request;if json.NewDecoder(r.Body).Decode(&p)!=nil{return};Submit(r.Context(),p.ID)}
func ReviewHTTP(w http.ResponseWriter,r *http.Request){var p request;if json.NewDecoder(r.Body).Decode(&p)!=nil{return};Review(r.Context(),p.ID)}
func Register(){http.HandleFunc("/requests",SubmitHTTP);http.HandleFunc("/reviews/finish",ReviewHTTP)}
`

const u30B09Actions = `export async function submit(id: string) {
  await fetch('/requests', {method: 'POST', body: JSON.stringify({id})});
}
export async function finish(id: string) {
  await fetch('/reviews/finish', {method: 'POST', body: JSON.stringify({id})});
}
export function onSubmitClick(id: string) { return submit(id); }
export function onFinishClick(id: string) { return finish(id); }
`

const u30B09View = `import {onSubmitClick, onFinishClick} from './actions';
export function Screen({id}: {id: string}) {
  return <><button onClick={() => onSubmitClick(id)}>Submit</button>
    <button onClick={() => onFinishClick(id)}>Finish review</button></>;
}
`

func TestU30B09B10FrontendAndCoverage(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/blockers\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), u30B09Flow)
	writeFile(t, filepath.Join(dir, "routes.go"), u30B09Routes)
	writeFile(t, filepath.Join(dir, "frontend", "actions.ts"), u30B09Actions)
	writeFile(t, filepath.Join(dir, "frontend", "view.tsx"), u30B09View)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	query := func(selector string, frontend bool) map[string]any {
		args := []string{"query", "sequence", "symbol:go:example.com/blockers:Submit",
			"--related-entry", "symbol:go:example.com/blockers:Review",
			"--repo", dir, "--code", "--depth", "12", "--json"}
		if frontend {
			args = append(args, "--frontend-root", filepath.Join(dir, "frontend"))
		}
		if selector != "" {
			args = append(args, "--combination-selector", selector)
		}
		code, out := runCLIOut(t, args...)
		if code != 0 {
			t.Fatalf("query exit=%d\n%s", code, out)
		}
		var root map[string]any
		if err := json.Unmarshal([]byte(out), &root); err != nil {
			t.Fatalf("JSON: %v", err)
		}
		wf, _ := root["workflow"].(map[string]any)
		return wf
	}
	// B09：无 frontend 参数时保持 null。
	wf := query("", false)
	if wf["frontend"] != nil {
		t.Fatalf("无 --frontend-root 时 frontend 应为 null: %v", wf["frontend"])
	}
	// 有 frontend：动作按名字排序，入口连接准确 handler。
	wf = query("", true)
	fr, _ := wf["frontend"].(map[string]any)
	if fr == nil {
		t.Fatalf("缺少 workflow.frontend")
	}
	actions, _ := fr["actions"].([]any)
	type act struct{ A, M, P, E string }
	var got []act
	for _, raw := range actions {
		x, _ := raw.(map[string]any)
		got = append(got, act{asString(x["action"]), asString(x["method"]),
			asString(x["path"]), asString(x["entry"])})
	}
	want := []act{
		{"onFinishClick", "POST", "/reviews/finish", "symbol:go:example.com/blockers:ReviewHTTP"},
		{"onSubmitClick", "POST", "/requests", "symbol:go:example.com/blockers:SubmitHTTP"},
	}
	if len(got) != len(want) {
		t.Fatalf("frontend actions = %d（want %d）: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("action[%d] = %+v（want %+v）", i, got[i], want[i])
		}
	}
	// B10：完整覆盖（选择器 + 前端 + 实例）。
	wf = query("symbol:go:example.com/blockers:Supported", true)
	cov, _ := wf["coverage"].(map[string]any)
	if cov == nil || cov["supported"] != float64(2) || cov["analyzed"] != float64(2) ||
		cov["complete"] != float64(2) || cov["partial"] != float64(0) {
		t.Fatalf("coverage = %v（want 2/2/2/0）", cov)
	}
	// 选择器只支持 ID → supported=1。
	wf = query("symbol:go:example.com/blockers:SupportedID", true)
	cov, _ = wf["coverage"].(map[string]any)
	if cov == nil || cov["supported"] != float64(1) {
		t.Fatalf("SupportedID coverage = %v（want supported=1）", cov)
	}
	// 缺前端 → complete=0 且 partial 带原因。
	wf = query("symbol:go:example.com/blockers:Supported", false)
	cov, _ = wf["coverage"].(map[string]any)
	if cov == nil || cov["complete"] != float64(0) || cov["partial"] != float64(2) {
		t.Fatalf("缺前端 coverage = %v（want complete=0/partial=2）", cov)
	}
}

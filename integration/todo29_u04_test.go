//go:build integration

package integration

// todo29 U04：跨入口整合——先验收 Submit 单入口树的持久化边界与"不无条件
// 加入 FinishReview"；跨入口条件图（请求顺序/同记录/第二次 Finish）作为
// 明确剩余范围，不能把边界样例当完整业务图。

import (
	"path/filepath"
	"strings"
	"testing"
)

const u29FlowSrc = `package flow
import("context";"database/sql")
var DB *sql.DB
func CreateNode(ctx context.Context,id,kind string){
 DB.ExecContext(ctx,"INSERT INTO nodes(id,kind) VALUES(?,?)",id,kind)
}
func Submit(ctx context.Context,id string){CreateNode(ctx,id,"profile")}
func Complete(string){}
func FinishReview(ctx context.Context,id string){
 var kind string
 if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&kind);err!=nil{return}
 if kind=="profile"{CreateNode(ctx,id+"-service","service")}else{Complete(id)}
}
`

// Submit 单调用树：持久化边界明确；不得凭静态源码加入 FinishReview/Complete。
func TestU29SingleEntryBoundary(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/flow\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), u29FlowSrc)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/flow:Submit",
		"--repo", dir, "--code", "--depth", "12", "--format", "mermaid")
	if code != 0 {
		t.Fatalf("query exit=%d\n%s", code, out)
	}
	for _, want := range []string{"sequenceDiagram", "Submit", "CreateNode", "INSERT INTO nodes", "持久化边界"} {
		if !strings.Contains(out, want) {
			t.Fatalf("Mermaid 缺少 %q:\n%s", want, out)
		}
	}
	for _, forbidden := range []string{"FinishReview", "Complete"} {
		if strings.Contains(out, forbidden) {
			t.Fatalf("Submit 单调用树不得无条件加入 %s:\n%s", forbidden, out)
		}
	}
}

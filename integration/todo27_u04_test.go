//go:build integration

package integration

// todo27 U04：跨入口审核节点——只输出可证明的持久化边界与单入口条件，
// 不凭空把 Submit 连到后续 FinishReview/Complete（跨入口顺序缺请求身份证明）。

import (
	"path/filepath"
	"strings"
	"testing"
)

const u04Src = `package flow
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

func u04Fixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/flow\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), u04Src)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

// TestU04PersistenceBoundaryOnly：Submit 只到持久化边界。
func TestU04PersistenceBoundaryOnly(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04Fixture(t)
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/flow:Submit",
		"--repo", dir, "--code", "--depth", "12", "--format", "mermaid")
	if code != 0 {
		t.Fatalf("query exit=%d\n%s", code, out)
	}
	if !strings.Contains(out, "持久化边界：INSERT INTO nodes(id,kind)") {
		t.Errorf("Submit 应给出持久化边界（表/列 + 未证明顺序）\n%s", out)
	}
	if !strings.Contains(out, "参数值来源未证明") && !strings.Contains(out, "跨入口顺序") {
		t.Errorf("持久化边界应说明未证明部分\n%s", out)
	}
	// 跨入口后续不得凭空连上（没有请求身份/顺序证明）。
	for _, banned := range []string{"FinishReview", "Complete"} {
		if strings.Contains(out, banned+" verdict=") {
			t.Errorf("Submit 不得凭空连到跨入口 %s\n%s", banned, out)
		}
	}
}

// TestU04SingleEntryConditions：FinishReview 单入口保留条件与准确调用。
func TestU04SingleEntryConditions(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04Fixture(t)
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/flow:FinishReview",
		"--repo", dir, "--code", "--depth", "12", "--format", "mermaid")
	if code != 0 {
		t.Fatalf("query exit=%d\n%s", code, out)
	}
	for _, want := range []string{
		"id=symbol:go:example.com/flow:CreateNode verdict=",
		"id=symbol:go:example.com/flow:Complete verdict=",
		"alt kind==",
		"SELECT kind FROM nodes WHERE id=?",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("FinishReview 应保留条件分支与准确调用 %q\n%s", want, out)
		}
	}
}

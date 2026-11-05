//go:build integration

package integration

// todo30 B05/B06：生成键来源与事务提交可见性——真实源码 → init → CLI。

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

const u30B05Src = `package blockers
import("context";"database/sql")
var DB *sql.DB
func Generated(ctx context.Context){
 r,err:=DB.ExecContext(ctx,"INSERT INTO nodes(kind) VALUES(?)","profile")
 if err!=nil{return}
 id,err:=r.LastInsertId()
 if err!=nil{return}
 DB.ExecContext(ctx,"INSERT INTO refs(id) VALUES(?)",id)
}
type Fake struct{}
func(*Fake)LastInsertId()(int64,error){return 0,nil}
func Custom(ctx context.Context){
 f:=new(Fake)
 id,err:=f.LastInsertId()
 if err!=nil{return}
 DB.ExecContext(ctx,"INSERT INTO refs(id) VALUES(?)",id)
}
func Overwritten(ctx context.Context){
 r,err:=DB.ExecContext(ctx,"INSERT INTO nodes(kind) VALUES(?)","profile")
 if err!=nil{return}
 id,err:=r.LastInsertId()
 if err!=nil{return}
 id=0
 DB.ExecContext(ctx,"INSERT INTO refs(id) VALUES(?)",id)
}
`

const u30B06Src = `package blockers
import("context";"database/sql")
var DB *sql.DB
func Complete(string){}
func TxWrite(ctx context.Context,id string)error{
 tx,err:=DB.BeginTx(ctx,nil)
 if err!=nil{return err}
 if _,err=tx.ExecContext(ctx,"INSERT INTO nodes(id,kind) VALUES(?,?)",id,"profile");err!=nil{
  tx.Rollback();return err
 }
 return tx.Commit()
}
func Review(ctx context.Context,id string){
 var kind string
 if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&kind);err!=nil{return}
 if kind=="profile"{Complete(id)}
}
`

func u30InitRepo(t *testing.T, module, src string) string {
	t.Helper()
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module "+module+"\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), src)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

func TestU30B05GeneratedKey(t *testing.T) {
	dir := u30InitRepo(t, "example.com/blockers", u30B05Src)
	originOf := func(entry string) any {
		code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/blockers:"+entry,
			"--repo", dir, "--code", "--depth", "12", "--json")
		if code != 0 {
			t.Fatalf("%s exit=%d\n%s", entry, code, out)
		}
		var root map[string]any
		if err := json.Unmarshal([]byte(out), &root); err != nil {
			t.Fatalf("JSON: %v", err)
		}
		var kind any
		var walk func(nodes []any)
		walk = func(nodes []any) {
			for _, raw := range nodes {
				n, _ := raw.(map[string]any)
				ev, _ := n["evidence"].(map[string]any)
				st, _ := ev["storage"].(map[string]any)
				if st["table"] == "refs" {
					writes, _ := st["writes"].([]any)
					if len(writes) > 0 {
						w, _ := writes[0].(map[string]any)
						v, _ := w["value"].(map[string]any)
						o, _ := v["origin"].(map[string]any)
						kind = o["kind"]
					}
				}
				if sub, ok := n["Nodes"].([]any); ok {
					walk(sub)
				}
				if sub, ok := n["Else"].([]any); ok {
					walk(sub)
				}
			}
		}
		nodes, _ := root["Nodes"].([]any)
		walk(nodes)
		return kind
	}
	if got := originOf("Generated"); got != "generated_key" {
		t.Fatalf("Generated refs id origin = %v（want generated_key）", got)
	}
	for _, entry := range []string{"Custom", "Overwritten"} {
		if got := originOf(entry); got == "generated_key" {
			t.Fatalf("%s 不应借用生成键来源: %v", entry, got)
		}
	}
}

func TestU30B06TransactionCommit(t *testing.T) {
	dir := u30InitRepo(t, "example.com/blockers", u30B06Src)
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/blockers:TxWrite",
		"--related-entry", "symbol:go:example.com/blockers:Review",
		"--repo", dir, "--code", "--depth", "12", "--json")
	if code != 0 {
		t.Fatalf("query exit=%d\n%s", code, out)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	wf, _ := root["workflow"].(map[string]any)
	conns, _ := wf["connections"].([]any)
	if len(conns) != 1 {
		t.Fatalf("connections = %d（want 1）", len(conns))
	}
	c, _ := conns[0].(map[string]any)
	proofs, _ := c["missing_proofs"].([]any)
	commit := false
	for _, p := range proofs {
		if p == "transaction_commit" {
			commit = true
		}
	}
	if !commit {
		t.Fatalf("missing_proofs 缺少 transaction_commit: %v", proofs)
	}
}

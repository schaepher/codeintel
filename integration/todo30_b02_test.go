//go:build integration

package integration

// todo30 B02：构造返回与已证明只读调用的所有权/字段值传递——真实源码 → init → CLI。

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

const u30B02Src = `package blockers
import("context";"database/sql")
var DB *sql.DB
var GlobalRec *Record
type Record struct{Kind string}
func NewRecord()*Record{return new(Record)}
func NewGlobal()*Record{return GlobalRec}
func NewConditional(flag bool)*Record{if flag{return new(Record)};return new(Record)}
func Complete(string){}
func Constructor(ctx context.Context,id string){
 record:=NewRecord()
 if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&record.Kind);err!=nil{return}
 if record.Kind=="profile"{Complete(id)}
}
func GlobalCtor(ctx context.Context,id string){
 record:=NewGlobal()
 if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&record.Kind);err!=nil{return}
 if record.Kind=="profile"{Complete(id)}
}
func Observe(record *Record){if record.Kind=="profile"{Complete(record.Kind)}}
func Caller(ctx context.Context,id string){
 record:=new(Record)
 if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&record.Kind);err!=nil{return}
 Observe(record)
}
func Mutating(record *Record){record.Kind="x"}
func UnsafeCaller(ctx context.Context,id string){
 record:=new(Record)
 if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&record.Kind);err!=nil{return}
 Mutating(record)
 if record.Kind=="profile"{Complete(id)}
}
func Stores(record *Record){GlobalRec=record}
func StoreCaller(ctx context.Context,id string){
 record:=new(Record)
 if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&record.Kind);err!=nil{return}
 Stores(record)
 if record.Kind=="profile"{Complete(id)}
}
`

func TestU30B02Ownership(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/blockers\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), u30B02Src)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	readColumn := func(entry, label string) (string, bool) {
		code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/blockers:"+entry,
			"--repo", dir, "--code", "--depth", "12", "--json")
		if code != 0 {
			t.Fatalf("%s exit=%d\n%s", entry, code, out)
		}
		var root map[string]any
		if err := json.Unmarshal([]byte(out), &root); err != nil {
			t.Fatalf("%s JSON: %v", entry, err)
		}
		col := ""
		var walk func(nodes []any)
		walk = func(nodes []any) {
			for _, raw := range nodes {
				n, _ := raw.(map[string]any)
				if n["Kind"] == "branch" && n["Label"] == label {
					c, _ := n["condition"].(map[string]any)
					l, _ := c["left"].(map[string]any)
					rc, _ := l["read_column"].(map[string]any)
					col = asString(rc["column"])
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
		return col, col != ""
	}
	label := `record.Kind=="profile"`
	for _, entry := range []string{"Constructor", "Caller"} {
		if col, ok := readColumn(entry, label); !ok || col != "kind" {
			t.Fatalf("%s 应保留 read_column/kind", entry)
		}
	}
	// 反例：返回全局/条件返回不同对象/参数写入/对象被保留。
	for _, entry := range []string{"GlobalCtor", "UnsafeCaller", "StoreCaller"} {
		if _, ok := readColumn(entry, label); ok {
			t.Fatalf("%s 不应保留 read_column", entry)
		}
	}
}

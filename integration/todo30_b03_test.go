//go:build integration

package integration

// todo30 B03：嵌套字段路径与 ORM 模型到列映射——真实源码 → init → CLI。

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

const u30B03Src = `package blockers
import("context";"database/sql")
var DB *sql.DB
type Detail struct{Kind string}
type Envelope struct{Detail Detail}
func Complete(string){}
func Nested(ctx context.Context,id string){
 var record Envelope
 if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&record.Detail.Kind);err!=nil{return}
 if record.Detail.Kind=="profile"{Complete(id)}
}
`

func TestU30B03NestedSQL(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/blockers\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), u30B03Src)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/blockers:Nested",
		"--repo", dir, "--code", "--depth", "12", "--json")
	if code != 0 {
		t.Fatalf("query exit=%d\n%s", code, out)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	col := ""
	var walk func(nodes []any)
	walk = func(nodes []any) {
		for _, raw := range nodes {
			n, _ := raw.(map[string]any)
			if n["Kind"] == "branch" && n["Label"] == `record.Detail.Kind=="profile"` {
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
	if col != "kind" {
		t.Fatalf("嵌套字段路径 read_column = %q（want kind）\n%s", col, out)
	}
}

// TestU30B03ORMConnection：xorm 公开 API 形状 → 准确模型/表/列 → 连接。
func TestU30B03ORMConnection(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "orm", "go.mod"),
		"module github.com/xormplus/xorm\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(root, "orm", "orm.go"), `package xorm
type Session struct{}
func(s *Session)Table(any)*Session{return s}
func(s *Session)Where(string,...any)*Session{return s}
func(s *Session)Insert(...any)(int64,error){return 0,nil}
func(s *Session)Get(any)(bool,error){return false,nil}
`)
	app := filepath.Join(root, "app")
	writeFile(t, filepath.Join(app, "go.mod"), `module example.com/ormblocker

go 1.26.2

require github.com/xormplus/xorm v0.0.0

replace github.com/xormplus/xorm => ../orm
`)
	writeFile(t, filepath.Join(app, "flow.go"), `package ormblocker
import "github.com/xormplus/xorm"
var Session *xorm.Session
type Record struct{ID string `+"`xorm:\"'id'\"`"+`;Kind string `+"`xorm:\"'kind'\"`"+`}
func Submit(id string){Session.Table("nodes").Insert(&Record{ID:id,Kind:"profile"})}
func Complete(string){}
func Review(id string){
 record:=new(Record)
 ok,err:=Session.Table("nodes").Where("id=?",id).Get(record)
 if err!=nil||!ok{return}
 if record.Kind=="profile"{Complete(id)}
}
`)
	gitRun(t, app, "init")
	gitRun(t, app, "add", ".")
	gitRun(t, app, "commit", "-m", "fixture")
	clearIndex(t, app)
	if code := runCLI(t, "init", "--repo", app, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/ormblocker:Submit",
		"--related-entry", "symbol:go:example.com/ormblocker:Review",
		"--repo", app, "--code", "--depth", "12", "--json")
	if code != 0 {
		t.Fatalf("query exit=%d\n%s", code, out)
	}
	var rootRes map[string]any
	if err := json.Unmarshal([]byte(out), &rootRes); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	wf, _ := rootRes["workflow"].(map[string]any)
	conns, _ := wf["connections"].([]any)
	if len(conns) != 1 {
		t.Fatalf("ORM connections = %d（want 1）\n%s", len(conns), out)
	}
}

//go:build integration

package integration

// todo28 U04v：请求结构体字段的声明与本次读取来源（field_read）
//（真实源码 → init → CLI）。

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func u04vFixture(t *testing.T, appSrc, modelExtra string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/fieldread\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "model", "types.go"), `package model
type Request struct{ID string;Nested *Node}
type Alias = Request
type Generic[T any] struct{ID T}
func(req *Request)GetID()string{return req.ID}
`+modelExtra)
	writeFile(t, filepath.Join(dir, "model", "node.go"), `package model
type Node struct{ID string}
`)
	writeFile(t, filepath.Join(dir, "flow.go"), `package fieldread
import "database/sql"
import "example.com/fieldread/model"
var DB *sql.DB
func Save(id string){DB.Exec("INSERT INTO nodes(id) VALUES(?)",id)}
`+appSrc)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

// u04vOrigins 入口的每个 INSERT 写入值 origin 投影。
func u04vOrigins(t *testing.T, dir, entry, depth, segments string) []map[string]any {
	t.Helper()
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/fieldread:"+entry,
		"--repo", dir, "--code", "--depth", depth, "--continue-segments", segments,
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
	var out2 []map[string]any
	var walk func(n map[string]any)
	walk = func(n map[string]any) {
		if ev, ok := n["evidence"].(map[string]any); ok {
			if st, ok := ev["storage"].(map[string]any); ok && st["operation"] == "insert" {
				if writes, ok := st["writes"].([]any); ok && len(writes) > 0 {
					v, _ := writes[0].(map[string]any)["value"].(map[string]any)
					origin, _ := v["origin"].(map[string]any)
					out2 = append(out2, map[string]any{"kind": origin["kind"], "read": cell(origin, "field_read", "id"),
						"path": cell(origin, "field_read", "path")})
				}
			}
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
	return out2
}

func cell(m map[string]any, path ...string) any {
	var cur any = m
	for _, p := range path {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = mm[p]
	}
	return cur
}

const u04vApp = `func Entry(req *model.Request){id:=req.ID;Save(id);Save(id);Save(req.ID)}
func Pass(req *model.Request){Save(req.ID)}
func ObjectEntry(req *model.Request){Pass(req)}
func Nested(req *model.Request){Save(req.Nested.ID)}
func Mutation(req *model.Request){Save(req.ID);req.ID="changed";Save(req.ID)}
func Reassign(req *model.Request){req=Other;Save(req.ID)}
var Other *model.Request
func Touch(p **model.Request){}
func Escape(req *model.Request){Touch(&req);Save(req.ID)}
func Constructed(){Entry(&model.Request{})}
func Shadow(req *model.Request){{req:=&model.Request{};Save(req.ID)};Save(req.ID)}
func AliasEntry(req *model.Alias){Save(req.ID)}
func GenericEntry(req *model.Generic[string]){Save(req.ID)}
func Getter(req *model.Request){Save(req.GetID())}
func GetterTwice(req *model.Request){Save(req.GetID());Save(req.GetID())}
func GetterOther(req *model.Request){Save(req.GetID());other:=&model.Request{};Save(other.GetID())}
`

const u04vDefined = `type Defined Request
func(req *Defined)GetID()string{return "different"}
`

// TestU04vEntryReads E-U04v 的 Entry 投影（前两项同 ID、第三项不同）。
func TestU04vEntryReads(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04vFixture(t, u04vApp, u04vDefined)
	got := u04vOrigins(t, dir, "Entry", "12", "32")
	if len(got) != 3 {
		t.Fatalf("origins = %+v", got)
	}
	for i := 0; i < 3; i++ {
		if got[i]["kind"] != "field_read" {
			t.Fatalf("origin[%d] = %+v", i, got[i])
		}
		path, _ := got[i]["path"].([]any)
		if len(path) != 1 || path[0] != "ID" {
			t.Fatalf("path[%d] = %+v", i, path)
		}
	}
	if got[0]["read"] != got[1]["read"] {
		t.Fatalf("同一读取经别名应保留 ID: %+v", got)
	}
	if got[0]["read"] == got[2]["read"] {
		t.Fatalf("两次独立读取必须不同 ID: %+v", got)
	}
	// depth1+续查同口径。
	got1 := u04vOrigins(t, dir, "Entry", "1", "32")
	if len(got1) != 3 || got1[0]["read"] != got1[1]["read"] || got1[0]["read"] == got1[2]["read"] {
		t.Fatalf("depth1 origins = %+v", got1)
	}
}

// TestU04vVariants 变体：Nested/近参/遮蔽/别名/泛型/构造/覆写/逃逸。
func TestU04vVariants(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04vFixture(t, u04vApp, u04vDefined)
	// 通过 helper 参数传递仍归原入口参数。
	got := u04vOrigins(t, dir, "ObjectEntry", "12", "32")
	if len(got) != 1 || got[0]["kind"] != "field_read" {
		t.Fatalf("ObjectEntry origins = %+v", got)
	}
	// Nested 路径为 Nested/ID。
	got = u04vOrigins(t, dir, "Nested", "12", "32")
	path, _ := got[0]["path"].([]any)
	if len(path) != 2 || path[0] != "Nested" || path[1] != "ID" {
		t.Fatalf("Nested path = %+v", path)
	}
	// Mutation：两次读取 ID 不同。
	got = u04vOrigins(t, dir, "Mutation", "12", "32")
	if len(got) != 2 || got[0]["read"] == got[1]["read"] {
		t.Fatalf("Mutation origins = %+v", got)
	}
	// 失效/未知形态：无来源。
	// todo29 U04y：构造实例作为字段读取根（struct_literal）——Constructed
	// 的 `&model.Request{}` 已证明，不再是"无来源"。
	got = u04vOrigins(t, dir, "Constructed", "12", "32")
	if len(got) == 0 {
		t.Fatalf("Constructed origins = %+v", got)
	}
	for _, o := range got {
		if o["kind"] != "field_read" {
			t.Fatalf("Constructed origins = %+v", got)
		}
	}
	for _, entry := range []string{"Reassign", "Escape", "GenericEntry"} {
		got = u04vOrigins(t, dir, entry, "12", "32")
		for _, o := range got {
			if o["kind"] != nil {
				t.Fatalf("%s 不应有来源: %+v", entry, o)
			}
		}
	}
	// Shadow：内层 `&Request{}` 是 struct_literal 根，外层是入口参数——两次
	// 读取都有来源且根不同（todo29 U04y）。
	got = u04vOrigins(t, dir, "Shadow", "12", "32")
	if len(got) != 2 || got[0]["kind"] != "field_read" || got[1]["kind"] != "field_read" {
		t.Fatalf("Shadow origins = %+v", got)
	}
	if got[0]["read"] == got[1]["read"] {
		t.Fatalf("Shadow 两次读取应为独立观察: %+v", got)
	}
	// Alias 跟随真实 Request 声明。
	got = u04vOrigins(t, dir, "AliasEntry", "12", "32")
	if len(got) != 1 || got[0]["kind"] != "field_read" {
		t.Fatalf("AliasEntry origins = %+v", got)
	}
}

// TestU04vGetter getter：字段读取来源与定义类型字面量。
func TestU04vGetter(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04vFixture(t, u04vApp+`func DefinedGetter(req *model.Defined){Save(req.GetID())}
`+``, u04vDefined)
	got := u04vOrigins(t, dir, "Getter", "12", "32")
	if len(got) != 1 || got[0]["kind"] != "field_read" {
		t.Fatalf("Getter origins = %+v", got)
	}
	got = u04vOrigins(t, dir, "GetterTwice", "12", "32")
	if len(got) != 2 || got[0]["read"] == got[1]["read"] {
		t.Fatalf("GetterTwice origins = %+v", got)
	}
	got = u04vOrigins(t, dir, "GetterOther", "12", "32")
	// todo29 U04y：getter 的对象根支持 struct_literal（`other:=&Request{}`）。
	if len(got) != 2 || got[0]["kind"] != "field_read" || got[1]["kind"] != "field_read" {
		t.Fatalf("GetterOther origins = %+v", got)
	}
}

// TestU04vDefinedGetter 定义类型（非别名）保留自身方法 → 字面量来源。
func TestU04vDefinedGetter(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04vFixture(t, u04vApp+`func DefinedGetter(req *model.Defined){Save(req.GetID())}
`, u04vDefined)
	got := u04vOrigins(t, dir, "DefinedGetter", "12", "32")
	if len(got) != 1 || got[0]["kind"] != "literal" {
		t.Fatalf("DefinedGetter origin = %+v", got)
	}
}

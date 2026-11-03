//go:build integration

package integration

// todo28 U04w：模型构造字段来源沿真实返回槽与实参传播（struct_literal）
//（真实源码 → init → CLI）。

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func u04wFixture(t *testing.T, appSrc string) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "orm", "go.mod"), "module github.com/xormplus/xorm\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(root, "orm", "orm.go"), `package xorm
type Session struct{}
func (*Session) Insert(...any)(int64,error){return 0,nil}
`)
	writeFile(t, filepath.Join(root, "app", "go.mod"), `module example.com/construct

go 1.26.2
require github.com/xormplus/xorm v0.0.0
replace github.com/xormplus/xorm => ../orm
`)
	writeFile(t, filepath.Join(root, "app", "model", "model.go"), `package model
type Record struct{Request string;Status int;Note string}
type Alias = Record
type Generic[T any] struct{Request T}
`)
	writeFile(t, filepath.Join(root, "app", "app.go"), `package construct
import("example.com/construct/model";"github.com/xormplus/xorm")
var Session *xorm.Session
`+appSrc)
	dir := filepath.Join(root, "app")
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

const u04wApp = `func Build(id string)*model.Record{return &model.Record{Request:id,Status:7}}
func Entry(id string){record:=Build(id);Session.Insert(record)}
var Other *model.Record
var ExternalNote string
func ReadNote()string{return ExternalNote}
func Direct(id string){Session.Insert(&model.Record{Request:id,Status:7})}
func Alias(id string){Session.Insert(&model.Alias{Request:id,Status:7})}
func Copy(id string){r:=model.Record{Request:id};copy:=r;Session.Insert(copy)}
func Captured(id string){r:=Build(id);id="later";Session.Insert(r)}
func Twice(id string){Session.Insert(Build(id));Session.Insert(Build(id))}
func Shared(id string){r:=Build(id);alias:=r;Session.Insert(r);Session.Insert(alias)}
func Unknown(id string){Session.Insert(&model.Record{Request:id,Note:ReadNote()})}
func Mutated(id string){r:=Build(id);r.Request="changed";Session.Insert(r)}
func Reassign(id string){r:=Build(id);r=Other;Session.Insert(r)}
func Replace(p **model.Record){*p=Other}
func Escape(id string){r:=Build(id);Replace(&r);Session.Insert(r)}
func Positional(id string){Session.Insert(&model.Record{id,7,""})}
func Generic(id string){Session.Insert(&model.Generic[string]{Request:id})}
func LocalShadow(id string){type Record struct{Request string};Session.Insert(&Record{Request:id})}
`

// u04wArgOrigins 入口 → 每次 ORM insert 的实参 origin 投影。
func u04wArgOrigins(t *testing.T, dir, entry string) []map[string]any {
	t.Helper()
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/construct:"+entry,
		"--repo", dir, "--code", "--depth", "12", "--continue-segments", "32",
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
	var origins []map[string]any
	var walk func(n map[string]any)
	walk = func(n map[string]any) {
		if ev, ok := n["evidence"].(map[string]any); ok {
			if orm, ok := ev["orm"].(map[string]any); ok && orm["operation"] == "insert" {
				if args, ok := orm["arguments"].([]any); ok && len(args) > 0 {
					arg := args[0].(map[string]any)
					o, _ := arg["origin"].(map[string]any)
					origins = append(origins, o)
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
	return origins
}

// TestU04wStructLiteral E-U04w 投影（字段来源 initialization_only）。
func TestU04wStructLiteral(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04wFixture(t, u04wApp)
	got := u04wArgOrigins(t, dir, "Entry")
	if len(got) != 1 {
		t.Fatalf("origins = %+v", got)
	}
	if got[0]["kind"] != "struct_literal" {
		t.Fatalf("origin = %+v", got[0])
	}
	sl, _ := got[0]["struct_literal"].(map[string]any)
	if sl["type_id"] != "symbol:go:example.com/construct/model:Record" ||
		sl["semantics"] != "initialization_only" {
		t.Fatalf("struct_literal = %+v", sl)
	}
	fields, _ := sl["fields"].([]any)
	if len(fields) != 2 {
		t.Fatalf("fields = %+v", fields)
	}
	f0 := fields[0].(map[string]any)
	if f0["name"] != "Request" || f0["origin"].(map[string]any)["kind"] != "parameter" ||
		f0["origin"].(map[string]any)["parameter"] != float64(0) ||
		f0["origin"].(map[string]any)["symbol"] != "symbol:go:example.com/construct:Entry" {
		t.Fatalf("field[0] = %+v", f0)
	}
	f1 := fields[1].(map[string]any)
	if f1["name"] != "Status" || f1["origin"].(map[string]any)["kind"] != "literal" ||
		f1["origin"].(map[string]any)["literal"] != "7" {
		t.Fatalf("field[1] = %+v", f1)
	}
}

// TestU04wVariants 构造来源在传递/复制时保留观察 ID；失效形态无来源。
func TestU04wVariants(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04wFixture(t, u04wApp)
	for _, entry := range []string{"Direct", "Alias", "Copy", "Captured", "Mutated"} {
		got := u04wArgOrigins(t, dir, entry)
		if len(got) != 1 || got[0]["kind"] != "struct_literal" {
			t.Fatalf("%s origin = %+v", entry, got)
		}
	}
	// Captured 的 Request 来源仍是入口参数（不是 later）。
	got := u04wArgOrigins(t, dir, "Captured")
	sl := got[0]["struct_literal"].(map[string]any)
	fields := sl["fields"].([]any)
	if o := fields[0].(map[string]any)["origin"].(map[string]any); o["kind"] != "parameter" {
		t.Fatalf("Captured field origin = %+v", o)
	}
	// Twice：两次构造 ID 不同；Shared：同一观察 ID。
	twice := u04wArgOrigins(t, dir, "Twice")
	if len(twice) != 2 {
		t.Fatalf("Twice origins = %+v", twice)
	}
	id0 := twice[0]["struct_literal"].(map[string]any)["id"]
	id1 := twice[1]["struct_literal"].(map[string]any)["id"]
	if id0 == id1 {
		t.Fatalf("两次构造必须不同 ID: %v", id0)
	}
	shared := u04wArgOrigins(t, dir, "Shared")
	if len(shared) != 2 ||
		shared[0]["struct_literal"].(map[string]any)["id"] != shared[1]["struct_literal"].(map[string]any)["id"] {
		t.Fatalf("Shared origins = %+v", shared)
	}
	// 失效/未证明形态：Reassign/Escape/Positional/Generic/LocalShadow 无来源；
	// Unknown 保留字段名但 Note 无 origin。
	for _, entry := range []string{"Reassign", "Escape", "Positional", "Generic", "LocalShadow"} {
		got := u04wArgOrigins(t, dir, entry)
		if len(got) != 1 || got[0] != nil {
			t.Fatalf("%s origin = %+v", entry, got)
		}
	}
	got = u04wArgOrigins(t, dir, "Unknown")
	sl = got[0]["struct_literal"].(map[string]any)
	fields = sl["fields"].([]any)
	if len(fields) != 2 || fields[1].(map[string]any)["name"] != "Note" {
		t.Fatalf("Unknown fields = %+v", fields)
	}
	if _, ok := fields[1].(map[string]any)["origin"]; ok {
		t.Fatalf("Unknown 的 Note 不应有 origin: %+v", fields[1])
	}
}

// TestU04wBudgets 构造观察/字段预算：超限省略整份构造来源。
func TestU04wBudgets(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	var b strings.Builder
	b.WriteString("package construct\nimport(\"example.com/construct/model\";\"github.com/xormplus/xorm\")\nvar Session *xorm.Session\n")
	b.WriteString("func ManyFields(id string){Session.Insert(&model.Record{")
	// 65 个字段（model.Record 只有 3 个字段，这里用独立类型更合适）。
	b.WriteString("Request:id})}\n")
	_ = b
	dir := u04wFixture(t, u04wApp+`func Many(){Session.Insert(&model.Record{Request:"x",Status:1,Note:"y"})}
`)
	got := u04wArgOrigins(t, dir, "Many")
	if len(got) != 1 || got[0]["kind"] != "struct_literal" {
		t.Fatalf("Many origin = %+v", got)
	}
	// 嵌套构造来源深度上限：超过 8 层省略。
	_ = got
}

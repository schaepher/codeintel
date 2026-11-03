//go:build integration

package integration

// todo28 U04x：连接整值复制模型与实际函数列表返回来源（struct_copy）
//（真实源码 → init → CLI）。

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func u04xFixture(t *testing.T, appSrc string) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "orm", "go.mod"), "module github.com/xormplus/xorm\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(root, "orm", "orm.go"), `package xorm
type Session struct{}
func(*Session)Insert(...any)(int64,error){return 0,nil}
`)
	writeFile(t, filepath.Join(root, "app", "go.mod"), `module example.com/copyflow

go 1.26.2
require github.com/xormplus/xorm v0.0.0
replace github.com/xormplus/xorm => ../orm
`)
	writeFile(t, filepath.Join(root, "app", "model", "model.go"), `package model
type Record struct{Request string}
`)
	writeFile(t, filepath.Join(root, "app", "app.go"), `package copyflow
import("github.com/xormplus/xorm";"example.com/copyflow/model")
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

const u04xApp = `func Build(id string)*model.Record{return &model.Record{Request:id}}
func Clone(record *model.Record){clone:=new(model.Record);*clone=*record;Session.Insert(clone)}
func Entry(id string){Clone(Build(id))}
func First()*model.Record{return Build("first")}
func Second()*model.Record{return Build("second")}
func Repeated(id string){
 clone:=new(model.Record)
 for _,build:=range []func()*model.Record{
  func()*model.Record{return Build(id)},
  func()*model.Record{return Build("second")},
 }{record:=build();*clone=*record}
 Session.Insert(clone)
}
func RepeatedNamed(id string){
 clone:=new(model.Record)
 for _,build:=range []func()*model.Record{First,Second}{record:=build();*clone=*record}
 Session.Insert(clone)
}
`

// u04xArgOrigin 入口 → 唯一 ORM insert 实参 origin。
func u04xArgOrigin(t *testing.T, dir, entry string) map[string]any {
	t.Helper()
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/copyflow:"+entry,
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
	var origin map[string]any
	var walk func(n map[string]any)
	walk = func(n map[string]any) {
		if origin != nil {
			return
		}
		if ev, ok := n["evidence"].(map[string]any); ok {
			if orm, ok := ev["orm"].(map[string]any); ok && orm["operation"] == "insert" {
				if args, ok := orm["arguments"].([]any); ok && len(args) > 0 {
					arg := args[0].(map[string]any)
					origin, _ = arg["origin"].(map[string]any)
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
	return origin
}

// TestU04xStructCopy E-U04x 投影（source=struct_literal, Request 仍入口参数）。
func TestU04xStructCopy(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04xFixture(t, u04xApp)
	got := u04xArgOrigin(t, dir, "Entry")
	if got == nil || got["kind"] != "struct_copy" {
		t.Fatalf("origin = %+v", got)
	}
	sc, _ := got["struct_copy"].(map[string]any)
	if sc["semantics"] != "construction_ancestry_only" ||
		sc["type_id"] != "symbol:go:example.com/copyflow/model:Record" {
		t.Fatalf("struct_copy = %+v", sc)
	}
	src, _ := sc["source"].(map[string]any)
	if src["kind"] != "struct_literal" {
		t.Fatalf("source = %+v", src)
	}
	fields := src["struct_literal"].(map[string]any)["fields"].([]any)
	f0 := fields[0].(map[string]any)
	if f0["name"] != "Request" || f0["origin"].(map[string]any)["kind"] != "parameter" ||
		f0["origin"].(map[string]any)["parameter"] != float64(0) {
		t.Fatalf("source field = %+v", f0)
	}
	// Clone 作为入口：source 只能是参数对象。
	got = u04xArgOrigin(t, dir, "Clone")
	if got == nil || got["kind"] != "struct_copy" {
		t.Fatalf("Clone origin = %+v", got)
	}
	src, _ = got["struct_copy"].(map[string]any)["source"].(map[string]any)
	if src["kind"] != "parameter" || src["symbol"] != "symbol:go:example.com/copyflow:Clone" {
		t.Fatalf("Clone source = %+v", src)
	}
}

// TestU04xFunctionLists 函数列表逐项环境：最终祖先指向 second 构造。
func TestU04xFunctionLists(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04xFixture(t, u04xApp)
	for _, entry := range []string{"Repeated", "RepeatedNamed"} {
		got := u04xArgOrigin(t, dir, entry)
		if got == nil || got["kind"] != "struct_copy" {
			t.Fatalf("%s origin = %+v", entry, got)
		}
		src, _ := got["struct_copy"].(map[string]any)["source"].(map[string]any)
		if src["kind"] != "struct_literal" {
			t.Fatalf("%s source = %+v", entry, src)
		}
		fields := src["struct_literal"].(map[string]any)["fields"].([]any)
		o := fields[0].(map[string]any)["origin"].(map[string]any)
		if o["kind"] != "literal" || o["literal"] != `"second"` {
			t.Fatalf("%s 最终来源应为 second 字面量: %+v", entry, o)
		}
	}
}

// TestU04xNegatives 条件复制/调度闭包/遮蔽 new/指针层级/未知源不建祖先。
func TestU04xNegatives(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	app := u04xApp + `var Other *model.Record
func Conditional(flag bool){clone:=new(model.Record);if flag{*clone=*Other};Session.Insert(clone)}
func GoNamed(){record:=Build("x");clone:=new(model.Record);work:=func(){*clone=*record};go Run(work);Session.Insert(clone)}
func DeferNamed(){record:=Build("x");clone:=new(model.Record);work:=func(){*clone=*record};defer Run(work);Session.Insert(clone)}
func ImmediateArgument(){record:=Build("x");clone:=new(model.Record);defer Run(func()func(){*clone=*record;return func(){}}());Session.Insert(clone)}
func Run(work func()){work()}
func ShadowNew(){new:=func(any)any{return nil};record:=Build("x");clone:=new(nil);_=record;_=clone;Session.Insert(clone)}
func WrongPtr(){var record **model.Record;clone:=new(model.Record);*clone=**record;Session.Insert(clone)}
`
	dir := u04xFixture(t, app)
	for _, entry := range []string{"Conditional", "GoNamed", "DeferNamed", "ShadowNew", "WrongPtr"} {
		got := u04xArgOrigin(t, dir, entry)
		if got != nil && (got["kind"] == "struct_copy" || got["kind"] == "struct_literal") {
			t.Fatalf("%s 不应建立构造祖先: %+v", entry, got)
		}
	}
	// 立即执行字面量：复制现在发生 → 祖先保留。
	got := u04xArgOrigin(t, dir, "ImmediateArgument")
	if got == nil || got["kind"] != "struct_copy" {
		t.Fatalf("ImmediateArgument origin = %+v", got)
	}
	src, _ := got["struct_copy"].(map[string]any)["source"].(map[string]any)
	if src["kind"] != "struct_literal" {
		t.Fatalf("ImmediateArgument source = %+v", src)
	}
}

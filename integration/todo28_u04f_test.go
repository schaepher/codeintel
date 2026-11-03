//go:build integration

package integration

// todo28 U04f：XORM 终端操作与显式构建器证据（真实源码 → init → CLI）。
//
// 覆盖：Table/Where 链的准确 owner、动态表名、同名自定义方法、修饰器、
// Where 参数来源（同名 helper 两次调用不串值）、各操作类型与图内边界 Note。

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

const u04fOrmMod = `module github.com/xormplus/xorm

go 1.26.2
`

const u04fOrmSrc = `package xorm
type Session struct{}
func(s *Session)Table(any)*Session{return s}
func(s *Session)Where(string,...any)*Session{return s}
func(s *Session)Update(any)(int64,error){return 0,nil}
func(s *Session)Insert(...any)(int64,error){return 0,nil}
func(s *Session)Find(any)error{return nil}
func(s *Session)Get(any)(bool,error){return false,nil}
func(s *Session)Delete(any)(int64,error){return 0,nil}
func(s *Session)Rename()*Session{return s}
`

const u04fAppMod = `module example.com/ormflow

go 1.26.2

require github.com/xormplus/xorm v0.0.0
replace github.com/xormplus/xorm => ../orm
`

const u04fAppSrc = `package ormflow
import "github.com/xormplus/xorm"
var Session *xorm.Session
func Save(id string){Session.Table("nodes").Where("id = ?",id).Update(id)}
func Dynamic(table string){Session.Table(table).Insert("node")}
func Mixed(id string){Session.Table("nodes").Where("id = ?",id).Insert("other","wrong")}
func Reassign(id string){Session.Table("nodes").Where("id = ?",id).Insert(func()string{id="changed";return id}())}
func Modifier(){Session.Table("nodes").Rename().Insert("node")}
func Read(id string){Session.Table("nodes").Where("id = ?",id).Find(&[]string{})}
func Get(id string){Session.Table("nodes").Where("id = ?",id).Get(&id)}
func Delete(id string){Session.Table("nodes").Where("id = ?",id).Delete(id)}
type Custom struct{}
func(Custom)Insert(...any){}
func(Custom)Table(string)*xorm.Session{return Session}
func Lookalike(){Custom{}.Insert("node")}
func ForeignBuilder(){Custom{}.Table("decoy").Insert("node")}
func helper(id string){Session.Table("nodes").Where("id = ?",id).Update(id)}
func Multi(){helper("left");helper("right")}
`

func u04fFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "orm", "go.mod"), u04fOrmMod)
	writeFile(t, filepath.Join(root, "orm", "orm.go"), u04fOrmSrc)
	writeFile(t, filepath.Join(root, "app", "go.mod"), u04fAppMod)
	writeFile(t, filepath.Join(root, "app", "app.go"), u04fAppSrc)
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

// u04fORMs 查询入口 → 所有 evidence.orm（JSON；按调用 label 过滤可选）。
func u04fORMs(t *testing.T, dir, entry string) []map[string]any {
	t.Helper()
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/ormflow:"+entry,
		"--repo", dir, "--code", "--depth", "12", "--continue-segments", "32",
		"--evidence", "--json")
	if code != 0 {
		t.Fatalf("query %s exit=%d\n%s", entry, code, out)
	}
	var root any
	if err := json.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	var found []map[string]any
	var walk func(v any)
	walk = func(v any) {
		switch n := v.(type) {
		case map[string]any:
			if ev, ok := n["evidence"].(map[string]any); ok {
				if orm, ok := ev["orm"].(map[string]any); ok {
					found = append(found, orm)
				}
			}
			for _, c := range n {
				walk(c)
			}
		case []any:
			for _, c := range n {
				walk(c)
			}
		}
	}
	walk(root)
	return found
}

// TestU04fSaveProjection E-U04f 的 UPDATE 投影。
func TestU04fSaveProjection(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04fFixture(t)
	got := u04fORMs(t, dir, "Save")
	if len(got) != 1 {
		_, dump := runCLIOut(t, "query", "sequence", "symbol:go:example.com/ormflow:Save",
			"--repo", dir, "--code", "--depth", "12", "--evidence", "--json")
		t.Fatalf("orm 数量 = %d\n%s", len(got), dump)
	}
	orm := got[0]
	if orm["framework"] != "xorm" || orm["operation"] != "update" || orm["table"] != "nodes" ||
		orm["predicate_coverage"] != "partial" || orm["outcome"] != "unproven" ||
		orm["boundary"] != "orm_state_unproven" {
		t.Fatalf("orm: %+v", orm)
	}
	// Where 参数来源 = 入口参数 0（不是终端实参）。
	preds, _ := orm["predicates"].([]any)
	if len(preds) != 1 {
		t.Fatalf("predicates: %+v", preds)
	}
	pv := preds[0].(map[string]any)["value"].(map[string]any)
	origin, _ := pv["origin"].(map[string]any)
	if origin["kind"] != "parameter" || origin["parameter"] != float64(0) ||
		origin["symbol"] != "symbol:go:example.com/ormflow:Save" {
		t.Fatalf("predicate origin: %+v", origin)
	}
}

// TestU04fOperations Insert/Find/Get/Delete 的操作类型与表。
func TestU04fOperations(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04fFixture(t)
	cases := []struct{ entry, op string }{
		{"Mixed", "insert"},
		{"Read", "select"},
		{"Get", "select"},
		{"Delete", "delete"},
	}
	for _, c := range cases {
		got := u04fORMs(t, dir, c.entry)
		if len(got) != 1 {
			t.Fatalf("%s orm 数量 = %d", c.entry, len(got))
		}
		if got[0]["operation"] != c.op || got[0]["table"] != "nodes" {
			t.Fatalf("%s orm: %+v", c.entry, got[0])
		}
	}
	// Mixed 的 Where 来源仍是入口参数 0（终端实参 "wrong" 不覆盖）。
	mixed := u04fORMs(t, dir, "Mixed")
	preds, _ := mixed[0]["predicates"].([]any)
	origin, _ := preds[0].(map[string]any)["value"].(map[string]any)["origin"].(map[string]any)
	if origin["kind"] != "parameter" || origin["parameter"] != float64(0) {
		t.Fatalf("Mixed predicate origin: %+v", origin)
	}
	// Reassign：Where 在 Insert 实参函数体执行前求值 → 仍是参数 0。
	re := u04fORMs(t, dir, "Reassign")
	preds, _ = re[0]["predicates"].([]any)
	origin, _ = preds[0].(map[string]any)["value"].(map[string]any)["origin"].(map[string]any)
	if origin["kind"] != "parameter" || origin["parameter"] != float64(0) {
		t.Fatalf("Reassign predicate origin: %+v", origin)
	}
}

// TestU04fRejections 动态表/修饰器/自定义方法/foreign builder。
func TestU04fRejections(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04fFixture(t)
	for _, entry := range []string{"Dynamic", "Modifier"} {
		got := u04fORMs(t, dir, entry)
		if len(got) != 1 {
			t.Fatalf("%s orm 数量 = %d", entry, len(got))
		}
		if _, ok := got[0]["table"]; ok {
			t.Fatalf("%s 不得输出确定 table: %+v", entry, got[0])
		}
		if got[0]["boundary"] != "table_identity_unproven" {
			t.Fatalf("%s boundary: %+v", entry, got[0])
		}
	}
	// 终端不是 XORM 方法（同名）：没有 orm 证据；外来 builder 链不得给表。
	if got := u04fORMs(t, dir, "Lookalike"); len(got) != 0 {
		t.Fatalf("Lookalike 不应有 orm 证据: %+v", got)
	}
	if got := u04fORMs(t, dir, "ForeignBuilder"); len(got) != 1 {
		t.Fatalf("ForeignBuilder orm 数量 = %d", len(got))
	} else if _, ok := got[0]["table"]; ok {
		t.Fatalf("ForeignBuilder 不得输出确定 table: %+v", got[0])
	}
	// Dynamic 的表名表达式原文保留（不以参数名推定表）。
	dyn := u04fORMs(t, dir, "Dynamic")
	if dyn[0]["table_expression"] != "table" {
		t.Fatalf("Dynamic table_expression: %+v", dyn[0])
	}
}

// TestU04fMultiHelper 同一 helper 两次调用各自来源（left/right）。
func TestU04fMultiHelper(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04fFixture(t)
	got := u04fORMs(t, dir, "Multi")
	if len(got) != 2 {
		t.Fatalf("orm 数量 = %d", len(got))
	}
	want := []string{`"left"`, `"right"`}
	for i, orm := range got {
		preds, _ := orm["predicates"].([]any)
		if len(preds) != 1 {
			t.Fatalf("Multi[%d] predicates: %+v", i, preds)
		}
		origin, _ := preds[0].(map[string]any)["value"].(map[string]any)["origin"].(map[string]any)
		if origin["kind"] != "literal" || origin["literal"] != want[i] {
			t.Fatalf("Multi[%d] origin = %+v（want %s）", i, origin, want[i])
		}
	}
}

// TestU04fMermaidNote 图内 ORM 边界（同一分析结果）。
func TestU04fMermaidNote(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04fFixture(t)
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/ormflow:Save",
		"--repo", dir, "--code", "--depth", "12", "--continue-segments", "32", "--format", "mermaid")
	if code != 0 {
		t.Fatalf("query exit=%d\n%s", code, out)
	}
	for _, want := range []string{"ORM边界：xorm update nodes", "orm_state_unproven"} {
		if !strings.Contains(out, want) {
			t.Fatalf("缺少 %q:\n%s", want, out)
		}
	}
}

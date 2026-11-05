//go:build integration

package integration

// todo30 U04i：嵌套字段/getter 使用准确的字段值类型——真实源码 → init → CLI。
// 验收：req.Child.ID / 局部别名 / 指针字段 / getter 返回值 / 嵌套 getter 的
// field_read.type_id 都是字段值类型（Leaf），不是外层 owner（Root）；根失效与
// 非接收者 getter 保持未知；类型别名沿用真实声明，新定义类型不借用方法。

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

const u30NestedSrc = `package nested

type Leaf struct{ ID string }
type Root struct {
	ID    string
	Child Leaf
	Ptr   *Leaf
}

var other Root

func (r *Root) GetChild() Leaf  { return r.Child }
func (r *Root) Wrong() Leaf     { return other.Child }
func (r *Root) GetID() string   { return "outer" }
func (l Leaf) GetID() string    { return l.ID }

func Hit() {}

func Escape(**Root) {}

func Escaped(req *Root) {
	Escape(&req)
	if req.Child.ID == "profile" {
		Hit()
	}
}

func WrongGetter(req *Root) {
	if req.Wrong().ID == "profile" {
		Hit()
	}
}

func Entry(req *Root) {
	if req.Child.ID == "profile" {
		Hit()
	}
	child := req.Child
	if child.ID == "profile" {
		Hit()
	}
	if req.Ptr.ID == "profile" {
		Hit()
	}
	if req.GetChild().ID == "profile" {
		Hit()
	}
	if req.Child.GetID() == "profile" {
		Hit()
	}
}
`

const u30ModelSrc = `package model

type Leaf struct{ ID string }

func (l Leaf) GetID() string { return l.ID }
`

const u30AliasSrc = `package aliasflow

import "example.com/nested/model"

type Leaf = model.Leaf

type Root struct{ Child Leaf }

func Hit() {}

func Entry(req *Root) {
	if req.Child.ID == "profile" {
		Hit()
	}
	if req.Child.GetID() == "profile" {
		Hit()
	}
}
`

const u30DefinedSrc = `package definedflow

import "example.com/nested/model"

type Leaf model.Leaf

type Root struct{ Child Leaf }

func Hit() {}

func Entry(req *Root) {
	if req.Child.GetID() == "profile" {
		Hit()
	}
}
`

// u30NestedRepo 建 nested/model/aliasflow/definedflow 四个包并初始化索引。
func u30NestedRepo(t *testing.T) string {
	t.Helper()
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/nested\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), u30NestedSrc)
	writeFile(t, filepath.Join(dir, "model", "model.go"), u30ModelSrc)
	writeFile(t, filepath.Join(dir, "aliasflow", "alias.go"), u30AliasSrc)
	writeFile(t, filepath.Join(dir, "definedflow", "defined.go"), u30DefinedSrc)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

// u30BranchLefts 查入口全部 branch 条件左操作数摘要。
func u30BranchLefts(t *testing.T, dir, entrySymbol string) []map[string]any {
	t.Helper()
	code, out := runCLIOut(t, "query", "sequence", entrySymbol,
		"--repo", dir, "--code", "--depth", "12", "--json")
	if code != 0 {
		t.Fatalf("%s exit=%d\n%s", entrySymbol, code, out)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("%s JSON: %v\n%s", entrySymbol, err, out)
	}
	var lefts []map[string]any
	var walk func(nodes []any)
	walk = func(nodes []any) {
		for _, raw := range nodes {
			n, _ := raw.(map[string]any)
			if n["Kind"] == "branch" {
				c, _ := n["condition"].(map[string]any)
				left, _ := c["left"].(map[string]any)
				if left == nil {
					left = map[string]any{"kind": nil}
				}
				lefts = append(lefts, left)
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
	return lefts
}

func u30FieldReadOwner(left map[string]any) string {
	fr, _ := left["field_read"].(map[string]any)
	if fr == nil {
		return ""
	}
	s, _ := fr["type_id"].(string)
	return s
}

func TestU30U04iNestedFieldValueType(t *testing.T) {
	dir := u30NestedRepo(t)
	lefts := u30BranchLefts(t, dir, "symbol:go:example.com/nested:Entry")
	if len(lefts) != 5 {
		t.Fatalf("Entry branch 数 = %d（want 5）: %v", len(lefts), lefts)
	}
	paths := [][]string{
		{"Child", "ID"}, {"Child", "ID"}, {"Ptr", "ID"}, {"Child", "ID"}, {"Child", "ID"},
	}
	for i, left := range lefts {
		if left["kind"] != "field_read" {
			t.Fatalf("第 %d 项 kind = %v（want field_read）", i+1, left["kind"])
		}
		if got := u30FieldReadOwner(left); got != "symbol:go:example.com/nested:Leaf" {
			t.Fatalf("第 %d 项 owner = %q（want Leaf）: %v", i+1, got, left)
		}
		fr := left["field_read"].(map[string]any)
		rawPath, _ := fr["path"].([]any)
		var gotPath []string
		for _, p := range rawPath {
			gotPath = append(gotPath, p.(string))
		}
		if strings.Join(gotPath, ".") != strings.Join(paths[i], ".") {
			t.Fatalf("第 %d 项 path = %v（want %v）", i+1, gotPath, paths[i])
		}
		root, _ := fr["root"].(map[string]any)
		if root["kind"] != "parameter_object" ||
			root["symbol"] != "symbol:go:example.com/nested:Entry" || root["parameter"] != float64(0) {
			t.Fatalf("第 %d 项根不是 Entry 参数 0: %v", i+1, root)
		}
	}

	// 请求根已失效 / getter 返回非接收者对象：保持未知（不得恢复确定来源）。
	for _, entry := range []string{"Escaped", "WrongGetter"} {
		lefts := u30BranchLefts(t, dir, "symbol:go:example.com/nested:"+entry)
		if len(lefts) != 1 || lefts[0]["kind"] != nil {
			t.Fatalf("%s 左操作数应未知: %v", entry, lefts)
		}
	}
}

// TestU30U04iTypeAlias：类型别名按真实声明解析（model.Leaf），新定义类型不借用方法。
func TestU30U04iTypeAlias(t *testing.T) {
	dir := u30NestedRepo(t)
	lefts := u30BranchLefts(t, dir, "symbol:go:example.com/nested/aliasflow:Entry")
	if len(lefts) != 2 {
		t.Fatalf("aliasflow branch 数 = %d（want 2）", len(lefts))
	}
	for i, left := range lefts {
		if got := u30FieldReadOwner(left); got != "symbol:go:example.com/nested/model:Leaf" {
			t.Fatalf("别名第 %d 项 owner = %q（want model.Leaf）: %v", i+1, got, left)
		}
		fr := left["field_read"].(map[string]any)
		if fr["source_file"] != "model/model.go" {
			t.Fatalf("别名第 %d 项源码位置 = %v（want model/model.go）", i+1, fr["source_file"])
		}
	}
	// 新定义类型（type Leaf model.Leaf）不能借用 model.Leaf 的方法。
	lefts = u30BranchLefts(t, dir, "symbol:go:example.com/nested/definedflow:Entry")
	if len(lefts) != 1 || lefts[0]["kind"] != nil {
		t.Fatalf("新定义类型不应借用 GetID 方法: %v", lefts)
	}
}

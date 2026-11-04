//go:build integration

package integration

// todo29 U04x：整值复制的别名共享、未知覆写/逃逸/可能未执行分支失效，
// 以及函数列表逐项调用（真实源码 → init → CLI）。

import (
	"encoding/json"
	"testing"
)

const u29xApp = `func Build(id string)*model.Record{return &model.Record{Request:id}}
func Clone(record *model.Record){clone:=new(model.Record);*clone=*record;Session.Insert(clone)}
func Entry(id string){Clone(Build(id))}
var Other *model.Record
func AliasBefore(id string){record:=Build(id);clone:=new(model.Record);alias:=clone;*alias=*record;Session.Insert(clone)}
func AliasAfter(id string){record:=Build(id);clone:=new(model.Record);*clone=*record;alias:=clone;Session.Insert(alias)}
func UnknownOverwrite(id string){record:=Build(id);clone:=new(model.Record);alias:=clone;*clone=*record;*alias=*Other;Session.Insert(clone)}
func OverwriteThroughAlias(id string){record:=Build(id);clone:=new(model.Record);alias:=clone;*clone=*record;*alias=*Build("second");Session.Insert(clone)}
func EscapedBefore(id string){record:=Build(id);clone:=new(model.Record);p:=&clone;*clone=*record;_=p;Session.Insert(clone)}
func ConditionalCopy(flag bool,id string){record:=Build(id);clone:=new(model.Record);if flag{*clone=*record};Session.Insert(clone)}
func Repeated(id string){
 clone:=new(model.Record)
 for _,build:=range []func()*model.Record{
  func()*model.Record{return Build(id)},
  func()*model.Record{return Build("second")},
 }{record:=build();*clone=*record}
 Session.Insert(clone)
}
func GetterCopy(id string){clone:=new(model.Record);*clone=*Build(id);Session.Insert(clone.GetRequest())}
`

// u29xOrigin 入口 → 唯一 ORM insert 实参 origin。
func u29xOrigin(t *testing.T, dir, entry string) map[string]any {
	t.Helper()
	return u04xArgOrigin(t, dir, entry)
}

// TestU29xAliasSharedAllocation：复制前/后创建的别名共享同一分配身份。
func TestU29xAliasSharedAllocation(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04xFixture(t, u29xApp)
	for _, entry := range []string{"Entry", "AliasBefore", "AliasAfter"} {
		got := u29xOrigin(t, dir, entry)
		if got == nil || got["kind"] != "struct_copy" {
			t.Fatalf("%s origin = %+v，期望 struct_copy", entry, got)
		}
		src, _ := got["struct_copy"].(map[string]any)["source"].(map[string]any)
		if entry == "AliasBefore" || entry == "AliasAfter" {
			if src["kind"] != "struct_literal" {
				t.Fatalf("%s source = %+v，期望构造来源", entry, src)
			}
			fields, _ := src["struct_literal"].(map[string]any)["fields"].([]any)
			if len(fields) == 0 {
				t.Fatalf("%s source fields = %+v", entry, src)
			}
			o, _ := fields[0].(map[string]any)["origin"].(map[string]any)
			if o["kind"] != "parameter" {
				t.Fatalf("%s Request origin = %+v，期望入口参数", entry, o)
			}
		}
	}
}

// TestU29xOverwriteInvalidates：未知覆写/通过别名覆写/逃逸/可能未执行分支。
func TestU29xOverwriteInvalidates(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04xFixture(t, u29xApp)
	for _, entry := range []string{"UnknownOverwrite", "EscapedBefore"} {
		got := u29xOrigin(t, dir, entry)
		if got != nil && got["kind"] == "struct_copy" {
			t.Fatalf("%s 不应保留确定构造祖先: %+v", entry, got)
		}
	}
	// 条件复制可能未执行：不得保留无条件祖先。
	if got := u29xOrigin(t, dir, "ConditionalCopy"); got != nil && got["kind"] == "struct_copy" {
		t.Fatalf("条件复制不应升级为无条件祖先: %+v", got)
	}
	// 通过别名覆写 → 同一分配的新来源（second）。
	got := u29xOrigin(t, dir, "OverwriteThroughAlias")
	if got == nil || got["kind"] != "struct_copy" {
		t.Fatalf("别名覆写后 origin = %+v", got)
	}
	src, _ := got["struct_copy"].(map[string]any)["source"].(map[string]any)
	fields, _ := src["struct_literal"].(map[string]any)["fields"].([]any)
	if len(fields) == 0 {
		t.Fatalf("source = %+v", src)
	}
	o, _ := fields[0].(map[string]any)["origin"].(map[string]any)
	if o["kind"] != "literal" || o["literal"] != `"second"` {
		t.Fatalf("别名覆写后来源应为 second 字面量: %+v", o)
	}
}

// 安全 getter 保留整值复制的历史对象根与准确字段读取。
func TestU29xGetterHistoryRoot(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04xFixture(t, u29xApp)
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/copyflow:GetterCopy",
		"--repo", dir, "--code", "--depth", "12", "--continue-segments", "32",
		"--evidence", "--json")
	if code != 0 {
		t.Fatalf("query exit=%d\n%s", code, out)
	}
	var got map[string]any
	var doc struct {
		Nodes []map[string]any `json:"Nodes"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("json: %v", err)
	}
	var walk func(m map[string]any)
	walk = func(m map[string]any) {
		if got != nil {
			return
		}
		if ev, ok := m["evidence"].(map[string]any); ok {
			if orm, ok := ev["orm"].(map[string]any); ok && orm["operation"] == "insert" {
				if args, ok := orm["arguments"].([]any); ok && len(args) > 0 {
					arg := args[0].(map[string]any)
					got, _ = arg["origin"].(map[string]any)
				}
			}
		}
		for _, key := range []string{"Nodes", "Else"} {
			if arr, ok := m[key].([]any); ok {
				for _, c := range arr {
					walk(c.(map[string]any))
				}
			}
		}
	}
	for _, m := range doc.Nodes {
		walk(m)
	}
	if got == nil || got["kind"] != "field_read" {
		t.Fatalf("getter origin = %+v，期望 field_read", got)
	}
	fr, _ := got["field_read"].(map[string]any)
	root, _ := fr["root"].(map[string]any)
	if root["kind"] != "struct_copy" || root["id"] == nil {
		t.Fatalf("getter 应保留 struct_copy 历史根: %+v", root)
	}
	if path, _ := fr["path"].([]any); len(path) != 1 || path[0] != "Request" {
		t.Fatalf("getter 读取路径 = %+v", fr["path"])
	}
}

// TestU29xRepeatedFunctionList：逐项环境，最终指向第二次构造。
func TestU29xRepeatedFunctionList(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04xFixture(t, u29xApp)
	got := u29xOrigin(t, dir, "Repeated")
	if got == nil || got["kind"] != "struct_copy" {
		t.Fatalf("Repeated origin = %+v", got)
	}
	src, _ := got["struct_copy"].(map[string]any)["source"].(map[string]any)
	fields, _ := src["struct_literal"].(map[string]any)["fields"].([]any)
	if len(fields) == 0 {
		t.Fatalf("source = %+v", src)
	}
	o, _ := fields[0].(map[string]any)["origin"].(map[string]any)
	if o["kind"] != "literal" || o["literal"] != `"second"` {
		t.Fatalf("最终 struct_copy 祖先应为第二次构造: %+v", o)
	}
}

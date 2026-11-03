//go:build integration

package integration

// todo28 U04g：ORM 实参的准确模型声明与字段源码（真实源码 → init → CLI）。
//
// 覆盖：显式类型形参、值拷贝、别名、字面量、单结果构造器；any/接口返回、
// 本地遮蔽 new、局部同名类型、未知类型都不建立模型关联。

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

const u04gOrmMod = `module github.com/xormplus/xorm

go 1.26.2
`

const u04gOrmSrc = `package xorm
type Session struct{}
func(*Session)Insert(...any)(int64,error){return 0,nil}
`

const u04gAppMod = `module example.com/modelflow

go 1.26.2

require github.com/xormplus/xorm v0.0.0
replace github.com/xormplus/xorm => ../orm
`

const u04gModelSrc = "package model\ntype Record struct {\n Id int64 `xorm:\"pk autoincr BIGINT\"`\n Request string `xorm:\"'request_id' VARCHAR(64)\"`\n}\ntype Alias = Record\nfunc New()*Record{return &Record{}}\nfunc Dynamic()any{return &Record{}}\n"

const u04gAppSrc = `package modelflow
import("github.com/xormplus/xorm";"example.com/modelflow/model")
var Session *xorm.Session
type API interface{Run()}
func Parameter(record *model.Record){Session.Insert(record)}
type Record struct{Wrong bool}
func Clone(record *model.Record){clone:=new(model.Record);*clone=*record;Session.Insert(clone)}
func Alias(record *model.Alias){Session.Insert(record)}
func Literal(){Session.Insert(&model.Record{})}
func Constructor(){Session.Insert(model.New())}
func Dynamic(){Session.Insert(model.Dynamic())}
func Unknown(record any){Session.Insert(record)}
func Interface(record API){Session.Insert(record)}
func ShadowNew(){new:=func(any)any{return nil};Session.Insert(new(nil))}
func LocalShadow(){type Record struct{Other string};record:=&Record{};Session.Insert(record)}
`

func u04gFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "orm", "go.mod"), u04gOrmMod)
	writeFile(t, filepath.Join(root, "orm", "orm.go"), u04gOrmSrc)
	writeFile(t, filepath.Join(root, "app", "go.mod"), u04gAppMod)
	writeFile(t, filepath.Join(root, "app", "model", "model.go"), u04gModelSrc)
	writeFile(t, filepath.Join(root, "app", "app.go"), u04gAppSrc)
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

// u04gModels 查询入口 → orm.models 扁平列表。
func u04gModels(t *testing.T, dir, entry string) []map[string]any {
	t.Helper()
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/modelflow:"+entry,
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
					if models, ok := orm["models"].([]any); ok {
						for _, m := range models {
							found = append(found, m.(map[string]any))
						}
					}
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

// TestU04gParameterModel E-U04g 的 Parameter 投影（字段/标签/边界）。
func TestU04gParameterModel(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04gFixture(t)
	got := u04gModels(t, dir, "Parameter")
	if len(got) != 1 {
		t.Fatalf("models 数量 = %d: %+v", len(got), got)
	}
	m := got[0]
	if m["argument"] != float64(0) || m["symbol_id"] != "symbol:go:example.com/modelflow/model:Record" ||
		m["boundary"] != "model_mapping_unproven" {
		t.Fatalf("model: %+v", m)
	}
	fields, _ := m["fields"].([]any)
	if len(fields) != 2 {
		t.Fatalf("fields: %+v", fields)
	}
	f0 := fields[0].(map[string]any)
	if f0["name"] != "Id" || f0["go_type"] != "int64" || f0["tag"] != `xorm:"pk autoincr BIGINT"` {
		t.Fatalf("field[0]: %+v", f0)
	}
	f1 := fields[1].(map[string]any)
	if f1["name"] != "Request" || f1["go_type"] != "string" ||
		f1["tag"] != `xorm:"'request_id' VARCHAR(64)"` {
		t.Fatalf("field[1]: %+v", f1)
	}
	if _, ok := f0["column"]; ok {
		t.Fatalf("不得推定 column: %+v", f0)
	}
	src, _ := m["source"].(map[string]any)
	if src["package"] != "example.com/modelflow/model" || src["file"] != "model/model.go" ||
		src["location"] != "repository" || !strings.Contains(src["text"].(string), "type Record struct") {
		t.Fatalf("source: %+v", src)
	}
}

// TestU04gModelVariants Clone/Alias/Literal/Constructor 都指向 model.Record。
func TestU04gModelVariants(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04gFixture(t)
	for _, entry := range []string{"Clone", "Alias", "Literal", "Constructor"} {
		got := u04gModels(t, dir, entry)
		if len(got) != 1 {
			t.Fatalf("%s models 数量 = %d", entry, len(got))
		}
		if got[0]["symbol_id"] != "symbol:go:example.com/modelflow/model:Record" {
			t.Fatalf("%s symbol_id: %+v", entry, got[0])
		}
	}
}

// TestU04gModelRejections any/接口/遮蔽 new/局部同名类型/未知类型。
func TestU04gModelRejections(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04gFixture(t)
	for _, entry := range []string{"Dynamic", "Unknown", "Interface", "ShadowNew", "LocalShadow"} {
		if got := u04gModels(t, dir, entry); len(got) != 0 {
			t.Fatalf("%s 不应建立模型关联: %+v", entry, got)
		}
	}
}

//go:build integration

package integration

// todo29 U04y：整值复制后的字段写入、求值顺序与历史对象根（真实 init → CLI）。

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func u29yFixture(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/fieldwrites\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "model", "model.go"), `package model
type Record struct{ ID int64; State int }
`)
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

const u29yFlow = `package fieldwrites
import "example.com/fieldwrites/model"
func Clone(r *model.Record, id int64) {
    copy := new(model.Record)
    *copy = *r
    copy.ID = id
    r.ID = copy.ID
}
`

type u29yWrite struct {
	Field    string
	Operator string
	Kind     any
}

// u29yWrites 按树序收集 field_writes。
func u29yWrites(t *testing.T, raw string) []u29yWrite {
	t.Helper()
	var doc struct {
		Nodes []map[string]any `json:"Nodes"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("json: %v", err)
	}
	var out []u29yWrite
	var walk func(n map[string]any)
	walk = func(n map[string]any) {
		if arr, ok := n["field_writes"].([]any); ok {
			for _, it := range arr {
				w, _ := it.(map[string]any)
				tgt, _ := w["target"].(map[string]any)
				path, _ := tgt["path"].([]any)
				field := ""
				if len(path) > 0 {
					field, _ = path[len(path)-1].(string)
				}
				var kind any
				if v, ok := w["value"].(map[string]any); ok {
					kind = v["kind"]
				}
				out = append(out, u29yWrite{Field: field, Operator: str(w["operator"]), Kind: kind})
			}
		}
		for _, key := range []string{"Nodes", "Else"} {
			if arr, ok := n[key].([]any); ok {
				for _, c := range arr {
					if cm, ok := c.(map[string]any); ok {
						walk(cm)
					}
				}
			}
		}
	}
	for _, n := range doc.Nodes {
		walk(n)
	}
	return out
}

// 整值复制后的字段写入：copy.ID=id（parameter）与 r.ID=copy.ID（field_read）。
func TestU29yCopyFieldWrites(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u29yFixture(t, u29yFlow)
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/fieldwrites:Clone",
		"--repo", dir, "--code", "--depth", "1", "--continue-segments", "32", "--evidence", "--json")
	if code != 0 {
		t.Fatalf("query exit=%d\n%s", code, out)
	}
	got := u29yWrites(t, out)
	if len(got) != 2 {
		t.Fatalf("field_writes = %+v, want 2", got)
	}
	if got[0].Field != "ID" || got[0].Operator != "=" || got[0].Kind != "parameter" {
		t.Fatalf("第一条写入 = %+v，want ID = parameter", got[0])
	}
	if got[1].Field != "ID" || got[1].Operator != "=" || got[1].Kind != "field_read" {
		t.Fatalf("第二条写入 = %+v，want ID = field_read", got[1])
	}
}

// 求值顺序/compound：源码顺序保留，+= 只描述右操作数。
func TestU29yEvaluationOrder(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	src := `package fieldwrites
import "example.com/fieldwrites/model"
func Get() *model.Record { return new(model.Record) }
func Clone(r *model.Record, delta int) {
    copy := new(model.Record)
    *copy = *r
    copy.State += delta
    copy.ID = int64(delta)
    Get().ID = int64(delta)
}
`
	dir := u29yFixture(t, src)
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/fieldwrites:Clone",
		"--repo", dir, "--code", "--depth", "1", "--continue-segments", "32", "--evidence", "--json")
	if code != 0 {
		t.Fatalf("query exit=%d\n%s", code, out)
	}
	got := u29yWrites(t, out)
	if len(got) != 2 {
		t.Fatalf("field_writes = %+v, want 2", got)
	}
	if got[0].Field != "State" || got[0].Operator != "+=" || got[0].Kind != "parameter" {
		t.Fatalf("compound 写入应只记录右操作数来源: %+v", got[0])
	}
	if got[1].Field != "ID" || got[1].Operator != "=" || got[1].Kind != "parameter" {
		t.Fatalf("普通写入 = %+v", got[1])
	}
}

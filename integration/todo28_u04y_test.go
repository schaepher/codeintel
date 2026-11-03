//go:build integration

package integration

// todo28 U04y：字段赋值观察与准确求值顺序（field_writes）
//（真实源码 → init → CLI）。

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func u04yFixture(t *testing.T, appSrc string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/fieldwrites\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "model", "model.go"), `package model
type Record struct{ID int64;State int;Child *Record;Active bool}
func(r *Record)GetID()int64{return r.ID}
func(r *Record)SetActive(){r.Active=true}
`)
	writeFile(t, filepath.Join(dir, "flow.go"), `package fieldwrites
import "example.com/fieldwrites/model"
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

// u04yWrites 入口的字段写入投影。
func u04yWrites(t *testing.T, dir, entry, depth, segments string) ([]map[string]any, map[string]any) {
	t.Helper()
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/fieldwrites:"+entry,
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
	var writes []map[string]any
	var budget map[string]any
	var walk func(n map[string]any)
	walk = func(n map[string]any) {
		if fw, ok := n["field_writes"].([]any); ok {
			for _, w := range fw {
				writes = append(writes, w.(map[string]any))
			}
		}
		if stop, ok := n["stop"].(map[string]any); ok && stop["code"] == "field_write_budget" {
			budget = stop
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
	return writes, budget
}

// TestU04yEntryProjection E-U04y 投影（字段/operator/semantics/value）。
func TestU04yEntryProjection(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04yFixture(t, `func Entry(r *model.Record,id int64){r.ID=id;r.State=7}
`)
	for _, args := range [][2]string{{"12", "32"}, {"1", "32"}} {
		writes, _ := u04yWrites(t, dir, "Entry", args[0], args[1])
		if len(writes) != 2 {
			t.Fatalf("writes(depth=%s) = %+v", args[0], writes)
		}
		w0 := writes[0]
		if w0["operator"] != "=" || w0["semantics"] != "assignment_only" {
			t.Fatalf("write[0] = %+v", w0)
		}
		tgt := w0["target"].(map[string]any)
		if tgt["type_id"] != "symbol:go:example.com/fieldwrites/model:Record" {
			t.Fatalf("target type = %+v", tgt)
		}
		path := tgt["path"].([]any)
		if len(path) != 1 || path[0] != "ID" {
			t.Fatalf("target path = %+v", path)
		}
		if tgt["root"].(map[string]any)["parameter"] != float64(0) {
			t.Fatalf("target root = %+v", tgt["root"])
		}
		v := w0["value"].(map[string]any)
		if v["kind"] != "parameter" || v["parameter"] != float64(1) {
			t.Fatalf("value[0] = %+v", v)
		}
		w1 := writes[1]
		if w1["target"].(map[string]any)["path"].([]any)[0] != "State" ||
			w1["value"].(map[string]any)["kind"] != "literal" ||
			w1["value"].(map[string]any)["literal"] != "7" {
			t.Fatalf("write[1] = %+v", w1)
		}
		if w0["id"] == w1["id"] {
			t.Fatalf("两次赋值必须不同观察 ID")
		}
	}
}

// TestU04yVariants 求值顺序/compound/嵌套/未知对象/调度/未执行闭包。
func TestU04yVariants(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04yFixture(t, `var Other *model.Record
func Build(id int64)*model.Record{return &model.Record{ID:id}}
func Nested(r *model.Record,id int64){r.Child.ID=id}
func Drop(r **model.Record){*r=Other}
func Escaped(r *model.Record,id int64){Drop(&r);r.ID=id}
func Compound(r *model.Record,id int64){r.ID+=id;r.ID++}
func Pair(r *model.Record,id int64){r.ID,r.State=id,7}
func Conditional(r *model.Record,b bool){if b{r.State=1}else{r.State=2}}
func Scheduled(r *model.Record){work:=func(){r.State=7};go work()}
func DeadCallback(r *model.Record){unused:=func(){r.State=7};_=unused}
func Effectful(){Get().ID=3}
func Get()*model.Record{return &model.Record{}}
`)
	// Nested：path Child/ID（root 保持入口参数）。
	writes, _ := u04yWrites(t, dir, "Nested", "12", "32")
	if len(writes) != 1 {
		t.Fatalf("Nested writes = %+v", writes)
	}
	path := writes[0]["target"].(map[string]any)["path"].([]any)
	if len(path) != 2 || path[0] != "Child" || path[1] != "ID" {
		t.Fatalf("Nested path = %+v", path)
	}
	// Escaped：实例来源未知 → root 省略但保留字段声明。
	writes, _ = u04yWrites(t, dir, "Escaped", "12", "32")
	if len(writes) != 1 {
		t.Fatalf("Escaped writes = %+v", writes)
	}
	if _, ok := writes[0]["target"].(map[string]any)["root"]; ok {
		t.Fatalf("逃逸后不应有 root: %+v", writes[0]["target"])
	}
	// Compound：+= 与 ++ 的 operator。
	writes, _ = u04yWrites(t, dir, "Compound", "12", "32")
	ops := []string{}
	for _, w := range writes {
		ops = append(ops, w["operator"].(string))
	}
	if len(ops) != 2 || ops[0] != "+=" || ops[1] != "++" {
		t.Fatalf("Compound operators = %v", ops)
	}
	if writes[1]["value"] != nil {
		t.Fatalf("++ 不应有 RHS 值: %+v", writes[1])
	}
	// Pair：按 LHS 顺序两项目标与各自 value。
	writes, _ = u04yWrites(t, dir, "Pair", "12", "32")
	if len(writes) != 2 {
		t.Fatalf("Pair writes = %+v", writes)
	}
	// Conditional：两条互斥分支各自 guard。
	writes, _ = u04yWrites(t, dir, "Conditional", "12", "32")
	if len(writes) != 2 || writes[0]["guard"] == "" || writes[1]["guard"] == "" {
		t.Fatalf("Conditional writes = %+v", writes)
	}
	// Scheduled：scheduled=true。
	writes, _ = u04yWrites(t, dir, "Scheduled", "12", "32")
	if len(writes) != 1 || writes[0]["scheduled"] != true {
		t.Fatalf("Scheduled writes = %+v", writes)
	}
	// DeadCallback：未执行闭包体不算赋值。
	if writes, _ = u04yWrites(t, dir, "DeadCallback", "12", "32"); len(writes) != 0 {
		t.Fatalf("DeadCallback writes = %+v", writes)
	}
	// Effectful：构造实例字段赋值 root 为 struct_literal。
	writes, _ = u04yWrites(t, dir, "Effectful", "12", "32")
	if len(writes) != 1 {
		t.Fatalf("Effectful writes = %+v", writes)
	}
	root, _ := writes[0]["target"].(map[string]any)["root"].(map[string]any)
	if root == nil || root["kind"] != "struct_literal" {
		t.Fatalf("Effectful root = %+v", root)
	}
}

// TestU04yBudget 257 条赋值 → 256 项 + field_write_budget 诊断。
func TestU04yBudget(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	var b strings.Builder
	b.WriteString("func Budget(r *model.Record){\n")
	for i := 0; i < 257; i++ {
		b.WriteString(" r.State=7\n")
	}
	b.WriteString("}\n")
	dir := u04yFixture(t, b.String())
	writes, budget := u04yWrites(t, dir, "Budget", "12", "32")
	if len(writes) != 256 {
		t.Fatalf("writes = %d", len(writes))
	}
	if budget == nil || !strings.Contains(budget["detail"].(string), "field assignment observation budget exhausted (256)") {
		t.Fatalf("budget = %+v", budget)
	}
}

// TestU04yAssignRender 文本/Mermaid 保留字段赋值（不是远程调用）。
func TestU04yAssignRender(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04yFixture(t, `func Entry(r *model.Record,id int64){r.ID=id;r.State=7}
`)
	for _, format := range []string{"", "mermaid"} {
		args := []string{"query", "sequence", "symbol:go:example.com/fieldwrites:Entry",
			"--repo", dir, "--code", "--depth", "12"}
		if format != "" {
			args = append(args, "--format", format)
		}
		code, out := runCLIOut(t, args...)
		if code != 0 {
			t.Fatalf("query(%s) exit=%d\n%s", format, code, out)
		}
		if format == "mermaid" {
			if !strings.Contains(out, "r.ID = id") || !strings.Contains(out, "r.State = 7") {
				t.Fatalf("Mermaid 缺少字段赋值注记:\n%s", out)
			}
			if strings.Contains(out, "->>") && strings.Contains(out, "r.ID") &&
				strings.Contains(out, "r.ID->>") {
				t.Fatalf("字段赋值不得画成远程调用:\n%s", out)
			}
		} else if !strings.Contains(out, "r.ID = id") {
			t.Fatalf("文本缺少字段赋值:\n%s", out)
		}
	}
}

// TestU04yOrderAndSlots 求值顺序（RHS 先于下一 RHS）与 return 槽。
func TestU04yOrderAndSlots(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04yFixture(t, `func Parts()(int64,int){return 1,2}
func Tuple(r *model.Record){r.ID,r.State=Parts()}
func Order(r *model.Record,id int64){r.ID,r.State=id,func()int{id=9;return 7}()}
func Rebind(r *model.Record,other *model.Record){r.ID=func()int64{r=other;return 3}()}
`)
	// Tuple：两个槽分别保留 1/2（result_slot 0/1）。
	writes, _ := u04yWrites(t, dir, "Tuple", "12", "32")
	if len(writes) != 2 {
		t.Fatalf("Tuple writes = %+v", writes)
	}
	if writes[0]["result_slot"] != float64(0) || writes[1]["result_slot"] != float64(1) {
		t.Fatalf("Tuple slots = %+v", writes)
	}
	// Order：首项来源是参数 1（不能被匿名函数里的 id=9 污染）。
	writes, _ = u04yWrites(t, dir, "Order", "12", "32")
	if len(writes) != 2 {
		t.Fatalf("Order writes = %+v", writes)
	}
	v0 := writes[0]["value"].(map[string]any)
	if v0["kind"] != "parameter" || v0["parameter"] != float64(1) {
		t.Fatalf("Order value[0] = %+v", v0)
	}
	v1 := writes[1]["value"].(map[string]any)
	if v1["kind"] != "literal" || v1["literal"] != "7" {
		t.Fatalf("Order value[1] = %+v", v1)
	}
	// Rebind：目标仍是入口参数 0（RHS 前固定）。
	writes, _ = u04yWrites(t, dir, "Rebind", "12", "32")
	if len(writes) != 1 {
		t.Fatalf("Rebind writes = %+v", writes)
	}
	root := writes[0]["target"].(map[string]any)["root"].(map[string]any)
	if root["parameter"] != float64(0) {
		t.Fatalf("Rebind root = %+v", root)
	}
}

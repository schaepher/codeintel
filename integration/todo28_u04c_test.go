//go:build integration

package integration

// todo28 U04c：同一时序值环境中的标量/派生记录键来源（真实源码 → init → CLI）。
//
// 覆盖：入口 parameter / 字符串拼接 concat / literal、别名沿 AST 声明对象、
// 取址与闭包写者持续失效、分支来源不同 → 未知（相同保留）、遮蔽隔离、
// 两次实际调用的来源不串。

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

const u04cSrc = `package originflow
import "database/sql"
var DB *sql.DB
func Create(id string){DB.Exec("INSERT INTO nodes(id) VALUES(?)",id)}
func Entry(id string){alias:=id;Create(alias);Create(alias+"-service")}
func Log(...any){}
func Escape(input string){var id string;p:=&id;id=input;Log(p);Create(id)}
func Capture(input string){var id string;f:=func(){id="changed"};id=input;Log(f);Create(id)}
func Branch(id string,flag bool){alias:=id;if flag{alias="other"};Create(alias)}
func Same(id string,flag bool){alias:=id;if flag{alias=id}else{alias=id};Create(alias)}
func Shadow(id string){{id:="local";Create(id)};Create(id)}
func Distinct(){Entry("first");Entry("second")}
func Compound(id string){alias:=id;alias+="suffix";Create(alias)}
func Loop(id string){alias:=id;for i:=0;i<2;i++{alias="loop"};Create(alias)}
`

func u04cFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/originflow\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), u04cSrc)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

// u04cOrigins 查询入口的每个 INSERT 写入值的 origin（按出现顺序）。
func u04cOrigins(t *testing.T, dir, entry, depth, segments string) ([]any, map[string]any) {
	t.Helper()
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/originflow:"+entry,
		"--repo", dir, "--code", "--depth", depth, "--continue-segments", segments,
		"--evidence", "--json")
	if code != 0 {
		t.Fatalf("query %s exit=%d\n%s", entry, code, out)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	var origins []any
	var walk func(v any)
	walk = func(v any) {
		switch n := v.(type) {
		case map[string]any:
			if ev, ok := n["evidence"].(map[string]any); ok {
				if st, ok := ev["storage"].(map[string]any); ok {
					if writes, ok := st["writes"].([]any); ok && len(writes) > 0 {
						w := writes[0].(map[string]any)
						if val, ok := w["value"].(map[string]any); ok {
							origins = append(origins, val["origin"])
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
	return origins, root
}

func u04cOriginMap(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("origin 不是对象: %#v", v)
	}
	return m
}

// TestU04cEntryOrigin E-U04c 的 Entry 投影：parameter + concat。
func TestU04cEntryOrigin(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04cFixture(t)
	origins, _ := u04cOrigins(t, dir, "Entry", "12", "32")
	if len(origins) != 2 {
		t.Fatalf("origin 数量 = %d: %+v", len(origins), origins)
	}
	first := u04cOriginMap(t, origins[0])
	if first["kind"] != "parameter" || first["scalar_type"] != "string" ||
		first["symbol"] != "symbol:go:example.com/originflow:Entry" ||
		first["parameter"] != float64(0) || first["repo"] != dir {
		t.Fatalf("parameter origin: %+v", first)
	}
	second := u04cOriginMap(t, origins[1])
	if second["kind"] != "concat" || second["scalar_type"] != "string" {
		t.Fatalf("concat origin: %+v", second)
	}
	parts, _ := second["parts"].([]any)
	if len(parts) != 2 {
		t.Fatalf("concat parts: %+v", second)
	}
	p0 := u04cOriginMap(t, parts[0])
	if p0["kind"] != "parameter" || p0["parameter"] != float64(0) ||
		p0["symbol"] != "symbol:go:example.com/originflow:Entry" {
		t.Fatalf("concat part[0]: %+v", p0)
	}
	p1 := u04cOriginMap(t, parts[1])
	if p1["kind"] != "literal" || p1["literal"] != `"-service"` {
		t.Fatalf("concat part[1]: %+v", p1)
	}
	// depth1 + 续查必须给出同一来源语义。
	origins1, _ := u04cOrigins(t, dir, "Entry", "1", "32")
	if len(origins1) != 2 {
		t.Fatalf("depth1 origin 数量 = %d", len(origins1))
	}
	if got := u04cOriginMap(t, origins1[0]); got["kind"] != "parameter" {
		t.Fatalf("depth1 parameter origin: %+v", got)
	}
	if got := u04cOriginMap(t, origins1[1]); got["kind"] != "concat" {
		t.Fatalf("depth1 concat origin: %+v", got)
	}
}

// TestU04cEscapeAndCapture 取址/闭包写者 → 来源省略。
func TestU04cEscapeAndCapture(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04cFixture(t)
	for _, entry := range []string{"Escape", "Capture"} {
		origins, _ := u04cOrigins(t, dir, entry, "12", "32")
		if len(origins) != 1 {
			t.Fatalf("%s origin 数量 = %d", entry, len(origins))
		}
		if origins[0] != nil {
			t.Fatalf("%s origin 必须省略（持续逃逸）: %+v", entry, origins[0])
		}
	}
}

// TestU04cBranchMerge 分支来源不同 → 未知；相同 → 保留。
func TestU04cBranchMerge(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04cFixture(t)
	origins, _ := u04cOrigins(t, dir, "Branch", "12", "32")
	if len(origins) != 1 || origins[0] != nil {
		t.Fatalf("Branch origin 应省略: %+v", origins)
	}
	origins, _ = u04cOrigins(t, dir, "Same", "12", "32")
	if len(origins) != 1 {
		t.Fatalf("Same origin 数量 = %d", len(origins))
	}
	got := u04cOriginMap(t, origins[0])
	if got["kind"] != "parameter" || got["parameter"] != float64(0) ||
		got["symbol"] != "symbol:go:example.com/originflow:Same" {
		t.Fatalf("Same origin: %+v", got)
	}
}

// TestU04cShadowAndDistinct 遮蔽按对象隔离；两次调用来源不串。
func TestU04cShadowAndDistinct(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04cFixture(t)
	origins, _ := u04cOrigins(t, dir, "Shadow", "12", "32")
	if len(origins) != 2 {
		t.Fatalf("Shadow origin 数量 = %d", len(origins))
	}
	inner := u04cOriginMap(t, origins[0])
	if inner["kind"] != "literal" || inner["literal"] != `"local"` {
		t.Fatalf("内层遮蔽应为 literal: %+v", inner)
	}
	outer := u04cOriginMap(t, origins[1])
	if outer["kind"] != "parameter" || outer["symbol"] != "symbol:go:example.com/originflow:Shadow" {
		t.Fatalf("外层参数来源: %+v", outer)
	}
	// Distinct：两次 Entry("first"/"second") → 写入来源是各自调用的字面量。
	origins, _ = u04cOrigins(t, dir, "Distinct", "12", "32")
	if len(origins) != 4 {
		t.Fatalf("Distinct origin 数量 = %d: %+v", len(origins), origins)
	}
	// Entry 的两次写：id 与 id+"-service"（concat 首段是各自调用的字面量）。
	wantFirst := []string{`"first"`, `"second"`}
	for i, w := range wantFirst {
		o := u04cOriginMap(t, origins[i*2])
		if o["kind"] != "literal" || o["literal"] != w {
			t.Fatalf("Distinct origin[%d] = %+v（want %s）", i*2, o, w)
		}
		c := u04cOriginMap(t, origins[i*2+1])
		if c["kind"] != "concat" {
			t.Fatalf("Distinct origin[%d] 应为 concat: %+v", i*2+1, c)
		}
		parts, _ := c["parts"].([]any)
		if len(parts) != 2 {
			t.Fatalf("Distinct concat parts: %+v", c)
		}
		if p0 := u04cOriginMap(t, parts[0]); p0["literal"] != w {
			t.Fatalf("Distinct concat part[0] = %+v（want %s）", p0, w)
		}
	}
}

// TestU04cCompoundAndLoop compound/loop 写者不能保留赋值前来源。
func TestU04cCompoundAndLoop(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04cFixture(t)
	for _, entry := range []string{"Compound", "Loop"} {
		origins, _ := u04cOrigins(t, dir, entry, "12", "32")
		if len(origins) != 1 {
			t.Fatalf("%s origin 数量 = %d", entry, len(origins))
		}
		if origins[0] != nil {
			t.Fatalf("%s origin 必须省略: %+v", entry, origins[0])
		}
	}
}

//go:build integration

package integration

// todo29：T12b 统一追加预算 / T14b 取址逃逸 / T19b·T19c 运行时缺口与无
// Context SQL 读取（真实 init → CLI；JSON 契约）。

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func u29InitRepo(t *testing.T, module string, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module "+module+"\n\ngo 1.26.2\n")
	for rel, body := range files {
		writeFile(t, filepath.Join(dir, filepath.FromSlash(rel)), body)
	}
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

func u29QueryJSON(t *testing.T, args ...string) map[string]any {
	t.Helper()
	code, out := runCLIOut(t, args...)
	if code != 0 {
		t.Fatalf("query exit=%d\n%s", code, out)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	return doc
}

// T12b：预算 1 只追加第一个 Step；两个兄弟与 Leaf 保留 depth 停止。
func TestU29T12bBudgetCLI(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u29InitRepo(t, "example.com/budget", map[string]string{
		"flow.go": "package budget\nfunc Leaf() {}\nfunc Step() { Leaf() }\nfunc Entry() { Step(); Step(); Step() }\n",
	})
	for _, tc := range []struct {
		budget     string
		expanded   float64
		extra      float64
		used       float64
		budgetStop float64
	}{
		{"1", 1, 1, 1, 3},
		{"2", 2, 2, 2, 3},
	} {
		doc := u29QueryJSON(t, "query", "sequence", "symbol:go:example.com/budget:Entry",
			"--repo", dir, "--code", "--depth", "1", "--continue-segments", tc.budget,
			"--evidence", "--json")
		cont, _ := doc["meta"].(map[string]any)["continuation"].(map[string]any)
		if cont == nil {
			t.Fatalf("budget=%s 缺少 continuation", tc.budget)
		}
		checks := map[string]float64{"expanded": tc.expanded, "extra_expansions": tc.extra,
			"segments_used": tc.used, "budget_stops": tc.budgetStop}
		for k, want := range checks {
			if got, _ := cont[k].(float64); got != want {
				t.Fatalf("budget=%s %s=%v, want %v", tc.budget, k, cont[k], want)
			}
		}
	}
}

// T14b：取址别名持续失效；取址之前的判断不被倒推污染。
func TestU29T14bEscapeCLI(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u29InitRepo(t, "example.com/pointerflow", map[string]string{
		"flow.go": `package pointerflow
func Manual(){}
func Auto(){}
func mutate(p *int){*p=0}
func Run(){n:=1;p:=&n;mutate(p);if n==1{Manual()}else{Auto()}}
func RunReassign(){n:=1;p:=&n;mutate(p);n=1;if n==1{Manual()}else{Auto()}}
func IfBefore(){n:=1;if n==1{Manual()}else{Auto()};p:=&n;mutate(p)}
`,
		"runtime.json": `{"version":2,"sources":[],"bindings":[],"observations":[]}`,
	})
	count := func(entry, label string) int {
		doc := u29QueryJSON(t, "query", "sequence", "symbol:go:example.com/pointerflow:"+entry,
			"--repo", dir, "--code", "--depth", "12",
			"--runtime-data", filepath.Join(dir, "runtime.json"), "--json")
		n := 0
		var walk func(m map[string]any)
		walk = func(m map[string]any) {
			if m["Kind"] == "call" && m["Label"] == label {
				n++
			}
			for _, key := range []string{"Nodes", "Else"} {
				if arr, ok := m[key].([]any); ok {
					for _, c := range arr {
						if cm, ok := c.(map[string]any); ok {
							walk(cm)
						}
					}
				}
			}
		}
		for _, c := range doc["Nodes"].([]any) {
			walk(c.(map[string]any))
		}
		return n
	}
	for _, entry := range []string{"Run", "RunReassign"} {
		if count(entry, "Manual") != 1 || count(entry, "Auto") != 1 {
			t.Fatalf("%s 取址后应保留两分支", entry)
		}
	}
	if count("IfBefore", "Manual") != 1 || count("IfBefore", "Auto") != 0 {
		t.Fatalf("IfBefore 不应被后面的取址倒推污染")
	}
}

// T19b/T19c：三条观测都不满足时报告最接近有效匹配；无 Context 的
// QueryRow 与 QueryRowContext 得到同一缺口分类。
func TestU29T19GapCLI(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	runtime := `{"version":2,"sources":[{"id":"flags","kind":"database"}],"bindings":[{"target":{"symbol":"symbol:go:example.com/gap:DB"},"source":"flags"}],"observations":[{"source":"flags","provenance":{"observed_at":"2026-10-28T00:00:00Z","revision":"fixture"},"resource":{"table":"config"},"scope":[{"field":"tenant","op":"eq","value":"a"}],"coverage":{"complete":false,"columns":["tenant","enabled"]},"rows":[]},{"source":"flags","provenance":{"observed_at":"2026-10-28T00:00:00Z","revision":"fixture"},"resource":{"table":"config"},"scope":[],"coverage":{"complete":true,"columns":["tenant"]},"rows":[{"tenant":"a"}]},{"source":"flags","provenance":{"observed_at":"2026-10-28T00:00:00Z","revision":"fixture"},"resource":{"table":"config"},"scope":[{"field":"tenant","op":"eq","value":"b"}],"coverage":{"complete":true,"columns":["tenant","enabled"]},"rows":[{"tenant":"b","enabled":1}]}]}`
	dir := u29InitRepo(t, "example.com/gap", map[string]string{
		"flow.go": `package gap
import ("context"; "database/sql")
var DB *sql.DB
func Entry() {
    var enabled int
    _ = DB.QueryRow("SELECT enabled FROM config WHERE tenant = ?", "a").Scan(&enabled)
}
func ContextEntry(ctx context.Context) {
    var enabled int
    _ = DB.QueryRowContext(ctx,"SELECT enabled FROM config WHERE tenant = 'a'").Scan(&enabled)
}
`,
		"runtime.json": runtime,
	})
	for _, entry := range []string{"Entry", "ContextEntry"} {
		doc := u29QueryJSON(t, "query", "sequence", "symbol:go:example.com/gap:"+entry,
			"--repo", dir, "--code", "--depth", "12",
			"--runtime-data", filepath.Join(dir, "runtime.json"), "--evidence", "--json")
		var codes []string
		var walk func(m map[string]any)
		walk = func(m map[string]any) {
			if ev, ok := m["evidence"].(map[string]any); ok {
				if rt, ok := ev["runtime"].(map[string]any); ok {
					if b, ok := rt["boundary"].(map[string]any); ok {
						if code, ok := b["code"].(string); ok && !containsStr(codes, code) {
							codes = append(codes, code)
						}
					}
				}
			}
			for _, key := range []string{"Nodes", "Else"} {
				if arr, ok := m[key].([]any); ok {
					for _, c := range arr {
						if cm, ok := c.(map[string]any); ok {
							walk(cm)
						}
					}
				}
			}
		}
		for _, c := range doc["Nodes"].([]any) {
			walk(c.(map[string]any))
		}
		if len(codes) != 1 || codes[0] != "scope_not_covered" {
			t.Fatalf("%s 缺口 = %v, want scope_not_covered", entry, codes)
		}
	}
}

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

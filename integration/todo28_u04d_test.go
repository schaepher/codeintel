//go:build integration

package integration

// todo28 U04d：写入资源绑定与读写共同失效（真实源码 → init → CLI + 快照）。
//
// 覆盖：bound / binding_mutated / ambiguous_resource / binding_missing；
// 未知调用效果、Scan 目标、for/switch init、range、闭包写者、取址、
// 纯函数（strings/reflect）正例；无快照时省略 resource。

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

const u04dFlowSrc = `package resourceflow
import "database/sql"
var DB *sql.DB
func Save(){DB.Exec("INSERT INTO nodes(id) VALUES(?)","n")}
func Rebound(){DB=nil;Save()}
func Escaped(){p:=&DB;_ = p;Save()}
func Unknown(f func()){f();Save()}
func Reset(){DB=nil}
func Twice(){Save();Reset();Save()}
func Later(){Save();DB=nil}
func StaticSkip(){if false{DB=nil};Save()}
func Shadow(){var DB *sql.DB;_ = DB;Save()}
type Box struct{DB *sql.DB}
func ShadowField(){var b Box;b.DB=nil;Save()}
`

const u04dEffectsSrc = `package resourceflow
import("database/sql";"reflect";"strings")
type Fake struct{}
func(*Fake) QueryRow(...any){DB=nil}
func(*Fake) Scan(...any){DB=nil}
func UnknownRead(f *Fake){f.QueryRow();Save()}
func UnknownScan(f *Fake){f.Scan();Save()}
func Acquire()*Fake{DB=nil;return &Fake{}}
func FactoryScan(){var value string;Acquire().Scan(&value);Save()}
func GlobalScan(){DB.QueryRow("SELECT kind FROM nodes WHERE id = 'n'").Scan(&DB);Save()}
func ResetArg()string{DB=nil;return "n"}
func Dynamic(query string){DB.QueryRow(query,ResetArg());Save()}
func len(string)int{DB=nil;return 0}
func ShadowBuiltin(){len("x");Save()}
func Range(handles []*sql.DB){for _,DB=range handles{};Save()}
func RangeEmpty(){for _,DB=range []*sql.DB{}{};Save()}
func ForInit(){for DB=nil;false;{};Save()}
func SwitchInit(){switch DB=nil;{default:};Save()}
func PureStrings(){strings.Repeat("x",1);Save()}
func PureReflect(){reflect.TypeOf("x");Save()}
func ReflectArg(){reflect.TypeOf(ResetArg());Save()}
func Size()int{DB=nil;return 1}
func MakeArg(){_=make([]int,Size());Save()}
type FakeStrings struct{}
func(*FakeStrings) Repeat(s string,n int)string{DB=nil;return s}
func ShadowStrings(){strings:=&FakeStrings{};strings.Repeat("x",1);Save()}
`

const u04dCrossSrc = `package resourceflow
func CrossBuiltin(){len("x");Save()}
`

const u04dRuntime = `{"version":2,"sources":[{"id":"primary","kind":"database"}],
"bindings":[{"target":{"symbol":"symbol:go:example.com/resourceflow:DB"},"source":"primary"}],
"observations":[]}`

func u04dFixture(t *testing.T, runtimeJSON string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/resourceflow\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), u04dFlowSrc)
	writeFile(t, filepath.Join(dir, "effects.go"), u04dEffectsSrc)
	writeFile(t, filepath.Join(dir, "cross.go"), u04dCrossSrc)
	writeFile(t, filepath.Join(dir, "runtime.json"), runtimeJSON)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

// u04dResources 查询入口 → 每个 storage 的 {resource,outcome}（快照 hash 去掉）。
func u04dResources(t *testing.T, dir, entry, runtimePath string) []map[string]any {
	t.Helper()
	args := []string{"query", "sequence", "symbol:go:example.com/resourceflow:" + entry,
		"--repo", dir, "--code", "--depth", "12", "--json"}
	if runtimePath != "" {
		args = append(args, "--runtime-data", runtimePath)
	}
	code, out := runCLIOut(t, args...)
	if code != 0 {
		t.Fatalf("query %s exit=%d\n%s", entry, code, out)
	}
	var root any
	if err := json.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	var res []map[string]any
	var walk func(v any)
	walk = func(v any) {
		switch n := v.(type) {
		case map[string]any:
			if ev, ok := n["evidence"].(map[string]any); ok {
				if st, ok := ev["storage"].(map[string]any); ok {
					item := map[string]any{"outcome": st["outcome"]}
					if r, ok := st["resource"].(map[string]any); ok {
						cp := map[string]any{}
						for k, val := range r {
							if k == "snapshot_hash" {
								if h, _ := val.(string); len(h) == 64 {
									item["hash_ok"] = true
								}
								continue
							}
							cp[k] = val
						}
						item["resource"] = cp
					} else {
						item["resource"] = nil
					}
					res = append(res, item)
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
	return res
}

func u04dResourceMap(t *testing.T, item map[string]any) map[string]any {
	t.Helper()
	m, ok := item["resource"].(map[string]any)
	if !ok {
		t.Fatalf("resource 缺失: %#v", item)
	}
	return m
}

// TestU04dBoundAndMutated E-U04d 的 Save/Rebound/Escaped/Unknown 投影。
func TestU04dBoundAndMutated(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04dFixture(t, u04dRuntime)
	rt := filepath.Join(dir, "runtime.json")

	save := u04dResources(t, dir, "Save", rt)
	if len(save) != 1 {
		t.Fatalf("Save storage 数量 = %d", len(save))
	}
	res := u04dResourceMap(t, save[0])
	if res["status"] != "bound" || res["source_id"] != "primary" ||
		res["symbol"] != "symbol:go:example.com/resourceflow:DB" {
		t.Fatalf("Save resource: %+v", res)
	}
	if save[0]["hash_ok"] != true {
		t.Fatalf("bound 必须带实际快照 64 位 hash: %+v", save[0])
	}
	if save[0]["outcome"] != "unproven" {
		t.Fatalf("outcome: %+v", save[0])
	}

	for _, entry := range []string{"Rebound", "Escaped", "Unknown"} {
		got := u04dResources(t, dir, entry, rt)
		if len(got) != 1 {
			t.Fatalf("%s storage 数量 = %d", entry, len(got))
		}
		res := u04dResourceMap(t, got[0])
		if res["status"] != "binding_mutated" || res["symbol"] != "symbol:go:example.com/resourceflow:DB" {
			t.Fatalf("%s resource: %+v", entry, res)
		}
		if _, ok := res["source_id"]; ok {
			t.Fatalf("%s 失效后不得保留 source_id: %+v", entry, res)
		}
	}

	twice := u04dResources(t, dir, "Twice", rt)
	if len(twice) != 2 {
		t.Fatalf("Twice storage 数量 = %d", len(twice))
	}
	for i, item := range twice {
		res := u04dResourceMap(t, item)
		if res["status"] != "ambiguous_resource" {
			t.Fatalf("Twice[%d] resource: %+v", i, res)
		}
		if _, ok := res["source_id"]; ok {
			t.Fatalf("Twice 冲突不得保留 source_id: %+v", res)
		}
	}
}

// TestU04dBindingMissing 绑定目标不是 DB 符号 → binding_missing。
func TestU04dBindingMissing(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	const missing = `{"version":2,"sources":[{"id":"primary","kind":"database"}],
"bindings":[{"target":{"symbol":"symbol:go:example.com/resourceflow:Other"},"source":"primary"}],
"observations":[]}`
	dir := u04dFixture(t, missing)
	got := u04dResources(t, dir, "Save", filepath.Join(dir, "runtime.json"))
	if len(got) != 1 {
		t.Fatalf("storage 数量 = %d", len(got))
	}
	res := u04dResourceMap(t, got[0])
	if res["status"] != "binding_missing" || res["symbol"] != "symbol:go:example.com/resourceflow:DB" {
		t.Fatalf("resource: %+v", res)
	}
	if _, ok := res["source_id"]; ok {
		t.Fatalf("binding_missing 不得有 source_id: %+v", res)
	}
}

// TestU04dShadowAndStaticSkip 局部遮蔽/字段遮蔽不误伤；Save 前不变、后重绑定不回退。
func TestU04dShadowAndStaticSkip(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04dFixture(t, u04dRuntime)
	rt := filepath.Join(dir, "runtime.json")
	for _, entry := range []string{"Shadow", "ShadowField", "Later", "StaticSkip"} {
		got := u04dResources(t, dir, entry, rt)
		if len(got) != 1 {
			t.Fatalf("%s storage 数量 = %d", entry, len(got))
		}
		res := u04dResourceMap(t, got[0])
		if res["status"] != "bound" {
			t.Fatalf("%s 应保持 bound: %+v", entry, res)
		}
	}
}

// TestU04dEffectsMatrix effects.go 的未知效果/纯函数矩阵。
func TestU04dEffectsMatrix(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04dFixture(t, u04dRuntime)
	rt := filepath.Join(dir, "runtime.json")
	mutated := []string{"UnknownRead", "UnknownScan", "FactoryScan", "GlobalScan",
		"Dynamic", "ShadowBuiltin", "Range", "ForInit", "SwitchInit",
		"ReflectArg", "MakeArg", "ShadowStrings", "CrossBuiltin"}
	for _, entry := range mutated {
		got := u04dResources(t, dir, entry, rt)
		if len(got) != 1 {
			t.Fatalf("%s storage 数量 = %d", entry, len(got))
		}
		res := u04dResourceMap(t, got[0])
		if res["status"] != "binding_mutated" {
			t.Fatalf("%s resource = %+v（want binding_mutated）", entry, res)
		}
	}
	for _, entry := range []string{"RangeEmpty", "PureStrings", "PureReflect"} {
		got := u04dResources(t, dir, entry, rt)
		if len(got) != 1 {
			t.Fatalf("%s storage 数量 = %d", entry, len(got))
		}
		res := u04dResourceMap(t, got[0])
		if res["status"] != "bound" {
			t.Fatalf("%s resource = %+v（want bound）", entry, res)
		}
	}
}

// TestU04dNoSnapshotOmitsResource 无快照时不输出 resource。
func TestU04dNoSnapshotOmitsResource(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := u04dFixture(t, u04dRuntime)
	got := u04dResources(t, dir, "Save", "")
	if len(got) != 1 {
		t.Fatalf("storage 数量 = %d", len(got))
	}
	if got[0]["resource"] != nil {
		t.Fatalf("无快照应省略 resource: %+v", got[0])
	}
}

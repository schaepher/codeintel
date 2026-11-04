//go:build integration

package integration

// todo29 U04k：包装函数返回槽传播、冲突失效与 caller 条件（真实 init → CLI）。

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

const u29kGuardSrc = `package guardreturned
import "database/sql"
var DB *sql.DB
func Hit(){}
func Conflicting(b bool)error {
 _,a:=DB.Exec("INSERT INTO nodes(id) VALUES (?)",1)
 _,z:=DB.Exec("INSERT INTO nodes(id) VALUES (?)",2)
 if b{return a}
 return z
}
func ConflictingEntry(b bool){err:=Conflicting(b);if err!=nil{Hit()}}
`

func u29kCount(t *testing.T, out string, field string) int {
	t.Helper()
	var res struct {
		Nodes []map[string]any `json:"Nodes"`
		Else  []map[string]any `json:"Else"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("json: %v", err)
	}
	n := 0
	var walk func(m map[string]any)
	walk = func(m map[string]any) {
		if _, ok := m[field].(map[string]any); ok {
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
	for _, m := range res.Nodes {
		walk(m)
	}
	return n
}

// 不同写入返回到同一错误槽 → 不在 caller 生成确定写入条件。
func TestU29kConflictingWrites(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/guardreturned\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), u29kGuardSrc)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	for _, args := range [][2]string{{"1", "32"}, {"12", "32"}} {
		code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/guardreturned:ConflictingEntry",
			"--repo", dir, "--code", "--depth", args[0], "--continue-segments", args[1],
			"--evidence", "--json")
		if code != 0 {
			t.Fatalf("depth=%s query exit=%d\n%s", args[0], code, out)
		}
		if got := u29kCount(t, out, "write_condition"); got != 0 {
			t.Fatalf("depth=%s caller write_condition = %d, want 0\n%s", args[0], got, out)
		}
	}
}

const u29kReturnedSrc = `package returned
import "github.com/xormplus/xorm"
var Session *xorm.Session
func Hit(){}
func Save()(int64,error){return Session.Insert("node")}
func Wrap()(int64,error){return Save()}
func Nested(){rows,err:=Wrap();if err!=nil{Hit()};if rows>0{Hit()}}
func Named()(n int64,err error){n,err=Session.Insert("node");return}
func NamedNested(){rows,err:=Named();if err!=nil{Hit()};if rows>0{Hit()}}
func Escaped()(int64,error){n,err:=Save();p:=&err;_=p;return n,err}
func EscapedNested(){rows,err:=Escaped();if err!=nil{Hit()};if rows>0{Hit()}}
func Deferred()(n int64,err error){defer func(){err=nil}();n,err=Session.Insert("node");return}
func DeferredNested(){rows,err:=Deferred();if err!=nil{Hit()};if rows>0{Hit()}}
`

// 嵌套包装：return Save() 的槽来源传播到 Wrap 调用方；depth1+续查与 depth12 一致。
func TestU29kWrapperReturnSlots(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "orm", "go.mod"), "module github.com/xormplus/xorm\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(root, "orm", "orm.go"), `package xorm
type Session struct{}
func(*Session)Insert(...any)(int64,error){return 0,nil}
`)
	dir := filepath.Join(root, "app")
	writeFile(t, filepath.Join(dir, "go.mod"), `module example.com/returned

go 1.26.2
require github.com/xormplus/xorm v0.0.0
replace github.com/xormplus/xorm => ../orm
`)
	writeFile(t, filepath.Join(dir, "flow.go"), u29kReturnedSrc)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	for _, args := range [][2]string{{"1", "32"}, {"12", "32"}} {
		code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/returned:Nested",
			"--repo", dir, "--code", "--depth", args[0], "--continue-segments", args[1],
			"--evidence", "--json")
		if code != 0 {
			t.Fatalf("depth=%s query exit=%d\n%s", args[0], code, out)
		}
		if got := u29kCount(t, out, "write_condition"); got != 1 {
			t.Fatalf("depth=%s write_condition = %d, want 1\n%s", args[0], got, out)
		}
		if got := u29kCount(t, out, "row_count_condition"); got != 1 {
			t.Fatalf("depth=%s row_count_condition = %d, want 1\n%s", args[0], got, out)
		}
	}
}

// 命名返回值保留槽来源；取址逃逸/defer 改写不得补成确定写入结果。
func TestU29kSlotInvalidation(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "orm", "go.mod"), "module github.com/xormplus/xorm\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(root, "orm", "orm.go"), `package xorm
type Session struct{}
func(*Session)Insert(...any)(int64,error){return 0,nil}
`)
	dir := filepath.Join(root, "app")
	writeFile(t, filepath.Join(dir, "go.mod"), `module example.com/returned

go 1.26.2
require github.com/xormplus/xorm v0.0.0
replace github.com/xormplus/xorm => ../orm
`)
	writeFile(t, filepath.Join(dir, "flow.go"), u29kReturnedSrc)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	for _, tc := range []struct {
		entry string
		want  bool
	}{
		{"NamedNested", true},
		{"EscapedNested", false},
		{"DeferredNested", false},
	} {
		code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/returned:"+tc.entry,
			"--repo", dir, "--code", "--depth", "1", "--continue-segments", "32",
			"--evidence", "--json")
		if code != 0 {
			t.Fatalf("%s query exit=%d\n%s", tc.entry, code, out)
		}
		got := u29kCount(t, out, "write_condition") == 1
		if got != tc.want {
			t.Fatalf("%s write_condition=%t, want %t\n%s", tc.entry, got, tc.want, out)
		}
	}
}

//go:build integration

package integration

// todo30 U04e：标准 SQL Scan 的列来源、错误身份与成功前提——真实源码 → init → CLI。
// 只验收来源关系（不证明读取成功/列值/记录存在）。

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

const u30ScanSrc = `package scanx
import ("context";"database/sql")
var DB *sql.DB
func Complete(string){}
func mutate(any){}
func open(ctx context.Context,id string)*sql.Row{return DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id)}
func Entry(ctx context.Context,id string){
 var kind string
 if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&kind);err!=nil{return}
 if kind=="profile"{Complete(id)}
}
func Escaped(ctx context.Context,id string){
 var kind string
 mutate(&kind)
 if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&kind);err!=nil{return}
 if kind=="profile"{Complete(id)}
}
func Overwritten(ctx context.Context,id string){
 var kind string
 if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&kind);err!=nil{return}
 kind="other"
 if kind=="profile"{Complete(id)}
}
func TooMany(ctx context.Context,id string){
 kind:="old"
 var other string
 if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&kind,&other);err!=nil{return}
 if kind=="profile"{Complete(id)}
}
func ViaHelper(ctx context.Context,id string){
 row:=open(ctx,id)
 alias:=row
 var kind string
 if err:=alias.Scan(&kind);err!=nil{return}
 if kind=="profile"{Complete(id)}
}
func Deferred(ctx context.Context,id string){
 row:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id)
 var kind string
 defer row.Scan(&kind)
 if kind=="profile"{Complete(id)}
}
type custom struct{}
func(c *custom)Scan(any)error{return nil}
func CustomScanner(ctx context.Context,id string){
 row:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id)
 var c custom
 if err:=row.Scan(&c);err!=nil{return}
 if c.Scan(nil)==nil{Complete(id)}
}
func Loop(ctx context.Context,id string){
 rows,err:=DB.QueryContext(ctx,"SELECT kind FROM nodes WHERE id=?",id)
 if err!=nil{return}
 for rows.Next(){
  var kind string
  if err:=rows.Scan(&kind);err!=nil{return}
  if kind=="profile"{Complete(id)}
 }
}
`

func u30ScanRepo(t *testing.T) string {
	t.Helper()
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/scanx\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), u30ScanSrc)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

// u30ScanProjection 入口 → branch 条件左操作数（scan_error/read_column 摘要）。
func u30ScanProjection(t *testing.T, dir, entry, depth, segments string) ([]map[string]any, []map[string]any) {
	t.Helper()
	args := []string{"query", "sequence", "symbol:go:example.com/scanx:" + entry,
		"--repo", dir, "--code", "--depth", depth, "--json"}
	if segments != "" {
		args = append(args, "--continue-segments", segments)
	}
	code, out := runCLIOut(t, args...)
	if code != 0 {
		t.Fatalf("%s exit=%d\n%s", entry, code, out)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("%s JSON: %v\n%s", entry, err, out)
	}
	var lefts, scans []map[string]any
	var walk func(nodes []any)
	walk = func(nodes []any) {
		for _, raw := range nodes {
			n, _ := raw.(map[string]any)
			if ev, _ := n["evidence"].(map[string]any); ev != nil {
				if sc, _ := ev["scan"].(map[string]any); sc != nil {
					scans = append(scans, sc)
				}
			}
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
	return lefts, scans
}

func u30FindReadColumn(lefts []map[string]any) map[string]any {
	for _, l := range lefts {
		if l["kind"] == "read_column" {
			rc, _ := l["read_column"].(map[string]any)
			return rc
		}
	}
	return nil
}

func TestU30U04eScanColumnSources(t *testing.T) {
	dir := u30ScanRepo(t)

	// 正例：err 槽 + 列来源（同一 Scan 身份）。
	lefts, scans := u30ScanProjection(t, dir, "Entry", "12", "")
	if len(lefts) != 2 {
		t.Fatalf("Entry 条件数 = %d（want 2）: %v", len(lefts), lefts)
	}
	if lefts[0]["kind"] != "scan_error" {
		t.Fatalf("err!=nil 左操作数应为 scan_error: %v", lefts[0])
	}
	sr, _ := lefts[0]["scan_result"].(map[string]any)
	read, _ := sr["read"].(map[string]any)
	if sr["id"] != "scan-1" || read["id"] != "read-1" {
		t.Fatalf("scan_error 身份不符: %v", lefts[0])
	}
	rc := u30FindReadColumn(lefts)
	if rc == nil || rc["column"] != "kind" || rc["when"] != "scan_success" {
		t.Fatalf("read_column 不符: %v", rc)
	}
	scanObj, _ := rc["result"].(map[string]any)
	scanRead, _ := scanObj["read"].(map[string]any)
	if scanObj["id"] != "scan-1" || scanRead["id"] != "read-1" {
		t.Fatalf("read_column 的 Scan/读取身份不符: %v", rc)
	}
	if len(scans) != 1 {
		t.Fatalf("evidence.scan 数 = %d（want 1）", len(scans))
	}
	targets, _ := scans[0]["targets"].([]any)
	if len(targets) != 1 || targets[0].(map[string]any)["column"] != "kind" {
		t.Fatalf("Scan targets 不符: %v", scans[0]["targets"])
	}

	// 取址在 Scan 之前 → 持续逃逸，不生成列来源。
	if lefts, _ := u30ScanProjection(t, dir, "Escaped", "12", ""); u30FindReadColumn(lefts) != nil {
		t.Fatalf("Scan 前的取址逃逸不应生成 read_column: %v", lefts)
	}
	// Scan 后覆盖 → 新字面量（不能沿用旧列来源）。
	lefts, _ = u30ScanProjection(t, dir, "Overwritten", "12", "")
	if rc := u30FindReadColumn(lefts); rc != nil {
		t.Fatalf("覆盖后不应保留 read_column: %v", rc)
	}
	if lefts[len(lefts)-1]["literal"] != `"other"` {
		t.Fatalf("覆盖后应为新字面量: %v", lefts)
	}
	// 列数不符 → 不保留 Scan 前的旧值。
	lefts, _ = u30ScanProjection(t, dir, "TooMany", "12", "")
	if u30FindReadColumn(lefts) != nil || lefts[len(lefts)-1]["kind"] != nil {
		t.Fatalf("列数不符应清掉旧值且不给列来源: %v", lefts)
	}
	// 读取封装为辅助函数 + 别名 Scan → 仍保留查询身份。
	lefts, _ = u30ScanProjection(t, dir, "ViaHelper", "12", "")
	if rc := u30FindReadColumn(lefts); rc == nil {
		t.Fatalf("辅助函数返回 Row 经别名 Scan 应保留列来源: %v", lefts)
	}
	// 延迟 Scan 不借用当前调用的列值。
	if lefts, _ := u30ScanProjection(t, dir, "Deferred", "12", ""); u30FindReadColumn(lefts) != nil {
		t.Fatalf("defer Scan 不应生成 read_column: %v", lefts)
	}
	// 自定义 Scanner / 自定义同名 Scan → 不生成列来源。
	if lefts, _ := u30ScanProjection(t, dir, "CustomScanner", "12", ""); u30FindReadColumn(lefts) != nil {
		t.Fatalf("自定义 Scanner 不应生成 read_column: %v", lefts)
	}
}

// TestU30U04eRowsAndDepth：Rows 成功前提 + depth=1 续查与完整深度一致。
func TestU30U04eRowsAndDepth(t *testing.T) {
	dir := u30ScanRepo(t)
	lefts, scans := u30ScanProjection(t, dir, "Loop", "12", "")
	if len(scans) != 1 {
		t.Fatalf("Loop evidence.scan 数 = %d（want 1）", len(scans))
	}
	conds, _ := scans[0]["success_conditions"].([]any)
	var got []string
	for _, c := range conds {
		got = append(got, c.(string))
	}
	want := "query_success,rows_next_true,scan_success"
	if strings.Join(got, ",") != want {
		t.Fatalf("Rows success_conditions = %v（want %s）", got, want)
	}
	rc := u30FindReadColumn(lefts)
	if rc == nil || rc["column"] != "kind" {
		t.Fatalf("Rows 内 Scan 列来源不符: %v", lefts)
	}

	// depth=1 + 续查：Scan/读取身份与列来源一致。
	full, fullScans := u30ScanProjection(t, dir, "Entry", "12", "")
	shallow, shallowScans := u30ScanProjection(t, dir, "Entry", "1", "32")
	fullRC, shallowRC := u30FindReadColumn(full), u30FindReadColumn(shallow)
	if fullRC == nil || shallowRC == nil {
		t.Fatalf("depth 一致性缺少 read_column: full=%v shallow=%v", fullRC, shallowRC)
	}
	fullScan, _ := fullRC["result"].(map[string]any)
	shallowScan, _ := shallowRC["result"].(map[string]any)
	fullRead, _ := fullScan["read"].(map[string]any)
	shallowRead, _ := shallowScan["read"].(map[string]any)
	if fullScan["id"] != shallowScan["id"] || fullRead["id"] != shallowRead["id"] ||
		fullRC["column"] != shallowRC["column"] || fullRC["when"] != shallowRC["when"] {
		t.Fatalf("depth1+续查 与 depth12 来源不一致:\nfull=%v\nshallow=%v", fullRC, shallowRC)
	}
	if len(fullScans) != 1 || len(shallowScans) != 1 {
		t.Fatalf("Scan 证据数不一致: full=%d shallow=%d", len(fullScans), len(shallowScans))
	}
}

//go:build integration

package integration

// todo25 §13.4：生成 DAO/动态过滤参数（工厂/方法/动态 SQL 字符串/IN/args…）。

import (
	"path/filepath"
	"strings"
	"testing"
)

const todo25DAOSrc = `package runtimeflow
import ("context"; "database/sql")
var DB *sql.DB
type Row struct { Region string; NodeType int; ServiceType int; Enabled int }
type Filter struct { Region string; NodeType int; Services []int }
type DAO struct{ db *sql.DB }
func Manual(){}
func Auto(){}
func Failure(){}
func NewDAO(db *sql.DB) *DAO { return &DAO{db: db} }
type Repo interface{ QueryByFilter(context.Context, Filter) ([]*Row, error) }
func NewRepo(db *sql.DB) Repo { return NewDAO(db) }
func (d *DAO) QueryByFilter(ctx context.Context, f Filter) ([]*Row, error) {
    sql := "SELECT region,node_type,service_type,enabled FROM review_config WHERE region = ? AND node_type = ?"
    args := []any{f.Region, f.NodeType}
    if len(f.Services) > 0 {
        sql += " AND service_type IN (?,?)"
        args = append(args, f.Services[0], f.Services[1])
    }
    rows, err := d.db.QueryContext(ctx, sql, args...)
    if err != nil { return nil, err }
    defer rows.Close()
    var out []*Row
    for rows.Next() {
        r := &Row{}
        if err := rows.Scan(&r.Region, &r.NodeType, &r.ServiceType, &r.Enabled); err != nil { return nil, err }
        out = append(out, r)
    }
    return out, nil
}
func GroupBoth(ctx context.Context) {
    d := NewDAO(DB)
    rows, err := d.QueryByFilter(ctx, Filter{Region: "ID", NodeType: 2, Services: []int{1, 3}})
    if err != nil { Failure(); return }
    m := make(map[int][]*Row)
    for _, r := range rows { m[r.ServiceType] = append(m[r.ServiceType], r) }
    if len(m) == 2 { Manual() } else { Auto() }
    if r := m[1]; len(r) > 0 && r[0].Enabled == 1 { Manual() }
}
func GroupIface(ctx context.Context) {
    var r Repo = NewRepo(DB)
    rows, err := r.QueryByFilter(ctx, Filter{Region: "ID", NodeType: 2, Services: []int{1, 3}})
    if err != nil { Failure(); return }
    m := make(map[int][]*Row)
    for _, row := range rows { m[row.ServiceType] = append(m[row.ServiceType], row) }
    if len(m) == 2 { Manual() } else { Auto() }
}
func GroupOne(ctx context.Context) {
    d := NewDAO(DB)
    rows, err := d.QueryByFilter(ctx, Filter{Region: "ID", NodeType: 2, Services: []int{1, 1}})
    if err != nil { Failure(); return }
    m := make(map[int][]*Row)
    for _, r := range rows { m[r.ServiceType] = append(m[r.ServiceType], r) }
    if len(m) == 1 { Manual() } else { Auto() }
}
func GroupEmpty(ctx context.Context) {
    d := NewDAO(DB)
    rows, err := d.QueryByFilter(ctx, Filter{Region: "ID", NodeType: 2, Services: []int{1, 3}})
    if err != nil { Failure(); return }
    m := make(map[int][]*Row)
    for _, r := range rows { m[r.ServiceType] = append(m[r.ServiceType], r) }
    if len(m) == 0 { Manual() } else { Auto() }
}
`

func writeDAORepo(t *testing.T, snapshot string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/runtimeflow\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), todo25DAOSrc)
	snap := filepath.Join(dir, "runtime-v2.json")
	writeFile(t, snap, snapshot)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "init")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit=%d", code)
	}
	return dir, snap
}

func TestTodo25RuntimeGeneratedDAO(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir, snap := writeDAORepo(t, todo25CollectionV2)
	both, _ := runtimeSeqTree(t, dir, "symbol:go:example.com/runtimeflow:GroupBoth", 20, snap)
	if countRuntimeLabel(both, "Manual") != 2 || countRuntimeLabel(both, "Auto") != 0 ||
		countRuntimeLabel(both, "Failure") != 1 {
		t.Errorf("两项分组应 Manual=2 Auto=0 Failure=1: %d/%d/%d",
			countRuntimeLabel(both, "Manual"), countRuntimeLabel(both, "Auto"), countRuntimeLabel(both, "Failure"))
	}
	ev := runtimeEvidence(both)
	if ev == nil || ev.Table != "review_config" || len(ev.Query) != 3 || ev.Rows != 2 {
		t.Errorf("DAO 动态 SQL 应保留实际查询/快照证据: %+v", ev)
	}
	iface, _ := runtimeSeqTree(t, dir, "symbol:go:example.com/runtimeflow:GroupIface", 20, snap)
	if countRuntimeLabel(iface, "Manual") != 1 || countRuntimeLabel(iface, "Auto") != 0 {
		t.Errorf("接口持有具体 DAO/方法实参传播应走 len=2 分支: %d/%d",
			countRuntimeLabel(iface, "Manual"), countRuntimeLabel(iface, "Auto"))
	}
	one, _ := runtimeSeqTree(t, dir, "symbol:go:example.com/runtimeflow:GroupOne", 20, snap)
	if countRuntimeLabel(one, "Manual") != 1 || countRuntimeLabel(one, "Auto") != 0 {
		t.Errorf("服务集合 [1] 应走 len=1 分支: %d/%d",
			countRuntimeLabel(one, "Manual"), countRuntimeLabel(one, "Auto"))
	}
	emptySnap := strings.Replace(todo25CollectionV2,
		`{"region": "ID", "node_type": 2, "service_type": 1, "enabled": 1},
      {"region": "ID", "node_type": 2, "service_type": 3, "enabled": 0}`,
		``, 1)
	writeFile(t, snap, emptySnap)
	empty, _ := runtimeSeqTree(t, dir, "symbol:go:example.com/runtimeflow:GroupEmpty", 20, snap)
	if countRuntimeLabel(empty, "Manual") != 1 || countRuntimeLabel(empty, "Auto") != 0 {
		t.Errorf("完整空结果应走 len=0 分支: %d/%d",
			countRuntimeLabel(empty, "Manual"), countRuntimeLabel(empty, "Auto"))
	}
	// 错误/不完整：不得套值。
	writeFile(t, snap, strings.Replace(todo25CollectionV2, `"complete": true`, `"complete": false`, 1))
	incomplete, _ := runtimeSeqTree(t, dir, "symbol:go:example.com/runtimeflow:GroupBoth", 20, snap)
	if countRuntimeLabel(incomplete, "Auto") != 1 || countRuntimeLabel(incomplete, "Failure") != 1 {
		t.Errorf("不完整范围应保留 unknown/Failure: Auto=%d Failure=%d",
			countRuntimeLabel(incomplete, "Auto"), countRuntimeLabel(incomplete, "Failure"))
	}
}

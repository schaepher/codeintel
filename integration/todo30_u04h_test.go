//go:build integration

package integration

// todo30 U04h：同一源写入的多个读取联合约束一个请求——真实源码 → init → CLI。

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const u30JointSrc = `package joint
import("context";"database/sql")
var DB *sql.DB
func Submit(ctx context.Context,id string){DB.ExecContext(ctx,"INSERT INTO nodes(id,kind,status) VALUES(?,?,?)",id,"profile","ready")}
func Complete(string){}
func Review(ctx context.Context,id string){
 var kind,status string
 if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&kind);err!=nil{return}
 if err:=DB.QueryRowContext(ctx,"SELECT status FROM nodes WHERE id=?",id).Scan(&status);err!=nil{return}
 if kind=="profile" && status=="ready"{Complete(id)}
}
func AnotherParam(ctx context.Context,id,other string){
 var kind,status string
 if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&kind);err!=nil{return}
 if err:=DB.QueryRowContext(ctx,"SELECT status FROM nodes WHERE id=?",other).Scan(&status);err!=nil{return}
 if kind=="profile" && status=="ready"{Complete(id)}
}
func OtherTable(ctx context.Context,id string){
 var kind,status string
 if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&kind);err!=nil{return}
 if err:=DB.QueryRowContext(ctx,"SELECT status FROM other_nodes WHERE id=?",id).Scan(&status);err!=nil{return}
 if kind=="profile" && status=="ready"{Complete(id)}
}
func Unguarded(ctx context.Context,id string){
 var kind,status string
 if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&kind);err!=nil{return}
 DB.QueryRowContext(ctx,"SELECT status FROM nodes WHERE id=?",id).Scan(&status)
 if kind=="profile" && status=="ready"{Complete(id)}
}
`

func u30JointRepo(t *testing.T) string {
	t.Helper()
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/joint\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), u30JointSrc)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

// u30JointDecisions 入口 → 实例 related_reads 数 + 联合条件真值。
func u30JointDecisions(t *testing.T, dir, entry, depth, segments string) ([]int, []string) {
	t.Helper()
	args := []string{"query", "sequence", "symbol:go:example.com/joint:Submit",
		"--related-entry", "symbol:go:example.com/joint:" + entry,
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
		t.Fatalf("JSON: %v\n%s", err, out)
	}
	wf, _ := root["workflow"].(map[string]any)
	var reads []int
	var decisions []string
	insts, _ := wf["instances"].([]any)
	for _, raw := range insts {
		inst, _ := raw.(map[string]any)
		if inst["kind"] != "conditional_external_request" {
			continue
		}
		rr, _ := inst["related_reads"].([]any)
		reads = append(reads, len(rr))
		truth := ""
		var walk func(nodes []any)
		walk = func(nodes []any) {
			for _, nr := range nodes {
				n, _ := nr.(map[string]any)
				if n["Kind"] == "branch" &&
					strings.Contains(asString(n["Label"]), `kind=="profile" && status=="ready"`) {
					ic, _ := n["instance_condition"].(map[string]any)
					truth = asString(ic["truth"])
				}
				if sub, ok := n["Nodes"].([]any); ok {
					walk(sub)
				}
				if sub, ok := n["Else"].([]any); ok {
					walk(sub)
				}
			}
		}
		seq, _ := inst["sequence"].(map[string]any)
		nodes, _ := seq["Nodes"].([]any)
		walk(nodes)
		decisions = append(decisions, truth)
	}
	return reads, decisions
}

func TestU30U04hJointReads(t *testing.T) {
	dir := u30JointRepo(t)
	reads, decisions := u30JointDecisions(t, dir, "Review", "12", "")
	if len(reads) != 1 || reads[0] != 2 {
		t.Fatalf("联合读取数 = %v（want [2]）", reads)
	}
	if len(decisions) != 1 || decisions[0] != "true" {
		t.Fatalf("联合条件 = %v（want true）", decisions)
	}
	// Mermaid 显示第二次读取的 status 值与 joint_read_values_preserved。
	code, out := runCLIOut(t, "query", "sequence", "symbol:go:example.com/joint:Submit",
		"--related-entry", "symbol:go:example.com/joint:Review",
		"--repo", dir, "--code", "--depth", "12", "--format", "mermaid")
	if code != 0 {
		t.Fatalf("mermaid exit=%d", code)
	}
	for _, want := range []string{`status="ready"`, "joint_read_values_preserved"} {
		if !strings.Contains(out, want) {
			t.Fatalf("Mermaid 缺少 %q:\n%s", want, out)
		}
	}
	// 反例：另一个形参 → 两个独立候选请求，联合条件 unknown。
	reads, decisions = u30JointDecisions(t, dir, "AnotherParam", "12", "")
	if len(reads) != 2 || reads[0] != 1 || reads[1] != 1 {
		t.Fatalf("不同形参应保留两个独立读取: %v", reads)
	}
	for _, d := range decisions {
		if d != "unknown" {
			t.Fatalf("不同形参联合条件应 unknown: %v", decisions)
		}
	}
	// 反例：读其它表 → 不建立额外读取。
	reads, decisions = u30JointDecisions(t, dir, "OtherTable", "12", "")
	if len(reads) != 1 || reads[0] != 1 || decisions[0] != "unknown" {
		t.Fatalf("other_nodes 不应建立联合读取: %v / %v", reads, decisions)
	}
	// 反例：第二次 Scan 未检查错误 → 联合条件 unknown。
	reads, decisions = u30JointDecisions(t, dir, "Unguarded", "12", "")
	if len(decisions) != 1 || decisions[0] != "unknown" {
		t.Fatalf("未检查 Scan 错误应 unknown: %v", decisions)
	}
}

// TestU30U04hDepth 深度=1+续查与完整深度具有相同的读取身份与条件决定。
func TestU30U04hDepth(t *testing.T) {
	dir := u30JointRepo(t)
	fullReads, fullDec := u30JointDecisions(t, dir, "Review", "12", "")
	shallowReads, shallowDec := u30JointDecisions(t, dir, "Review", "1", "32")
	if strings.Join(intsToStrs(fullReads), ",") != strings.Join(intsToStrs(shallowReads), ",") ||
		strings.Join(fullDec, ",") != strings.Join(shallowDec, ",") {
		t.Fatalf("depth 一致性失败: full=%v/%v shallow=%v/%v", fullReads, fullDec,
			shallowReads, shallowDec)
	}
}

func intsToStrs(in []int) []string {
	out := make([]string, 0, len(in))
	for _, n := range in {
		out = append(out, strconv.Itoa(n))
	}
	return out
}

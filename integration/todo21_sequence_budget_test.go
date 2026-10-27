//go:build integration

package integration

// todo21-14.6：真实生产路径的小模块回归——从真实 Go 文件依次
// init → query sequence（JSON / Mermaid / 取消预算），全部走本轮构建的
// 二进制与真实 argv（不手工造图、不进程内调 cli.Main）。
//
// 说明：本用例只证明 CLI 契约（预算记录、取消出口、两次重复调用的目标
// 身份）；实际工作中途取消由仓储夹具
// TestCalleesContextInterruptsRecursiveSQL 独立证明，二者不可互相替代。
// 该小模块不是"大仓 61s 延迟"的独立复现（见 field_trace.md 记录）。

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// seqBudgetNode JSON 时序树节点（后端字段无 json tag——用导出名解码）。
type seqBudgetNode struct {
	Kind     string           `json:"Kind"`
	Label    string           `json:"Label"`
	Type     string           `json:"Type"`
	Line     int              `json:"Line"`
	Nodes    []*seqBudgetNode `json:"Nodes"`
	Else     *seqBudgetNode   `json:"Else"`
	Evidence *struct {
		SymbolID string `json:"symbol_id"`
		CallLine int    `json:"call_line"`
	} `json:"evidence"`
}

// walkSeqBudget 收集树中所有调用节点（含 Else 分支）。
func walkSeqBudget(n *seqBudgetNode, out *[]*seqBudgetNode) {
	if n == nil {
		return
	}
	if n.Kind == "call" {
		*out = append(*out, n)
	}
	for _, c := range n.Nodes {
		walkSeqBudget(c, out)
	}
	walkSeqBudget(n.Else, out)
}

// TestTodo21SequenceBudgetRealCLI：真实小模块 init → JSON → Mermaid →
// 1ns 预算取消。
func TestTodo21SequenceBudgetRealCLI(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	wd, _ := os.Getwd()
	repoRoot := filepath.Dir(wd)
	bin := filepath.Join(t.TempDir(), "codeintel")
	build := exec.Command("go", "build", "-o", bin, "./cmd/codeintel")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build codeintel: %v\n%s", err, out)
	}

	fixture := filepath.Join(t.TempDir(), "seqbudget")
	if err := os.MkdirAll(fixture, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(fixture, "go.mod"), "module example.com/seqbudget\n\ngo 1.26.0\n")
	writeFile(t, filepath.Join(fixture, "flow.go"), `package seqbudget

func Work(v int) (int, error) { return v + 1, nil }

func Run(v int) (int, error) {
    x, err := Work(v)
    if err != nil {
        return 0, err
    }
    return Work(x)
}
`)

	env := append(os.Environ(), "CODEINTEL_SEQ_TIMING=1")
	run := func(args ...string) (int, string, string) {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Env = env
		var stdout, stderr strings.Builder
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatalf("run %v: %v", args, err)
		}
		return code, stdout.String(), stderr.String()
	}

	// ① init（本文件唯一 init，串行执行）——断言真实索引产物
	if code, stdout, stderr := run("init", "--repo", fixture); code != 0 {
		t.Fatalf("init exit = %d\n%s\n%s", code, stdout, stderr)
	}
	db, err := sqlite.Open(fixture)
	if err != nil {
		t.Fatalf("init 未产出可打开的索引库: %v", err)
	}
	nodes, edges, err := sqlite.NewRepo(db).Counts()
	db.Close()
	if err != nil || nodes == 0 || edges == 0 {
		t.Fatalf("init 索引产物为空: nodes=%d edges=%d err=%v", nodes, edges, err)
	}

	// ② JSON：两次 Work 调用 + err 分支，目标身份正确；stderr 记录实测预算范围
	code, out, stderr := run("query", "sequence", "Run", "--repo", fixture,
		"--code", "--depth", "2", "--timeout", "2s", "--json")
	if code != 0 {
		t.Fatalf("JSON 查询 exit = %d\n%s", code, stderr)
	}
	var root seqBudgetNode
	if err := json.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("JSON 不可解码: %v\n%s", err, out)
	}
	var calls []*seqBudgetNode
	walkSeqBudget(&root, &calls)
	var work int
	for _, c := range calls {
		if c.Label == "Work" && c.Type == "seqbudget.Work" {
			work++
			if c.Evidence == nil || c.Evidence.SymbolID != "symbol:go:example.com/seqbudget:Work" {
				t.Errorf("Work 调用证据身份错误: %+v", c.Evidence)
			}
		}
	}
	if work != 2 {
		t.Errorf("JSON 应含 2 次 Work 调用，got %d（calls=%d）", work, len(calls))
	}
	// err 分支：branch err != nil + return 0, err
	var errBranch, errReturn bool
	var walkBranches func(n *seqBudgetNode)
	walkBranches = func(n *seqBudgetNode) {
		if n == nil {
			return
		}
		if n.Kind == "branch" && n.Label == "err != nil" {
			errBranch = true
			for _, c := range n.Nodes {
				if c.Kind == "return" && c.Label == "return 0, err" {
					errReturn = true
				}
			}
		}
		for _, c := range n.Nodes {
			walkBranches(c)
		}
		walkBranches(n.Else)
	}
	walkBranches(&root)
	if !errBranch || !errReturn {
		t.Errorf("JSON 应含 err != nil 分支及其 return 0, err（branch=%t return=%t）", errBranch, errReturn)
	}
	if !strings.Contains(stderr, "[seq.query] elapsed=") ||
		!strings.Contains(stderr, "budget=2s") || !strings.Contains(stderr, "canceled=false") {
		t.Errorf("stderr 应含实测预算范围记录:\n%s", stderr)
	}
	if !strings.Contains(stderr, "[seq] resolve=") || !strings.Contains(stderr, "[seq.stage]") {
		t.Errorf("stderr 应含阶段耗时:\n%s", stderr)
	}
	t.Logf("JSON 预算记录: %s", firstLineWith(stderr, "[seq.query]"))

	// ③ Mermaid（--mermaid 别名，与 --format mermaid 等价）
	code, out, stderr = run("query", "sequence", "Run", "--repo", fixture,
		"--code", "--depth", "2", "--timeout", "2s", "--mermaid")
	if code != 0 {
		t.Fatalf("Mermaid 查询 exit = %d\n%s", code, stderr)
	}
	if !strings.HasPrefix(strings.TrimSpace(out), "sequenceDiagram") {
		t.Errorf("--mermaid 应输出 Mermaid:\n%s", out)
	}
	if n := strings.Count(out, "P0->>P1: Work"); n != 2 {
		t.Errorf("Mermaid 应含 2 次 Work 调用，got %d:\n%s", n, out)
	}
	for _, want := range []string{"alt err != nil", "return 0, err", "seqbudget.Work"} {
		if !strings.Contains(out, want) {
			t.Errorf("Mermaid 缺 %q:\n%s", want, out)
		}
	}

	// ④ 1ns 预算：非零退出、stdout 为空、stderr 含预算错误 + canceled=true
	code, out, stderr = run("query", "sequence", "Run", "--repo", fixture,
		"--code", "--depth", "2", "--timeout", "1ns", "--json")
	if code == 0 {
		t.Fatalf("1ns 预算应失败退出")
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("取消时 stdout 必须为空:\n%s", out)
	}
	if !strings.Contains(stderr, "预算") || !strings.Contains(stderr, "canceled=true") {
		t.Errorf("stderr 应含预算错误 + canceled=true:\n%s", stderr)
	}
	t.Logf("1ns 预算记录: %s", firstLineWith(stderr, "[seq.query]"))
}

// firstLineWith 取含标记的第一行（诊断日志断言/记录用）。
func firstLineWith(s, marker string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, marker) {
			return line
		}
	}
	return ""
}

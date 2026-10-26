//go:build integration

package integration

// todo12 验收：真实源码 → 生产索引 → CLI（A1/A3/A6/A9 + task 12 A1/A2）。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
)

const todo12Flow = `package example

type Record struct { ApprovalState, AuditState string }

func Advance(record *Record, approved bool) (approval string, audit string) {
	record.AuditState = "logged"
	if approved {
		record.ApprovalState = "accepted"
		return record.ApprovalState, record.AuditState
	}
	record.ApprovalState = "pending"
	return record.ApprovalState, record.AuditState
}

func Apply(r *Record) {
	r.ApprovalState = "applied"
}

func AdvanceApply(record *Record) error {
	Apply(record)
	return nil
}

func Named() (state, reason string) {
	return "approved", "checked"
}

func Bare() (state, reason string) {
	state = "ok"
	reason = "why"
	return
}
`

func setupTodo12Repo(t *testing.T) string {
	t.Helper()
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module example.com/effects\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "flow.go"), []byte(todo12Flow), 0o644); err != nil {
		t.Fatal(err)
	}
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

func queryEffectsJSON(t *testing.T, dir string, args ...string) *action.EffectsResult {
	t.Helper()
	full := append([]string{"query", "effects", "--repo", dir, "--json"}, args...)
	code, out := runCLIOut(t, full...)
	if code != 0 {
		t.Fatalf("query effects exit = %d; out=%s", code, out)
	}
	var res action.EffectsResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("effects JSON: %v\n%s", err, out)
	}
	return &res
}

func TestTodo12EffectsTargetFieldRealIndex(t *testing.T) {
	dir := setupTodo12Repo(t)
	sym := "symbol:go:example.com/effects:Advance"

	// A1：字段目标
	res := queryEffectsJSON(t, dir, sym, "--target-field", "example.com/effects.Record.ApprovalState", "--depth", "1")
	if res.TargetInfo == nil || res.TargetInfo.Kind != "field" ||
		res.TargetInfo.Field != "example.com/effects.Record.ApprovalState" {
		t.Fatalf("target_info = %+v", res.TargetInfo)
	}
	if res.Analysis == nil || res.Analysis.Status != "complete" || res.Analysis.Depth != 1 {
		t.Fatalf("analysis = %+v", res.Analysis)
	}
	if len(res.Paths) != 2 {
		t.Fatalf("应两条路径: %+v", res.Paths)
	}
	for _, p := range res.Paths {
		if len(p.Writes) != 1 || !strings.HasSuffix(p.Writes[0].Field, "ApprovalState") {
			t.Errorf("%s 应只含 ApprovalState 写入: %+v", p.ID, p.Writes)
		}
		for _, w := range p.Writes {
			if strings.Contains(w.Field, "AuditState") {
				t.Errorf("%s 不得含 AuditState: %+v", p.ID, p.Writes)
			}
		}
		if p.Result == nil || len(p.Result.Slots) != 1 || p.Result.Slots[0] != "#0" {
			t.Errorf("%s result 应为 #0: %+v", p.ID, p.Result)
		}
	}
	if len(res.DBWrites) != 1 || !strings.HasSuffix(res.DBWrites[0].Field, "ApprovalState") {
		t.Errorf("函数级汇总应排除 AuditState: %+v", res.DBWrites)
	}
	for _, r := range res.Result.Returns {
		if strings.Contains(r, "AuditState") {
			t.Errorf("函数级 result 不得含 AuditState: %+v", res.Result)
		}
	}
	// 文本渲染同步
	code, text := runCLIOut(t, "query", "effects", "--repo", dir, sym,
		"--target-field", "example.com/effects.Record.ApprovalState", "--depth", "1")
	if code != 0 {
		t.Fatalf("text exit = %d; %s", code, text)
	}
	if !strings.Contains(text, "目标：字段 example.com/effects.Record.ApprovalState") {
		t.Errorf("文本应显示目标字段:\n%s", text)
	}
	if !strings.Contains(text, "分析：complete") {
		t.Errorf("文本应显示完整性:\n%s", text)
	}
	if strings.Contains(text, "AuditState") {
		t.Errorf("文本不得出现 AuditState:\n%s", text)
	}

	// A3：返回槽 audit / #1 等价，不重编号
	r1 := queryEffectsJSON(t, dir, sym, "--target-return", "audit", "--depth", "1")
	r2 := queryEffectsJSON(t, dir, sym, "--target-return", "#1", "--depth", "1")
	a, _ := json.Marshal(r1.Paths)
	b, _ := json.Marshal(r2.Paths)
	if string(a) != string(b) {
		t.Fatalf("audit 与 #1 应等价:\n%s\n%s", a, b)
	}
	if r1.TargetInfo == nil || r1.TargetInfo.Name != "audit" || r1.TargetInfo.Slot != "#1" {
		t.Fatalf("target_info = %+v", r1.TargetInfo)
	}
	for _, p := range r1.Paths {
		if p.Result == nil || len(p.Result.Slots) != 1 || p.Result.Slots[0] != "#1" {
			t.Errorf("%s 槽身份不得重编号: %+v", p.ID, p.Result)
		}
		if len(p.Result.Returns) != 1 || p.Result.Returns[0] != "record.AuditState" {
			t.Errorf("%s 返回值 = %+v", p.ID, p.Result)
		}
		if len(p.Writes) != 1 || !strings.HasSuffix(p.Writes[0].Field, "AuditState") || p.Writes[0].Line != 6 {
			t.Errorf("%s 应只含 AuditState(L6) 写入: %+v", p.ID, p.Writes)
		}
	}
	// approval 与 #0 等价
	q0 := queryEffectsJSON(t, dir, sym, "--target-return", "approval", "--depth", "1")
	q1 := queryEffectsJSON(t, dir, sym, "--target-return", "#0", "--depth", "1")
	c, _ := json.Marshal(q0.Paths)
	d, _ := json.Marshal(q1.Paths)
	if string(c) != string(d) {
		t.Fatalf("approval 与 #0 应等价")
	}

	// A9：不指定目标保持完整路径（两条分支、全部写入）
	plain := queryEffectsJSON(t, dir, sym, "--depth", "1")
	if len(plain.Paths) != 2 {
		t.Fatalf("无目标应两条路径: %+v", plain.Paths)
	}
	for _, p := range plain.Paths {
		if len(p.Writes) != 2 {
			t.Errorf("%s 无目标应保留全部写入: %+v", p.ID, p.Writes)
		}
	}
}

func TestTodo12EffectsTargetCalleeRealIndex(t *testing.T) {
	dir := setupTodo12Repo(t)
	res := queryEffectsJSON(t, dir, "symbol:go:example.com/effects:AdvanceApply",
		"--target-field", "example.com/effects.Record.ApprovalState", "--depth", "2")
	if len(res.Paths) != 1 {
		t.Fatalf("应一条路径: %+v", res.Paths)
	}
	p := res.Paths[0]
	var callee *action.DBEffect
	for i := range p.Writes {
		if strings.Contains(p.Writes[i].Function, "Apply") {
			callee = &p.Writes[i]
		}
	}
	if callee == nil {
		t.Fatalf("路径应含被调写入: %+v", p.Writes)
	}
	if callee.File != "flow.go" || callee.Line == 0 {
		t.Errorf("被调写入应带文件/行号: %+v", callee)
	}
	if len(p.Calls) != 1 || !strings.Contains(p.Calls[0].Function, "Apply") || !p.Calls[0].Target {
		t.Errorf("应保留命中目标的调用: %+v", p.Calls)
	}
}

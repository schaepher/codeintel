//go:build integration

package integration

// todo12-10 A1/A2：真实源码 → 生产索引 → trace-backward 取实际写点 ID →
// value-trace，追到状态常量与条件合流（不硬编码 tN）。
//   - 常量节点（literal）与 phi 合流在 flows 中可见
//   - 字段名锚点与节点 ID 输入的契约分开（字段名 → 明确错误）
//   - 深度不足 → analysis.partial（不冒充完整）

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const todo12ValueTraceSrc = `package vt

type State string

const (
	StateQueued   State = "queued"
	StateRejected State = "rejected"
)

type StateHandler struct{ State State }

func (h *StateHandler) Apply(reject bool) State {
	status := StateQueued
	if reject {
		status = StateRejected
	}
	h.State = status
	return status
}

func (h *StateHandler) Value() State { return h.State }

type Layer struct{ State State }

func normalize(s State) State { return s }

func (l *Layer) Run(reject bool) State {
	status := StateQueued
	if reject {
		status = StateRejected
	}
	l.State = normalize(status)
	return status
}

type Other struct{ State State }

const (
	OtherA State = "other-a"
	OtherB State = "other-b"
)

func (o *Other) Apply2(reject bool) State {
	s := OtherA
	if reject {
		s = OtherB
	}
	o.State = s
	return s
}
`

func setupTodo12ValueTraceRepo(t *testing.T) string {
	t.Helper()
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module example.com/vt\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "flow.go"), []byte(todo12ValueTraceSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

func TestTodo12ValueTraceConstantsRealIndex(t *testing.T) {
	dir := setupTodo12ValueTraceRepo(t)
	// 1) trace-backward 取实际写点 ID（不硬编码 tN/行号）
	code, out := runCLIOut(t, "query", "trace-backward", "example.com/vt.StateHandler.State",
		"--func", "symbol:go:example.com/vt:(StateHandler).Apply", "--repo", dir, "--json")
	if code != 0 {
		t.Fatalf("trace-backward exit = %d; %s", code, out)
	}
	var trace struct {
		Rows []struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
			Line int    `json:"line"`
		} `json:"rows"`
	}
	if err := json.Unmarshal([]byte(out), &trace); err != nil {
		t.Fatalf("trace JSON: %v\n%s", err, out)
	}
	writeID := ""
	for _, r := range trace.Rows {
		if strings.Contains(r.ID, "State.write") {
			writeID = r.ID
			if r.Line == 0 {
				t.Errorf("写点应带源码行号: %+v", r)
			}
		}
	}
	if writeID == "" {
		t.Fatalf("trace-backward 应返回字段写点: %+v", trace.Rows)
	}

	// 2) value-trace → 常量 + phi 合流
	code, out = runCLIOut(t, "query", "value-trace", writeID, "--repo", dir, "--max-depth", "6", "--json")
	if code != 0 {
		t.Fatalf("value-trace exit = %d; %s", code, out)
	}
	var vt struct {
		Flows []struct {
			ID         string   `json:"id"`
			Name       string   `json:"name"`
			Depth      int      `json:"depth"`
			Edge       string   `json:"edge"`
			Line       int      `json:"line"`
			Conditions []string `json:"conditions"`
		} `json:"flows"`
		Analysis struct {
			Status string `json:"status"`
			Depth  int    `json:"depth"`
			Stop   string `json:"stop"`
		} `json:"analysis"`
	}
	if err := json.Unmarshal([]byte(out), &vt); err != nil {
		t.Fatalf("value-trace JSON: %v\n%s", err, out)
	}
	names := map[string]bool{}
	edges := map[string]bool{}
	for _, r := range vt.Flows {
		names[r.Name] = true
		edges[r.Edge] = true
		if r.Name == `"rejected"` && len(r.Conditions) == 0 {
			t.Errorf("rejected 常量应带条件依据（if reject）: %+v", r)
		}
	}
	for _, want := range []string{`"queued"`, `"rejected"`, "status"} {
		if !names[want] {
			t.Errorf("flows 应包含 %s（常量/合流）: %+v", want, vt.Flows)
		}
	}
	for _, bad := range []string{`"other-a"`, `"other-b"`} {
		if names[bad] {
			t.Errorf("同名不同实例的常量不得串链: %s", bad)
		}
	}
	if !edges["phi_operand"] {
		t.Errorf("应有 phi_operand 边（条件合流）: %+v", vt.Flows)
	}
	if vt.Analysis.Status != "complete" {
		t.Errorf("6 跳深度应 complete: %+v", vt.Analysis)
	}

	// 3) 深度不足 → partial（可能截断）
	code, out = runCLIOut(t, "query", "value-trace", writeID, "--repo", dir, "--max-depth", "1", "--json")
	if code != 0 {
		t.Fatalf("value-trace(depth1) exit = %d; %s", code, out)
	}
	if err := json.Unmarshal([]byte(out), &vt); err != nil {
		t.Fatalf("value-trace JSON: %v", err)
	}
	if vt.Analysis.Status != "partial" || vt.Analysis.Stop != "depth" {
		t.Errorf("深度不足应 partial/depth: %+v", vt.Analysis)
	}

	// 3b) 跨一层传参/返回：write base = normalize(status) → 仍可追到常量
	code, out = runCLIOut(t, "query", "trace-backward", "example.com/vt.Layer.State",
		"--func", "symbol:go:example.com/vt:(Layer).Run", "--repo", dir, "--json")
	if code != 0 {
		t.Fatalf("Layer trace-backward exit = %d; %s", code, out)
	}
	var lt struct {
		Rows []struct {
			ID string `json:"id"`
		} `json:"rows"`
	}
	if err := json.Unmarshal([]byte(out), &lt); err != nil {
		t.Fatalf("Layer trace JSON: %v", err)
	}
	layerWrite := ""
	for _, r := range lt.Rows {
		if strings.Contains(r.ID, "State.write") {
			layerWrite = r.ID
		}
	}
	if layerWrite == "" {
		t.Fatalf("Layer 写点缺失: %+v", lt.Rows)
	}
	code, out = runCLIOut(t, "query", "value-trace", layerWrite, "--repo", dir, "--max-depth", "8", "--json")
	if code != 0 {
		t.Fatalf("Layer value-trace exit = %d; %s", code, out)
	}
	if err := json.Unmarshal([]byte(out), &vt); err != nil {
		t.Fatalf("Layer value-trace JSON: %v", err)
	}
	lnames, ledges := map[string]bool{}, map[string]bool{}
	for _, r := range vt.Flows {
		lnames[r.Name] = true
		ledges[r.Edge] = true
	}
	for _, want := range []string{`"queued"`, `"rejected"`} {
		if !lnames[want] {
			t.Errorf("跨一层传参应保留常量来源 %s: %+v", want, vt.Flows)
		}
	}
	if !ledges["argument"] || !ledges["returns"] {
		t.Errorf("跨一层传参/返回应保留 argument/returns 边: %+v", vt.Flows)
	}

	// 4) 字段名锚点契约 → 明确错误（非零）
	code, out = runCLIOut(t, "query", "value-trace", "State", "--repo", dir, "--json")
	if code == 0 {
		t.Errorf("字段名锚点应报错（value-trace 只接受节点 ID）:\n%s", out)
	}
}

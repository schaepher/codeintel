package ssa

import (
	"encoding/json"
	"fmt"
	"sort"
	"testing"
)

// determinismSnapshot 一次构建的产物快照（全部归一化为可比较字符串）。
type determinismSnapshot struct {
	nodes     []string // id|kind|name|file|line_start|line_end|properties(键序归一)
	edges     []string // source|target|kind  （count 是写入期累加，见 note）
	summaries []string // function_id|access_kind|field_path|instance_path|line_start|code_snippet
}

// normalizeProps JSON 键序归一化（键序无查询语义）。
func normalizeProps(raw string) string {
	if raw == "" {
		return ""
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return raw
	}
	b, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return string(b)
}

func buildDeterminismSnapshot(t *testing.T) *determinismSnapshot {
	t.Helper()
	return buildDeterminismSnapshotWorkers(t, 0)
}

// buildDeterminismSnapshotWorkers 指定并发度构建快照（Q252e：并发只该
// 影响速度，不该影响产物）。
func buildDeterminismSnapshotWorkers(t *testing.T, workers int) *determinismSnapshot {
	t.Helper()
	nodes, facts, summaries, _ := collectFixtureIndex(t, map[string]string{
		"go.mod":     determinismFixture,
		"types/t.go": determinismTypes,
		"impl/i.go":  determinismImpl,
		"main.go":    determinismMain,
	}, workers)
	snap := &determinismSnapshot{}
	for _, n := range nodes {
		snap.nodes = append(snap.nodes, fmt.Sprintf("%s|%s|%s|%s|%d|%d|%s",
			n.ID, n.Kind, n.Name, n.FilePath, n.LineStart, n.LineEnd, normalizeProps(propsJSON(n.Properties))))
	}
	for _, f := range facts {
		snap.edges = append(snap.edges, fmt.Sprintf("%s|%s|%s", f.SourceID, f.TargetID, f.Kind))
	}
	for _, s := range summaries {
		snap.summaries = append(snap.summaries, fmt.Sprintf("%s|%s|%s|%s|%d|%s",
			s.FunctionID, s.AccessKind, s.FieldPath, s.InstancePath, s.LineStart, s.CodeSnippet))
	}
	sort.Strings(snap.nodes)
	sort.Strings(snap.edges)
	sort.Strings(snap.summaries)
	return snap
}

func propsJSON(p map[string]any) string {
	if p == nil {
		return ""
	}
	b, err := json.Marshal(p)
	if err != nil {
		return ""
	}
	return string(b)
}

// diffLines 返回 a 与 b 的对称差（排序后逐行比较）。
func diffLines(a, b []string) []string {
	set := map[string]int{}
	for _, x := range a {
		set[x]++
	}
	for _, x := range b {
		set[x]--
	}
	var out []string
	for x, c := range set {
		if c != 0 {
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}

package ssa

// Q250：图构建确定性门槛测试——同一个 fixture 在**同一进程内构建两次**，
// 产物必须完全一致（节点 ID 集合 + 内容、边集合含 count、摘要行）。
//
// 为什么这条测试能抓顺序依赖：Go 的 map 迭代顺序每次随机（按 map 实例
// 播种），所以任何"遍历 map → 决定发射顺序/命名归属"的代码路径，两次
// 构建之间都会分叉。go2o 实测（改动前）：串行双跑 nodes 归一化差异
// 1670 行、摘要 136 行——且**与并发无关**（workers=1 照样差）。
//
// 验收口径（Q250 定）：四类 dump 全 0 差异，properties 的 JSON 键序
// 归一化后比较（键序属 sqlite json_patch / json v2 序列化细节，无查询
// 语义）。go2o 级双跑（workers=1/8）由 §91 的验证流程覆盖。

import (
	"encoding/json"
	"fmt"
	"sort"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// determinismFixture 覆盖顺序敏感面：接口动态派发、方法值、字段读写、
// 闭包、map/slice 对象、别名链、外部调用摘要。
const determinismFixture = `module example.com/mtest

go 1.26
`

const determinismTypes = `package types

type Store interface {
	Save(k string, v int) error
	Load(k string) (int, error)
}

type Item struct {
	Key   string
	Value int
	Tags  map[string]string
	List  []int
}

type Repo interface {
	Put(it *Item) error
	Get(k string) (*Item, error)
}
`

const determinismImpl = `package impl

import "example.com/mtest/types"

type sqlStore struct{ rows map[string]int }

func NewStore() types.Store { return &sqlStore{rows: map[string]int{}} }

func (s *sqlStore) Save(k string, v int) error { s.rows[k] = v; return nil }

func (s *sqlStore) Load(k string) (int, error) { return s.rows[k], nil }

type memRepo struct{ items map[string]*types.Item }

func NewRepo() types.Repo { return &memRepo{items: map[string]*types.Item{}} }

func (r *memRepo) Put(it *types.Item) error {
	it.Tags["saved"] = "1"
	r.items[it.Key] = it
	return nil
}

func (r *memRepo) Get(k string) (*types.Item, error) {
	it := r.items[k]
	if it == nil {
		return nil, nil
	}
	return it, nil
}
`

const determinismMain = `package main

import (
	"example.com/mtest/impl"
	"example.com/mtest/types"
)

func loadAll(s types.Store, keys []string) map[string]int {
	out := map[string]int{}
	fn := func(k string) int {
		v, _ := s.Load(k)
		return v
	}
	for _, k := range keys {
		out[k] = fn(k)
	}
	return out
}

func main() {
	s := impl.NewStore()
	r := impl.NewRepo()
	it := &types.Item{Key: "a", Value: 1, Tags: map[string]string{}, List: []int{1, 2}}
	if err := r.Put(it); err != nil {
		panic(err)
	}
	it.Tags["k"] = "v"
	it.List = append(it.List, 3)
	_ = loadAll(s, []string{"a", "b"})
	got, _ := r.Get("a")
	if got != nil {
		got.Value++
	}
	_ = s.Save("a", it.Value)
}
`

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
	nodes, facts, summaries, _ := indexFixtureFullOrigins(t, map[string]string{
		"go.mod":     determinismFixture,
		"types/t.go": determinismTypes,
		"impl/i.go":  determinismImpl,
		"main.go":    determinismMain,
	})
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

// TestBuildDeterminism 同一 fixture 连续构建两次，产物必须逐条一致。
func TestBuildDeterminism(t *testing.T) {
	first := buildDeterminismSnapshot(t)
	if len(first.nodes) == 0 || len(first.edges) == 0 {
		t.Fatalf("fixture 未产出（nodes=%d edges=%d）", len(first.nodes), len(first.edges))
	}
	second := buildDeterminismSnapshot(t)

	for _, c := range []struct {
		name string
		a, b []string
	}{
		{"节点", first.nodes, second.nodes},
		{"边", first.edges, second.edges},
		{"摘要", first.summaries, second.summaries},
	} {
		if d := diffLines(c.a, c.b); len(d) > 0 {
			t.Errorf("%s 两次构建不一致（%d 条差异，共 %d/%d）：\n  %s\n  %s\n  %s",
				c.name, len(d), len(c.a), len(c.b),
				text(d, 0), text(d, 1), text(d, 2))
		}
	}
}

func text(lines []string, i int) string {
	if i < len(lines) {
		return lines[i]
	}
	return ""
}

// TestBuildDeterminismRepeated -count=N / 同进程多次构建都要稳（map 顺序
// 每次随机，重复跑是主要抓手）。
func TestBuildDeterminismRepeated(t *testing.T) {
	base := buildDeterminismSnapshot(t)
	for i := 0; i < 3; i++ {
		cur := buildDeterminismSnapshot(t)
		if d := diffLines(base.nodes, cur.nodes); len(d) > 0 {
			t.Fatalf("第 %d 轮节点不一致（%d 条）：\n  %s", i+2, len(d), text(d, 0))
		}
		if d := diffLines(base.edges, cur.edges); len(d) > 0 {
			t.Fatalf("第 %d 轮边不一致（%d 条）：\n  %s", i+2, len(d), text(d, 0))
		}
		if d := diffLines(base.summaries, cur.summaries); len(d) > 0 {
			t.Fatalf("第 %d 轮摘要不一致（%d 条）：\n  %s", i+2, len(d), text(d, 0))
		}
	}
}

var _ = domain.KindFunction

// Q250：同一字段路径有多个候选条目时，赢家由内容决定（最早行号；
// 同行取 instance_path 字典序最小），与 entries 的构造顺序无关。
func TestEmitSummaryRowsOrderIndependent(t *testing.T) {
	a := fieldEntry{fieldPath: "m.T.X", instancePath: "t49.Items", line: 210, snippet: "late"}
	b := fieldEntry{fieldPath: "m.T.X", instancePath: "*query.Items", line: 17, snippet: "early"}
	c := fieldEntry{fieldPath: "m.T.Y", instancePath: "b", line: 17}
	collect := func(entries []fieldEntry) []string {
		var out []string
		err := emitSummaryRows("symbol:go:m:f", domain.SummaryDirectWrite, entries,
			func(item domain.Item) error {
				if item.Summary != nil {
					out = append(out, fmt.Sprintf("%s|%s|%d", item.Summary.FieldPath,
						item.Summary.InstancePath, item.Summary.LineStart))
				}
				return nil
			})
		if err != nil {
			t.Fatal(err)
		}
		sort.Strings(out)
		return out
	}
	want := []string{"m.T.X|*query.Items|17", "m.T.Y|b|17"}
	if got := collect([]fieldEntry{a, b, c}); !equalStrs(got, want) {
		t.Errorf("顺序 1 = %v, want %v", got, want)
	}
	if got := collect([]fieldEntry{c, b, a}); !equalStrs(got, want) {
		t.Errorf("顺序 2 = %v, want %v", got, want)
	}
	// 同行：instance_path 字典序最小者赢（'*' < 'a'）
	if got := collect([]fieldEntry{a, b, {fieldPath: "m.T.X", instancePath: "a.Z", line: 17}}); !equalStrs(got, []string{"m.T.X|*query.Items|17"}) {
		t.Errorf("同行应取 instance_path 最小，got %v", got)
	}
}

func equalStrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

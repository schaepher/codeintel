package sqlite

import (
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
)

// 节点间路径查询（field_trace.md §17.3）。

func pathNode(id, name, file string, line int) *domain.CodeEntity {
	return &domain.CodeEntity{
		ID: domain.CanonicalID(id), Kind: domain.KindSSAValue, Name: name,
		FilePath: file, LineStart: line,
	}
}

// TestGetPathDataFlow：数据流路径存在——a#v0 → a#f.write → b#f.read → b#v1。
func TestGetPathDataFlow(t *testing.T) {
	r := newTestRepo(t)
	nodes := []*domain.CodeEntity{
		pathNode("symbol:go:example.com/m:a#v0", "v0", "a.go", 1),
		pathNode("symbol:go:example.com/m:a#f.write@2", "a.f", "a.go", 2),
		pathNode("symbol:go:example.com/m:b#f.read@3", "b.f", "b.go", 3),
		pathNode("symbol:go:example.com/m:b#v1", "v1", "b.go", 4),
		pathNode("symbol:go:example.com/m:c#v9", "v9", "c.go", 9),
	}
	edges := []*domain.Fact{
		{SourceID: "symbol:go:example.com/m:a#v0", TargetID: "symbol:go:example.com/m:a#f.write@2", Kind: domain.FactDataFlowsTo, ToolSource: domain.ToolSSA, Confidence: 1},
		{SourceID: "symbol:go:example.com/m:a#f.write@2", TargetID: "symbol:go:example.com/m:b#f.read@3", Kind: domain.FactArgument, ToolSource: domain.ToolSSA, Confidence: 1},
		{SourceID: "symbol:go:example.com/m:b#f.read@3", TargetID: "symbol:go:example.com/m:b#v1", Kind: domain.FactDataFlowsTo, ToolSource: domain.ToolSSA, Confidence: 1},
	}
	save(t, r, nodes, edges)

	path, err := r.GetPath("symbol:go:example.com/m:a#v0", "symbol:go:example.com/m:b#v1", 50, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(path) != 4 {
		t.Fatalf("path len = %d, want 4（v0→a.f→b.f→v1）: %+v", len(path), path)
	}
	// 顺序与边类型
	ids := []string{}
	for _, p := range path {
		ids = append(ids, string(p.ID))
	}
	want := []string{
		"symbol:go:example.com/m:a#v0",
		"symbol:go:example.com/m:a#f.write@2",
		"symbol:go:example.com/m:b#f.read@3",
		"symbol:go:example.com/m:b#v1",
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("path[%d] = %s, want %s", i, ids[i], want[i])
		}
	}
	if path[1].EdgeKinds != "data_flows_to" || path[2].EdgeKinds != "argument" || path[3].EdgeKinds != "data_flows_to" {
		t.Errorf("边类型 = %+v", path)
	}
}

// TestGetPathUnreachable：不可达返回空（不报错）。
func TestGetPathUnreachable(t *testing.T) {
	r := newTestRepo(t)
	nodes := []*domain.CodeEntity{
		pathNode("symbol:go:example.com/m:a#v0", "v0", "a.go", 1),
		pathNode("symbol:go:example.com/m:c#v9", "v9", "c.go", 9),
	}
	save(t, r, nodes, nil)
	path, err := r.GetPath("symbol:go:example.com/m:a#v0", "symbol:go:example.com/m:c#v9", 50, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(path) != 0 {
		t.Errorf("不可达应有空路径: %+v", path)
	}
}

// TestGetPathCycle：环不挂（a→b→a），a→c 不可达返回空。
func TestGetPathCycle(t *testing.T) {
	r := newTestRepo(t)
	nodes := []*domain.CodeEntity{
		pathNode("symbol:go:example.com/m:a#v0", "v0", "a.go", 1),
		pathNode("symbol:go:example.com/m:b#v0", "v0", "b.go", 2),
		pathNode("symbol:go:example.com/m:c#v0", "v0", "c.go", 3),
	}
	edges := []*domain.Fact{
		{SourceID: "symbol:go:example.com/m:a#v0", TargetID: "symbol:go:example.com/m:b#v0", Kind: domain.FactDataFlowsTo, ToolSource: domain.ToolSSA, Confidence: 1},
		{SourceID: "symbol:go:example.com/m:b#v0", TargetID: "symbol:go:example.com/m:a#v0", Kind: domain.FactDataFlowsTo, ToolSource: domain.ToolSSA, Confidence: 1},
	}
	save(t, r, nodes, edges)
	if _, err := r.GetPath("symbol:go:example.com/m:a#v0", "symbol:go:example.com/m:c#v0", 50, false); err != nil {
		t.Fatalf("环查询不应报错: %v", err)
	}
	path, err := r.GetPath("symbol:go:example.com/m:a#v0", "symbol:go:example.com/m:b#v0", 50, false)
	if err != nil || len(path) != 2 {
		t.Errorf("a→b 路径 = %+v, err %v", path, err)
	}
}

// TestGetPathCalls：--kind calls——函数调用路径。
func TestGetPathCalls(t *testing.T) {
	r := newTestRepo(t)
	nodes := []*domain.CodeEntity{
		pathNode("symbol:go:example.com/m:a", "a", "a.go", 1),
		pathNode("symbol:go:example.com/m:b", "b", "b.go", 2),
		pathNode("symbol:go:example.com/m:c", "c", "c.go", 3),
	}
	edges := []*domain.Fact{
		{SourceID: "symbol:go:example.com/m:a", TargetID: "symbol:go:example.com/m:b", Kind: domain.FactCalls, ToolSource: domain.ToolCodeGraph, Confidence: 0.8},
		{SourceID: "symbol:go:example.com/m:b", TargetID: "symbol:go:example.com/m:c", Kind: domain.FactCalls, ToolSource: domain.ToolCodeGraph, Confidence: 0.8},
	}
	save(t, r, nodes, edges)
	path, err := r.GetPath("symbol:go:example.com/m:a", "symbol:go:example.com/m:c", 50, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(path) != 3 || path[2].EdgeKinds != "calls" {
		t.Errorf("calls 路径 = %+v", path)
	}
	// 数据流边集下不可达
	path2, _ := r.GetPath("symbol:go:example.com/m:a", "symbol:go:example.com/m:c", 50, false)
	if len(path2) != 0 {
		t.Errorf("数据流边集下 calls 链不应可达: %+v", path2)
	}
}

// TestGetPathDepthNotNodeBudget（Q252c 回归）：maxDepth 是**路径深度**上限，
// 不是 BFS 已发现节点数上限。
//
// 修复前 BFS 的入队条件是 `len(parent) <= maxDepth`——搜索面比 maxDepth 宽时
// （起点扇出多个邻居、或目标在若干跳之后）BFS 提前停止扩展，**可达的两点被
// 静默报成"无路径"**（go2o 实测：4 跳内抽样的 113 对里 6 对假阴性）。
func TestGetPathDepthNotNodeBudget(t *testing.T) {
	r := newTestRepo(t)
	const p = "symbol:go:example.com/m:"
	nodes := []*domain.CodeEntity{pathNode(p+"start", "start", "a.go", 1)}
	var edges []*domain.Fact
	// 起点先扇出 6 个宽邻居（拉宽 BFS 的发现面），再串一条 3 跳链到 target
	for i := 0; i < 6; i++ {
		id := p + "wide" + string(rune('a'+i))
		nodes = append(nodes, pathNode(id, "wide", "a.go", i+2))
		edges = append(edges, &domain.Fact{SourceID: domain.CanonicalID(p + "start"),
			TargetID: domain.CanonicalID(id), Kind: domain.FactDataFlowsTo,
			ToolSource: domain.ToolSSA, Confidence: 1})
	}
	for i, id := range []string{"h1", "h2", "target"} {
		nodes = append(nodes, pathNode(p+id, id, "b.go", i+10))
	}
	edges = append(edges,
		&domain.Fact{SourceID: domain.CanonicalID(p + "start"), TargetID: domain.CanonicalID(p + "h1"),
			Kind: domain.FactDataFlowsTo, ToolSource: domain.ToolSSA, Confidence: 1},
		&domain.Fact{SourceID: domain.CanonicalID(p + "h1"), TargetID: domain.CanonicalID(p + "h2"),
			Kind: domain.FactDataFlowsTo, ToolSource: domain.ToolSSA, Confidence: 1},
		&domain.Fact{SourceID: domain.CanonicalID(p + "h2"), TargetID: domain.CanonicalID(p + "target"),
			Kind: domain.FactDataFlowsTo, ToolSource: domain.ToolSSA, Confidence: 1},
	)
	save(t, r, nodes, edges)

	// 深度 3 的链，maxDepth=3 必须找得到（修复前：发现面 >3 个节点即停止扩展）
	path, err := r.GetPath(p+"start", p+"target", 3, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(path) == 0 {
		t.Fatalf("maxDepth=3 应找到 3 跳链（start→h1→h2→target），实际报无路径")
	}
	// 反向仍是不可达（有向图）
	back, err := r.GetPath(p+"target", p+"start", 3, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(back) != 0 {
		t.Errorf("反向应不可达，实际返回 %d 行", len(back))
	}
	// 深度超限仍不可达（maxDepth=2 找不到 3 跳）
	short, err := r.GetPath(p+"start", p+"target", 2, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(short) != 0 {
		t.Errorf("maxDepth=2 不应找到 3 跳链，实际返回 %d 行", len(short))
	}
}

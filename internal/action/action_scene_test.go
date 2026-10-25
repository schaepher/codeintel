package action

// todo10-5：场景条件（--scene k=v）——静态可判定裁剪 + 一层条件传播。
//   - 同一入口在不同已知条件下生成对应路径（分支 taken/excluded）
//   - 未指定条件/未知条件保留候选（不裁剪）
//   - 条件经"调用点实参字面量 → 形参"一层传播后仍生效

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

const sceneSrc = `package m

func notifyCN() {}

func notifyUS() {}

func Route(region string) {
	if region == "CN" {
		notifyCN()
	} else if region == "US" {
		notifyUS()
	}
	if region != "CN" {
		notifyUS()
	}
}

func Dispatch() {
	Route("CN")
}

func DynamicDispatch(region string) {
	Route(region)
}
`

// seedSceneRepo 写源码 + 索引节点/调用边（含 Route 的调用边，便于 depth 展开）。
func seedSceneRepo(t *testing.T) (*Actions, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(sceneSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:Route", Kind: domain.KindFunction, Name: "Route", FilePath: "main.go", LineStart: 7},
		{ID: "symbol:go:example.com/m:Dispatch", Kind: domain.KindFunction, Name: "Dispatch", FilePath: "main.go", LineStart: 18},
		{ID: "symbol:go:example.com/m:DynamicDispatch", Kind: domain.KindFunction, Name: "DynamicDispatch", FilePath: "main.go", LineStart: 22},
		{ID: "symbol:go:example.com/m:notifyCN", Kind: domain.KindFunction, Name: "notifyCN", FilePath: "main.go", LineStart: 3},
		{ID: "symbol:go:example.com/m:notifyUS", Kind: domain.KindFunction, Name: "notifyUS", FilePath: "main.go", LineStart: 5},
	}
	facts := []*domain.Fact{
		// Dispatch → Route("CN")（第 18 行）；DynamicDispatch → Route(region)（第 22 行）
		{SourceID: "symbol:go:example.com/m:Dispatch", TargetID: "symbol:go:example.com/m:Route",
			Kind: domain.FactCalls, ToolSource: domain.ToolCodeGraph, Confidence: 0.8, Metadata: map[string]any{"line_num": float64(19)}},
		{SourceID: "symbol:go:example.com/m:DynamicDispatch", TargetID: "symbol:go:example.com/m:Route",
			Kind: domain.FactCalls, ToolSource: domain.ToolCodeGraph, Confidence: 0.8, Metadata: map[string]any{"line_num": float64(23)}},
	}
	if _, err := r.SaveBatchStats(nodes, facts, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(&domain.BuildMeta{BuildID: "scene1", ToolName: "fixture", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	return New(r), dir
}

// sceneBranches 收集树上分支的场景判定（按标签）。
func sceneBranches(root *CodeSeqNode) map[string]SceneFit {
	out := map[string]SceneFit{}
	var walk func(nodes []*CodeSeqNode)
	walk = func(nodes []*CodeSeqNode) {
		for _, n := range nodes {
			if n.Kind == "branch" && n.Scene != nil {
				out[n.Label] = *n.Scene
			}
			walk(n.Nodes)
			walk(n.Else)
		}
	}
	walk(root.Nodes)
	return out
}

// TestSceneRouteConditions：不同场景生成对应路径（taken/excluded），
// 未指定场景保留全部候选。
func TestSceneRouteConditions(t *testing.T) {
	acts, dir := seedSceneRepo(t)

	// 无场景：全部分支 unknown，内容都保留
	plain, err := acts.CodeSequence(CodeSequenceRequest{Target: "example.com/m:Route", RepoAbs: dir, Depth: 1})
	if err != nil || plain == nil {
		t.Fatalf("CodeSequence: %v root=%v", err, plain)
	}
	for label, fit := range sceneBranches(plain) {
		if fit.Status != SceneUnknown {
			t.Errorf("无场景时 %q 应为 unknown，实为 %+v", label, fit)
		}
	}

	// region=CN：第一个 if 命中（taken），else if 与实际不符（unknown：
	// else-if 链上的条件本身不含 else 语义——保留但标 unknown），
	// `region != "CN"` 被排除（excluded）
	cn, err := acts.CodeSequence(CodeSequenceRequest{Target: "example.com/m:Route", RepoAbs: dir,
		Depth: 1, Scene: NewScene([]string{"region=CN"})})
	if err != nil || cn == nil {
		t.Fatalf("CodeSequence: %v root=%v", err, cn)
	}
	fits := sceneBranches(cn)
	if fit := fits[`region == "CN"`]; fit.Status != SceneTaken || fit.Expected != "CN" {
		t.Errorf("region=CN 时 `region == \"CN\"` 应 taken: %+v", fit)
	}
	if fit := fits[`region != "CN"`]; fit.Status != SceneExcluded {
		t.Errorf("region=CN 时 `region != \"CN\"` 应 excluded: %+v", fit)
	}
	// 被排除的分支不再展开内容
	for _, n := range cn.Nodes {
		if n.Kind == "branch" && n.Label == `region != "CN"` && len(n.Nodes) != 0 {
			t.Errorf("被排除分支不应展开内容: %+v", n.Nodes)
		}
	}
	if cn.Meta == nil || cn.Meta.Scene == nil || !cn.Meta.Scene.Applied ||
		cn.Meta.Scene.Taken == 0 || cn.Meta.Scene.Excluded == 0 {
		t.Errorf("meta.scene 应统计 taken/excluded: %+v", cn.Meta)
	}

	// region=US：两个条件都判定（第一个 excluded，第二个 taken）
	us, err := acts.CodeSequence(CodeSequenceRequest{Target: "example.com/m:Route", RepoAbs: dir,
		Depth: 1, Scene: NewScene([]string{"region=US"})})
	if err != nil || us == nil {
		t.Fatalf("CodeSequence: %v root=%v", err, us)
	}
	fits = sceneBranches(us)
	if fit := fits[`region == "CN"`]; fit.Status != SceneExcluded {
		t.Errorf("region=US 时 `region == \"CN\"` 应 excluded: %+v", fit)
	}
	if fit := fits[`region != "CN"`]; fit.Status != SceneTaken {
		t.Errorf("region=US 时 `region != \"CN\"` 应 taken: %+v", fit)
	}
	// else if 分支（region == "US"）被 else 语义排除（父条件 excluded → else 成立，
	// 但 else-if 自身条件判定由场景给出 taken）——这里只要求不为 excluded 且不丢
	if _, ok := fits[`region == "US"`]; !ok {
		t.Errorf("else-if 分支应保留并带判定: %v", fits)
	}
}

// TestScenePropagatesThroughArgLiteral：调用点实参字面量 → 形参一层传播。
func TestScenePropagatesThroughArgLiteral(t *testing.T) {
	acts, dir := seedSceneRepo(t)
	// Dispatch 调 Route("CN")：即使没给 --scene，形参 region 也已知为 CN
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "example.com/m:Dispatch", RepoAbs: dir, Depth: 2})
	if err != nil || root == nil {
		t.Fatalf("CodeSequence: %v root=%v", err, root)
	}
	fits := sceneBranches(root)
	fit, ok := fits[`region == "CN"`]
	if !ok {
		t.Fatalf("展开 Route 后应有分支判定: %v", fits)
	}
	if fit.Status != SceneTaken || fit.Reason != "scene-arg:region=CN" {
		t.Errorf("实参字面量应传播为 taken（reason=scene-arg:region=CN）: %+v", fit)
	}

	// 动态实参（Route(region)）→ 无法判定，保留 unknown
	dyn, err := acts.CodeSequence(CodeSequenceRequest{Target: "example.com/m:DynamicDispatch", RepoAbs: dir, Depth: 2})
	if err != nil || dyn == nil {
		t.Fatalf("CodeSequence: %v root=%v", err, dyn)
	}
	for label, f := range sceneBranches(dyn) {
		if f.Status == SceneTaken || f.Status == SceneExcluded {
			t.Errorf("动态实参不应产生确定判定（%q = %+v）", label, f)
		}
	}
}

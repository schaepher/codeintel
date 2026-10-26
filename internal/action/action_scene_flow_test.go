package action

// todo11-2：场景条件的值流传播（有界）与实现选择。
//   - 条件经变量 + 两层函数传参后仍能约束目标分支
//   - 非字面量/无法证明 → unknown（不猜）；**不得按字段名相似推断同值**
//   - 接口候选被场景守卫选中时记录依据；无法证明 → 保持候选 + ambiguous

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

const sceneFlowSrc = `package m

func notifyCN() {}

func notifyUS() {}

// 最内层：按 region 分支
func Route(region string) {
	if region == "CN" {
		notifyCN()
	}
}

// 中层：把形参继续传下去（第二层）
func RouteFrom(region string) {
	Route(region)
}

// 顶层：变量链 + 字面量
func Dispatch() {
	region := "CN"
	RouteFrom(region)
}

// 顶层：变量来自请求字段（静态不可判定）
func DispatchDyn(region string) {
	RouteFrom(region)
}

// 字段名与场景键相似但值不同源——不得因此推断
func DispatchWrong() {
	otherRegion := "US"
	RouteFrom(otherRegion)
}
`

// seedSceneFlowRepo 建 fixture（含 calls 边，供向上找调用点）。
func seedSceneFlowRepo(t *testing.T) (*Actions, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(sceneFlowSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	mk := func(name string, line int) *domain.CodeEntity {
		return &domain.CodeEntity{ID: domain.CanonicalID("symbol:go:example.com/m:" + name),
			Kind: domain.KindFunction, Name: name, FilePath: "main.go", LineStart: line}
	}
	nodes := []*domain.CodeEntity{
		mk("Route", 8), mk("RouteFrom", 15), mk("Dispatch", 20),
		mk("DispatchDyn", 26), mk("DispatchWrong", 31),
		mk("notifyCN", 3), mk("notifyUS", 5),
	}
	facts := []*domain.Fact{
		{SourceID: "symbol:go:example.com/m:RouteFrom", TargetID: "symbol:go:example.com/m:Route",
			Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": float64(16)}},
		{SourceID: "symbol:go:example.com/m:Dispatch", TargetID: "symbol:go:example.com/m:RouteFrom",
			Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": float64(22)}},
		{SourceID: "symbol:go:example.com/m:DispatchDyn", TargetID: "symbol:go:example.com/m:RouteFrom",
			Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": float64(27)}},
		{SourceID: "symbol:go:example.com/m:DispatchWrong", TargetID: "symbol:go:example.com/m:RouteFrom",
			Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": float64(33)}},
	}
	if _, err := r.SaveBatchStats(nodes, facts, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(&domain.BuildMeta{BuildID: "flow1", ToolName: "test", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	return New(r), dir
}

// TestSceneFlowTwoLayers：Dispatch（变量 region := "CN"）→ RouteFrom → Route：
// 三层调用、两层传参后，最内层分支仍被判为 taken（reason=scene-arg）。
func TestSceneFlowTwoLayers(t *testing.T) {
	acts, dir := seedSceneFlowRepo(t)
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "symbol:go:example.com/m:Dispatch", RepoAbs: dir, Depth: 3})
	if err != nil || root == nil {
		t.Fatalf("CodeSequence: %v root=%v", err, root)
	}
	fit, ok := sceneBranches(root)[`region == "CN"`]
	if !ok {
		t.Fatalf("最内层分支应带场景判定: %+v", sceneBranches(root))
	}
	if fit.Status != SceneTaken {
		t.Errorf("经变量 + 两层传参后仍应 taken，实为 %+v", fit)
	}
	if !strings.HasPrefix(fit.Reason, "scene-arg:") {
		t.Errorf("依据应标明来自实参传播: %+v", fit)
	}
}

// TestSceneFlowUnknown：实参来自请求字段（静态不可判定）→ unknown（不猜）。
func TestSceneFlowUnknown(t *testing.T) {
	acts, dir := seedSceneFlowRepo(t)
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "symbol:go:example.com/m:DispatchDyn", RepoAbs: dir, Depth: 3})
	if err != nil || root == nil {
		t.Fatalf("CodeSequence: %v root=%v", err, root)
	}
	fit := sceneBranches(root)[`region == "CN"`]
	if fit.Status == SceneTaken || fit.Status == SceneExcluded {
		t.Errorf("无法证明的条件必须保持 unknown: %+v", fit)
	}
}

// TestSceneFlowNoNameSimilarity：变量名与场景键相似但值不同源 → 不得推断。
func TestSceneFlowNoNameSimilarity(t *testing.T) {
	acts, dir := seedSceneFlowRepo(t)
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "symbol:go:example.com/m:DispatchWrong", RepoAbs: dir, Depth: 3})
	if err != nil || root == nil {
		t.Fatalf("CodeSequence: %v root=%v", err, root)
	}
	fit := sceneBranches(root)[`region == "CN"`]
	if fit.Status == SceneTaken {
		t.Errorf("不得因变量名相似（otherRegion）推断 region=CN: %+v", fit)
	}
}

// TestSceneGuardedImplSelection：接口候选由场景守卫的赋值决定 → 选对应实现，
// 依据写进 evidence；无守卫 → 保持候选集合 + ambiguous。
func TestSceneGuardedImplSelection(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `package m

type Doer interface{ Do() error }

type ImplCN struct{}

func (ImplCN) Do() error { return nil }

type ImplUS struct{}

func (ImplUS) Do() error { return nil }

func Pick(platform string) Doer {
	var d Doer
	if platform == "CN" {
		d = &ImplCN{}
	} else {
		d = &ImplUS{}
	}
	d.Do()
	return d
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:Pick", Kind: domain.KindFunction, Name: "Pick", FilePath: "main.go", LineStart: 13},
		{ID: "symbol:go:example.com/m:Doer", Kind: domain.KindInterface, Name: "Doer"},
		{ID: "symbol:go:example.com/m:(Doer).Do", Kind: domain.KindMethod, Name: "(Doer).Do"},
		{ID: "symbol:go:example.com/m:ImplCN", Kind: domain.KindStruct, Name: "ImplCN"},
		{ID: "symbol:go:example.com/m:(ImplCN).Do", Kind: domain.KindMethod, Name: "(ImplCN).Do",
			FilePath: "main.go", LineStart: 7},
		{ID: "symbol:go:example.com/m:ImplUS", Kind: domain.KindStruct, Name: "ImplUS"},
		{ID: "symbol:go:example.com/m:(ImplUS).Do", Kind: domain.KindMethod, Name: "(ImplUS).Do",
			FilePath: "main.go", LineStart: 11},
	}
	facts := []*domain.Fact{
		{SourceID: "symbol:go:example.com/m:Pick", TargetID: "symbol:go:example.com/m:(Doer).Do",
			Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": float64(20)}},
		{SourceID: "symbol:go:example.com/m:Doer", TargetID: "symbol:go:example.com/m:ImplCN",
			Kind: domain.FactImplements, Confidence: 1.0},
		{SourceID: "symbol:go:example.com/m:Doer", TargetID: "symbol:go:example.com/m:ImplUS",
			Kind: domain.FactImplements, Confidence: 1.0},
	}
	if _, err := r.SaveBatchStats(nodes, facts, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(&domain.BuildMeta{BuildID: "impl1", ToolName: "test", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	acts := New(r)

	// 场景 platform=CN → 守卫选中 ImplCN（ImplType 为 ImplCN，不 ambiguous）
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "symbol:go:example.com/m:Pick", RepoAbs: dir,
		Depth: 2, Scene: NewScene([]string{"platform=CN"})})
	if err != nil || root == nil {
		t.Fatalf("CodeSequence: %v root=%v", err, root)
	}
	var call *CodeSeqNode
	for _, n := range root.Nodes {
		if n.Kind == "call" {
			call = n
		}
	}
	if call == nil {
		t.Fatalf("应有调用节点: %+v", root.Nodes)
	}
	if call.ImplType != "m.ImplCN" {
		t.Errorf("场景守卫应选中 ImplCN，实为 %q", call.ImplType)
	}
	if call.Evidence == nil || !strings.HasPrefix(call.Evidence.Reason, "scene-guard:") {
		t.Errorf("应记录场景守卫依据: %+v", call.Evidence)
	}
	if call.Evidence.Ambiguous {
		t.Errorf("守卫已唯一确定实现，不应 ambiguous: %+v", call.Evidence)
	}

	// 无场景 → 保持候选 + ambiguous（不静默选第一个）
	plain, err := acts.CodeSequence(CodeSequenceRequest{Target: "symbol:go:example.com/m:Pick", RepoAbs: dir, Depth: 2})
	if err != nil || plain == nil {
		t.Fatalf("CodeSequence: %v root=%v", err, plain)
	}
	var pc *CodeSeqNode
	for _, n := range plain.Nodes {
		if n.Kind == "call" {
			pc = n
		}
	}
	if pc == nil || pc.Evidence == nil || !pc.Evidence.Ambiguous || len(pc.Evidence.Candidates) < 2 {
		t.Errorf("无场景应保留候选并标 ambiguous: %+v", pc)
	}
}

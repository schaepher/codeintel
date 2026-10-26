package action

// todo12-3：场景传播使用调用点的到达定义（不是"第一个字面量"）。
//   - Reassign：region := "CN"; region = "US" → 内层条件 excluded
//   - ConditionalReassign：条件赋值合流 → unknown（不按 CN 裁剪）
//   - Dispatch 保持 taken / DispatchDynamic 保持 unknown
//   - 新增调用后才赋值、同名局部遮蔽、两分支同值/不同值

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

const sceneReachSrc = `package m

func notifyCN() {}

func notifyUS() {}

func Route(region string) {
	if region == "CN" {
		notifyCN()
	} else {
		notifyUS()
	}
}

func RouteFrom(region string) {
	Route(region)
}

func Reassign() {
	region := "CN"
	region = "US"
	RouteFrom(region)
}

func ConditionalReassign(change bool) {
	region := "CN"
	if change {
		region = "US"
	}
	RouteFrom(region)
}

func Dispatch() {
	region := "CN"
	RouteFrom(region)
}

func DispatchDyn(region string) {
	RouteFrom(region)
}

func DispatchAfter() {
	region := "US"
	region = "CN"
	RouteFrom(region)
}

func Shadowed() {
	region := "US"
	{
		region := "CN"
		_ = region
	}
	RouteFrom(region)
}

func SameBranches(change bool) {
	region := "US"
	if change {
		region = "CN"
	} else {
		region = "CN"
	}
	RouteFrom(region)
}

func DifferentBranches(change bool) {
	region := "US"
	if change {
		region = "CN"
	} else {
		region = "JP"
	}
	RouteFrom(region)
}
`

func seedSceneReachRepo(t *testing.T) (*Actions, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(sceneReachSrc), 0o644); err != nil {
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
		mk("Route", 7), mk("RouteFrom", 15), mk("Reassign", 19),
		mk("ConditionalReassign", 25), mk("Dispatch", 33), mk("DispatchDyn", 38),
		mk("DispatchAfter", 42), mk("Shadowed", 48), mk("SameBranches", 57),
		mk("DifferentBranches", 67), mk("notifyCN", 3), mk("notifyUS", 5),
	}
	call := func(caller string, line int) *domain.Fact {
		return &domain.Fact{SourceID: domain.CanonicalID("symbol:go:example.com/m:" + caller),
			TargetID: "symbol:go:example.com/m:RouteFrom", Kind: domain.FactCalls, Confidence: 0.8,
			Metadata: map[string]any{"line_num": float64(line)}}
	}
	facts := []*domain.Fact{
		{SourceID: "symbol:go:example.com/m:RouteFrom", TargetID: "symbol:go:example.com/m:Route",
			Kind: domain.FactCalls, Confidence: 0.8, Metadata: map[string]any{"line_num": float64(16)}},
		call("Reassign", 22), call("ConditionalReassign", 30), call("Dispatch", 35),
		call("DispatchDyn", 39), call("DispatchAfter", 45), call("Shadowed", 54),
		call("SameBranches", 64), call("DifferentBranches", 74),
	}
	if _, err := r.SaveBatchStats(nodes, facts, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(&domain.BuildMeta{BuildID: "reach1", ToolName: "test", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	return New(r), dir
}

func innerRegionFit(t *testing.T, acts *Actions, dir, entry string) SceneFit {
	t.Helper()
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/m:" + entry, RepoAbs: dir, Depth: 3})
	if err != nil || root == nil {
		t.Fatalf("%s CodeSequence: %v root=%v", entry, err, root)
	}
	fit, ok := sceneBranches(root)[`region == "CN"`]
	if !ok {
		t.Fatalf("%s 内层分支应带场景判定: %+v", entry, sceneBranches(root))
	}
	return fit
}

// A1：Reassign 的实际值为 US → 内层条件 excluded，US 分支保留。
func TestSceneReassignUsesReachingDef(t *testing.T) {
	acts, dir := seedSceneReachRepo(t)
	fit := innerRegionFit(t, acts, dir, "Reassign")
	if fit.Status != SceneExcluded {
		t.Fatalf("Reassign 内层条件应为 excluded（实际值 US），实为 %+v", fit)
	}
	if fit.Expected != "US" {
		t.Errorf("期望值应来自最后一次定义（US），实为 %+v", fit)
	}
	if fit.Source == "" {
		t.Errorf("证据应记录定义位置/传播依据: %+v", fit)
	}
}

// A1：ConditionalReassign 无法证明 → unknown，不能按 CN 裁剪。
func TestSceneConditionalReassignUnknown(t *testing.T) {
	acts, dir := seedSceneReachRepo(t)
	fit := innerRegionFit(t, acts, dir, "ConditionalReassign")
	if fit.Status != SceneUnknown {
		t.Fatalf("条件合流应 unknown，实为 %+v", fit)
	}
	if fit.Source == "" || fit.Reason != "unknown:arg-value" {
		t.Errorf("未知也应记录来源/依据: %+v", fit)
	}
}

// A2：Dispatch 保持 taken、DispatchDynamic 保持 unknown。
func TestSceneReachingBaselines(t *testing.T) {
	acts, dir := seedSceneReachRepo(t)
	if fit := innerRegionFit(t, acts, dir, "Dispatch"); fit.Status != SceneTaken {
		t.Errorf("Dispatch 应 taken: %+v", fit)
	}
	if fit := innerRegionFit(t, acts, dir, "DispatchDyn"); fit.Status == SceneTaken || fit.Status == SceneExcluded {
		t.Errorf("DispatchDyn 应 unknown: %+v", fit)
	}
}

// A3：调用后才赋值（定义顺序）→ 取最后定义；同名局部遮蔽 → 外层值。
func TestSceneReachingOrderAndShadowing(t *testing.T) {
	acts, dir := seedSceneReachRepo(t)
	if fit := innerRegionFit(t, acts, dir, "DispatchAfter"); fit.Status != SceneTaken {
		t.Errorf("调用前最后一次赋值 CN 应 taken: %+v", fit)
	}
	if fit := innerRegionFit(t, acts, dir, "Shadowed"); fit.Status != SceneExcluded {
		t.Errorf("块内同名变量不得遮蔽外层（外层 US → excluded）: %+v", fit)
	}
}

// A3：两分支相同值 → 可证明；不同值 → unknown（保留候选）。
func TestSceneReachingBranchMerge(t *testing.T) {
	acts, dir := seedSceneReachRepo(t)
	if fit := innerRegionFit(t, acts, dir, "SameBranches"); fit.Status != SceneTaken {
		t.Errorf("两分支同值 CN 应 taken: %+v", fit)
	}
	if fit := innerRegionFit(t, acts, dir, "DifferentBranches"); fit.Status != SceneUnknown {
		t.Errorf("两分支不同值应 unknown: %+v", fit)
	}
}

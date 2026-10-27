package action

// todo12-4：场景守卫的实现选择覆盖 else 与未知路径。
//   - platform=US → else 分支的 ImplUS（记录取反依据），排除 ImplCN
//   - 嵌套 else-if 逐层判定
//   - 无条件后续覆盖按到达定义生效
//   - 无场景/未初始化路径保持候选 + ambiguous（不表现成已确定）

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

const sceneImplSrc = `package m

type Doer interface{ Do() }

type ImplCN struct{}

func (ImplCN) Do() {}

type ImplUS struct{}

func (ImplUS) Do() {}

type ImplJP struct{}

func (ImplJP) Do() {}

func Pick(platform string) {
	var d Doer
	if platform == "CN" {
		d = &ImplCN{}
	} else if platform == "JP" {
		d = &ImplJP{}
	} else {
		d = &ImplUS{}
	}
	d.Do()
}

func Override() {
	d := &ImplCN{}
	d = &ImplUS{}
	d.Do()
}

func Uninitialized(cond bool) {
	var d Doer
	if cond {
		d = &ImplCN{}
	}
	d.Do()
}

func TwoVarsDo() {
	d := &ImplCN{}
	other := &ImplJP{}
	other.Do()
	d.Do()
}
`

func seedSceneImplRepo(t *testing.T) (*Actions, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(sceneImplSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:Pick", Kind: domain.KindFunction, Name: "Pick", FilePath: "main.go", LineStart: 17},
		{ID: "symbol:go:example.com/m:Override", Kind: domain.KindFunction, Name: "Override", FilePath: "main.go", LineStart: 29},
		{ID: "symbol:go:example.com/m:Uninitialized", Kind: domain.KindFunction, Name: "Uninitialized", FilePath: "main.go", LineStart: 35},
		{ID: "symbol:go:example.com/m:TwoVarsDo", Kind: domain.KindFunction, Name: "TwoVarsDo", FilePath: "main.go", LineStart: 43},
		{ID: "symbol:go:example.com/m:Doer", Kind: domain.KindInterface, Name: "Doer"},
		{ID: "symbol:go:example.com/m:(Doer).Do", Kind: domain.KindMethod, Name: "(Doer).Do"},
		{ID: "symbol:go:example.com/m:ImplCN", Kind: domain.KindStruct, Name: "ImplCN"},
		{ID: "symbol:go:example.com/m:(ImplCN).Do", Kind: domain.KindMethod, Name: "(ImplCN).Do", FilePath: "main.go", LineStart: 7},
		{ID: "symbol:go:example.com/m:ImplUS", Kind: domain.KindStruct, Name: "ImplUS"},
		{ID: "symbol:go:example.com/m:(ImplUS).Do", Kind: domain.KindMethod, Name: "(ImplUS).Do", FilePath: "main.go", LineStart: 11},
		{ID: "symbol:go:example.com/m:ImplJP", Kind: domain.KindStruct, Name: "ImplJP"},
		{ID: "symbol:go:example.com/m:(ImplJP).Do", Kind: domain.KindMethod, Name: "(ImplJP).Do", FilePath: "main.go", LineStart: 15},
	}
	callAt := func(caller string, line int) *domain.Fact {
		return &domain.Fact{SourceID: domain.CanonicalID("symbol:go:example.com/m:" + caller),
			TargetID: "symbol:go:example.com/m:(Doer).Do", Kind: domain.FactCalls, Confidence: 0.8,
			Metadata: map[string]any{"line_num": float64(line)}}
	}
	facts := []*domain.Fact{
		callAt("Pick", 26), callAt("Override", 32), callAt("Uninitialized", 40),
		callAt("TwoVarsDo", 46), callAt("TwoVarsDo", 47),
		{SourceID: "symbol:go:example.com/m:Doer", TargetID: "symbol:go:example.com/m:ImplCN",
			Kind: domain.FactImplements, Confidence: 1.0},
		{SourceID: "symbol:go:example.com/m:Doer", TargetID: "symbol:go:example.com/m:ImplUS",
			Kind: domain.FactImplements, Confidence: 1.0},
		{SourceID: "symbol:go:example.com/m:Doer", TargetID: "symbol:go:example.com/m:ImplJP",
			Kind: domain.FactImplements, Confidence: 1.0},
	}
	if _, err := r.SaveBatchStats(nodes, facts, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(&domain.BuildMeta{BuildID: "impl2", ToolName: "test", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	return New(r), dir
}

func ifaceCallNode(t *testing.T, root *CodeSeqNode) *CodeSeqNode {
	t.Helper()
	for _, n := range root.Nodes {
		if n.Kind == "call" && n.Label == "d.Do" {
			return n
		}
	}
	for _, n := range root.Nodes {
		if n.Kind == "call" {
			return n
		}
	}
	t.Fatalf("应有接口调用节点: %+v", root.Nodes)
	return nil
}

// A1：US 场景走 else 分支 → ImplUS，记录取反依据；CN 场景 → ImplCN。
func TestSceneImplElseBranch(t *testing.T) {
	acts, dir := seedSceneImplRepo(t)
	for _, tc := range []struct{ scene, want string }{
		{"platform=CN", "m.ImplCN"},
		{"platform=US", "m.ImplUS"},
	} {
		root, err := acts.CodeSequence(CodeSequenceRequest{Target: "symbol:go:example.com/m:Pick",
			RepoAbs: dir, Depth: 2, Scene: NewScene([]string{tc.scene})})
		if err != nil || root == nil {
			t.Fatalf("%s: CodeSequence %v root=%v", tc.scene, err, root)
		}
		n := ifaceCallNode(t, root)
		if n.ImplType != tc.want {
			t.Errorf("%s 应选 %s，实为 %q", tc.scene, tc.want, n.ImplType)
		}
		if n.Evidence == nil || !strings.HasPrefix(n.Evidence.Reason, "scene-guard:") {
			t.Errorf("%s 应记录守卫依据: %+v", tc.scene, n.Evidence)
		}
		if n.Evidence != nil && n.Evidence.Ambiguous {
			t.Errorf("%s 已唯一确定，不应 ambiguous: %+v", tc.scene, n.Evidence)
		}
		if n.Evidence != nil && strings.Contains(n.Evidence.SymbolID, "ImplCN") && tc.scene == "platform=US" {
			t.Errorf("US 场景不得选中 ImplCN: %+v", n.Evidence)
		}
	}
}

// A2：无场景 → 候选集合 + ambiguous（不能把第一个候选表现成确定路径）。
func TestSceneImplNoSceneAmbiguous(t *testing.T) {
	acts, dir := seedSceneImplRepo(t)
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "symbol:go:example.com/m:Pick",
		RepoAbs: dir, Depth: 2})
	if err != nil || root == nil {
		t.Fatalf("CodeSequence %v root=%v", err, root)
	}
	n := ifaceCallNode(t, root)
	if n.Evidence == nil || !n.Evidence.Ambiguous || len(n.Evidence.Candidates) < 3 {
		t.Errorf("无场景应保留全部候选并标 ambiguous: %+v", n.Evidence)
	}
	if n.Evidence != nil && strings.HasPrefix(n.Evidence.Reason, "scene-guard:") {
		t.Errorf("无场景不得给出场景确定结论: %+v", n.Evidence)
	}
}

// A3：无条件后续覆盖按到达定义生效；未初始化路径不给错误确定性。
func TestSceneImplOverrideAndUninitialized(t *testing.T) {
	acts, dir := seedSceneImplRepo(t)
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "symbol:go:example.com/m:Override",
		RepoAbs: dir, Depth: 2})
	if err != nil || root == nil {
		t.Fatalf("Override CodeSequence %v root=%v", err, root)
	}
	n := ifaceCallNode(t, root)
	if n.ImplType != "m.ImplUS" {
		t.Errorf("后续无条件覆盖应选 ImplUS，实为 %q", n.ImplType)
	}
	if n.Evidence != nil && strings.HasPrefix(n.Evidence.Reason, "scene-guard:") {
		t.Errorf("无场景的到达定义不应伪装成场景守卫: %+v", n.Evidence)
	}

	root, err = acts.CodeSequence(CodeSequenceRequest{Target: "symbol:go:example.com/m:Uninitialized",
		RepoAbs: dir, Depth: 2})
	if err != nil || root == nil {
		t.Fatalf("Uninitialized CodeSequence %v root=%v", err, root)
	}
	n = ifaceCallNode(t, root)
	if n.Evidence == nil || !n.Evidence.Ambiguous {
		t.Errorf("未初始化/条件赋值应保留候选 + ambiguous: %+v", n.Evidence)
	}
}

// TestSceneImplTwoVarsReachingPerObject：同一函数里两个接口变量互不串
// （旧实现把全部赋值并进一个 defs，导致两个调用点都拿到最后赋值的实现）。
func TestSceneImplTwoVarsReachingPerObject(t *testing.T) {
	acts, dir := seedSceneImplRepo(t)
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/m:TwoVarsDo", RepoAbs: dir, Depth: 3})
	if err != nil {
		t.Fatalf("CodeSequence: %v", err)
	}
	got := map[string]string{}
	for _, n := range root.Nodes {
		if n.Kind == "call" {
			got[n.Label] = n.ImplType
		}
	}
	if got["d.Do"] != "m.ImplCN" {
		t.Errorf("d.Do 应具体化为 m.ImplCN，got %q（%v）", got["d.Do"], got)
	}
	if got["other.Do"] != "m.ImplJP" {
		t.Errorf("other.Do 应具体化为 m.ImplJP，got %q（%v）", got["other.Do"], got)
	}
}

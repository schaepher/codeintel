package action

// todo22 P1 测试：场景判定的**跨包常量**（otherpkg.Const）——按文件 import
// 别名定位包目录，解析其 const 声明后参与比较；解析不到保持 unknown（不
// 按名称猜值）。覆盖默认 import、别名 import、内建转换与缺失常量反例。

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

const sceneConstMain = `package m

import "example.com/x/status"

func OnlyA() {}

func OnlyB() {}

func Route() {
	if status.Source == status.SourceB {
		OnlyB()
	} else {
		OnlyA()
	}
	if int32(status.Source) == int32(status.SourceB) {
		OnlyB()
	}
}

func RouteMissing() {
	if status.Source == status.NotDeclared {
		OnlyB()
	} else {
		OnlyA()
	}
}
`

const sceneConstAlias = `package m

import st "example.com/x/status"

func RouteAlias() {
	if st.Source == st.SourceA {
		OnlyA()
	} else {
		OnlyB()
	}
}
`

const sceneConstPkg = `package status

const (
	SourceA = 7
	SourceB = 19
)
`

// seedSceneConstRepo：两个包（m + status）的真实源码 + 索引节点。
func seedSceneConstRepo(t *testing.T) (*Actions, string) {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, body string) {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/x\n\ngo 1.21\n")
	write("main.go", sceneConstMain)
	write("alias.go", sceneConstAlias)
	write("status/status.go", sceneConstPkg)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/x:Route", Kind: domain.KindFunction, Name: "Route", FilePath: "main.go", LineStart: 9},
		{ID: "symbol:go:example.com/x:RouteMissing", Kind: domain.KindFunction, Name: "RouteMissing", FilePath: "main.go", LineStart: 21},
		{ID: "symbol:go:example.com/x:RouteAlias", Kind: domain.KindFunction, Name: "RouteAlias", FilePath: "alias.go", LineStart: 5},
		{ID: "symbol:go:example.com/x:OnlyA", Kind: domain.KindFunction, Name: "OnlyA", FilePath: "main.go", LineStart: 5},
		{ID: "symbol:go:example.com/x:OnlyB", Kind: domain.KindFunction, Name: "OnlyB", FilePath: "main.go", LineStart: 7},
	}
	if _, err := r.SaveBatchStats(nodes, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(&domain.BuildMeta{BuildID: "scenec1", ToolName: "fixture", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	return New(r), dir
}

func sceneFitsOf(t *testing.T, root *CodeSeqNode) []SceneFit {
	t.Helper()
	var out []SceneFit
	var walk func(nodes []*CodeSeqNode)
	walk = func(nodes []*CodeSeqNode) {
		for _, n := range nodes {
			if n.Kind == "branch" && n.Scene != nil {
				out = append(out, *n.Scene)
			}
			walk(n.Nodes)
			walk(n.Else)
		}
	}
	walk(root.Nodes)
	return out
}

func TestSceneCrossPackageConst(t *testing.T) {
	acts, dir := seedSceneConstRepo(t)
	cases := []struct {
		name   string
		entry  string
		scene  []string
		status []string
	}{
		{"默认 import / Source=19", "symbol:go:example.com/x:Route", []string{"Source=19"}, []string{SceneTaken, SceneTaken}},
		{"默认 import / Source=7", "symbol:go:example.com/x:Route", []string{"Source=7"}, []string{SceneExcluded, SceneExcluded}},
		{"别名 import / Source=19", "symbol:go:example.com/x:RouteAlias", []string{"Source=19"}, []string{SceneExcluded}},
		{"别名 import / Source=7", "symbol:go:example.com/x:RouteAlias", []string{"Source=7"}, []string{SceneTaken}},
		{"未声明常量保持 unknown", "symbol:go:example.com/x:RouteMissing", []string{"Source=7"}, []string{SceneUnknown}},
		{"无场景保持 unknown", "symbol:go:example.com/x:Route", nil, []string{SceneUnknown, SceneUnknown}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := CodeSequenceRequest{Target: c.entry, RepoAbs: dir, Depth: 3}
			if len(c.scene) > 0 {
				req.Scene = NewScene(c.scene)
			}
			root, err := acts.CodeSequence(req)
			if err != nil {
				t.Fatalf("CodeSequence: %v", err)
			}
			fits := sceneFitsOf(t, root)
			if len(fits) != len(c.status) {
				t.Fatalf("分支数 %d，期望 %d: %+v", len(fits), len(c.status), fits)
			}
			for i, want := range c.status {
				if fits[i].Status != want {
					t.Errorf("分支 %d（%s）→ %s，期望 %s（reason=%s literal=%q expected=%q）",
						i, fits[i].Field, fits[i].Status, want, fits[i].Reason, fits[i].Literal, fits[i].Expected)
				}
			}
		})
	}
}

// TestSceneCrossPackageConstRejectsNonConst：selector 不是常量（本地变量 /
// 包外未声明）时不得当成常量值，条件保持 unknown。
func TestSceneCrossPackageConstRejectsNonConst(t *testing.T) {
	tbl := sceneConstsMap{"x": "1"}
	if _, ok := sceneConstValue(tbl, "status.NotDeclared"); ok {
		t.Errorf("纯 map 表不应解析未登记名字")
	}
	if v, ok := sceneConstValue(tbl, "x"); !ok || v != "1" {
		t.Errorf("本地常量应命中: %q %v", v, ok)
	}
	if _, ok := sceneConstValue(nil, "x"); ok {
		t.Errorf("nil 表应返回未命中")
	}
}

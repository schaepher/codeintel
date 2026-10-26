package action

// todo11-4：effects 路径级摘要验收。
//   - 互斥分支的返回值与副作用分别展示（各自成路径）
//   - 提前返回路径不包含后续写入
//   - 区分"写入前失败"、"主写入后失败（部分成功）"
//   - 场景约束按**路径可达性**生效（不再按字段名过滤写入）

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// TestEffectsPathsExclusiveBranches：三条路径各自独立（写入前失败 / 只写主数据 /
// 主数据+追踪都写）。
func TestEffectsPathsExclusiveBranches(t *testing.T) {
	dir, acts := indexTodo10Fixture(t, "partial_write")
	res, err := acts.Effects(EffectsRequest{
		Target: "symbol:go:example.com/fx/partial_write:Commit", RepoAbs: dir, Depth: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Paths) < 3 {
		t.Fatalf("应有 ≥3 条路径（校验失败/主写失败/部分成功），实为 %d: %+v", len(res.Paths), res.Paths)
	}
	var preWriteFail, saveFail, partial *EffectPath
	for i := range res.Paths {
		p := &res.Paths[i]
		switch len(p.Writes) {
		case 0:
			preWriteFail = p
		case 1:
			saveFail = p
		default:
			partial = p
		}
	}
	if preWriteFail == nil || saveFail == nil || partial == nil {
		t.Fatalf("应按写入数区分三条路径: %+v", res.Paths)
	}
	// 写入前失败：没有任何写入，返回 err
	if preWriteFail.Result == nil || len(preWriteFail.Result.Returns) == 0 {
		t.Errorf("写入前失败路径应带自己的 return: %+v", preWriteFail)
	}
	// 主写入失败：只含主数据写入（不含后续 TraceID 写入）
	if !strings.Contains(saveFail.Writes[0].Field, "State") {
		t.Errorf("主写入失败路径应只含 State 写入: %+v", saveFail.Writes)
	}
	for _, w := range saveFail.Writes {
		if strings.Contains(w.Field, "TraceID") {
			t.Errorf("提前返回路径不得包含后续写入（TraceID）: %+v", saveFail.Writes)
		}
	}
	// 部分成功：主写入 + 追踪写入都在，返回表达式是 Trace(...)
	if len(partial.Writes) < 2 {
		t.Errorf("部分成功路径应含两个写入: %+v", partial.Writes)
	}
	if partial.Result == nil || !strings.Contains(strings.Join(partial.Result.Returns, ","), "Trace(") {
		t.Errorf("部分成功路径的返回应来自 Trace（追踪失败）: %+v", partial.Result)
	}
	// 函数级汇总保留（兼容）
	if len(res.DBWrites) < 2 {
		t.Errorf("函数级 db_writes 应保留全部写点: %+v", res.DBWrites)
	}
}

// TestEffectsPathsSceneReachability：场景按路径可达性生效——被排除分支
// 的路径标 reachable=false + 依据；**不按字段名过滤写入**。
func TestEffectsPathsSceneReachability(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `package m

type L struct{ State string }

func Route(region string, l *L) {
	if region == "CN" {
		l.State = "cn"
	} else {
		l.State = "us"
	}
}
`
	if err := os.WriteFile(filepath.Join(dir, "f.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:Route", Kind: domain.KindFunction, Name: "Route", FilePath: "f.go", LineStart: 5},
	}, nil, []*domain.FunctionFieldSummary{
		{FunctionID: "symbol:go:example.com/m:Route", AccessKind: domain.SummaryDirectWrite,
			FieldPath: "example.com/m.L.State", LineStart: 7},
		{FunctionID: "symbol:go:example.com/m:Route", AccessKind: domain.SummaryDirectWrite,
			FieldPath: "example.com/m.L.State", LineStart: 9},
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(&domain.BuildMeta{BuildID: "sc1", ToolName: "test", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	acts := New(r)

	// 无场景：两条路径都可达
	plain, err := acts.Effects(EffectsRequest{Target: "symbol:go:example.com/m:Route", RepoAbs: dir})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range plain.Paths {
		if !p.Reachable {
			t.Errorf("无场景时路径应可达: %+v", p)
		}
	}
	// region=CN：else 路径不可达（带依据），写入仍保留在各自路径上
	cn, err := acts.Effects(EffectsRequest{Target: "symbol:go:example.com/m:Route", RepoAbs: dir,
		Scene: NewScene([]string{"region=CN"})})
	if err != nil {
		t.Fatal(err)
	}
	var unreachable, reachable int
	for _, p := range cn.Paths {
		if p.Reachable {
			reachable++
			continue
		}
		unreachable++
		if !strings.Contains(strings.Join(p.Notes, " "), "场景排除") {
			t.Errorf("不可达路径应带场景排除依据: %+v", p)
		}
	}
	if unreachable == 0 || reachable == 0 {
		t.Fatalf("场景应只排除一侧（reachable=%d unreachable=%d）: %+v", reachable, unreachable, cn.Paths)
	}
	// 场景不得按字段名过滤写入：函数级摘要与无场景时一致；两条路径各自
	// 仍保留自己的写点（含被排除那条——它是该分支的事实，只是不可达）
	if len(cn.DBWrites) != len(plain.DBWrites) {
		t.Errorf("场景不得过滤函数级写入（plain=%d scene=%d）", len(plain.DBWrites), len(cn.DBWrites))
	}
	var withWrites int
	for _, p := range cn.Paths {
		if len(p.Writes) > 0 {
			withWrites++
		}
	}
	if withWrites < 2 {
		t.Errorf("两条分支路径应各自保留写点（实为 %d 条路径有写点）: %+v", withWrites, cn.Paths)
	}
}

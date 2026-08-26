package orchestrator

// P0-2 测试：dispatch_to 边增量丢失根治——改 impl 包时注册点包
// （含 MakeInterface 注册与动态调用的容器包）未 Load，emitDispatches
// 收不到注册点 → 依赖它的 dispatch_to 边全部丢失（用户实测改 impl
// 包丢 ~15 条）。修复：全量时持久化 dispatch 相关包，增量时补 Load。

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// TestIncrementalDispatchEdgesSurviveImplChange：端到端——容器包
// （接口 + MakeInterface 注册点 + 动态调用）与 impl 包（实现）。
// ① 全量构建：dispatch_to 边存在（register 0.9）；meta 记录
// dispatch 相关包（含容器包）。② 只改 impl 包走增量：dispatch_to
// 边必须保留（容器包被补 Load 重新扫描）。
func TestIncrementalDispatchEdgesSurviveImplChange(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/inc\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "container", "container.go"), `package container

import "example.com/inc/impl"

type Manager interface {
	Handle()
}

// 注册点：具体实现 → 接口（MakeInterface）
func Wire() Manager {
	return &impl.ManagerImpl{}
}

// 动态接口方法调用（容器包内）
func Use() {
	m := Wire()
	m.Handle()
}
`)
	writeFile(t, filepath.Join(dir, "impl", "impl.go"), `package impl

type ManagerImpl struct{}

func (m *ManagerImpl) Handle() {}
`)

	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	orch := New(&domain.Repository{Path: dir, Module: "example.com/inc", Modules: []string{"example.com/inc"}, ModuleDirs: []string{"."}}, db)
	repo := orch.GetRepo()

	countDispatch := func() int {
		rows, err := repo.Query(`SELECT count(*) FROM edges WHERE kind = 'dispatch_to'`)
		if err != nil {
			t.Fatalf("query dispatch_to: %v", err)
		}
		defer rows.Close()
		var n int
		if !rows.Next() {
			t.Fatal("no row")
		}
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan: %v", err)
		}
		return n
	}

	// ① 全量构建
	res, err := orch.FullBuild(context.Background())
	if err != nil {
		t.Fatalf("FullBuild: %v", err)
	}
	if res.Status == domain.BuildFailed {
		t.Fatalf("build failed: %+v", res.Adapter)
	}
	if n := countDispatch(); n != 1 {
		t.Fatalf("全量后 dispatch_to 边 = %d, want 1（Manager → managerImpl.Handle）", n)
	}
	// 注册点佐证（register 0.9）与 source/target 正确
	// 注意：SetMaxOpenConns(1) 单连接——rows 必须用完立即 Close，
	// 否则后续查询（GetLatest）死锁等连接
	rows, err := repo.Query(`SELECT source_id, target_id, confidence FROM edges WHERE kind = 'dispatch_to'`)
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		rows.Close()
		t.Fatal("no dispatch edge")
	}
	var src, tgt string
	var conf float64
	if err := rows.Scan(&src, &tgt, &conf); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	if src != "symbol:go:example.com/inc/container:Manager" {
		t.Errorf("source = %s; want container:Manager", src)
	}
	if tgt != "symbol:go:example.com/inc/impl:(ManagerImpl).Handle" {
		t.Errorf("target = %s; want impl:(ManagerImpl).Handle", tgt)
	}
	if conf < 0.8 {
		t.Errorf("confidence = %v; want register 0.9", conf)
	}
	// meta 记录 dispatch 相关包（容器包——注册点所在）
	meta, err := repo.GetLatest()
	if err != nil {
		t.Fatalf("GetLatest: %v", err)
	}
	found := false
	for _, p := range meta.DispatchPkgs {
		if p == "example.com/inc/container" {
			found = true
		}
	}
	if !found {
		t.Errorf("meta.DispatchPkgs = %v; want 含容器包 example.com/inc/container", meta.DispatchPkgs)
	}

	// ② 只改 impl 包 → 增量构建（容器包未变更——修复前不被 Load，
	// dispatch_to 边丢失）
	writeFile(t, filepath.Join(dir, "impl", "impl.go"), `package impl

type ManagerImpl struct{}

func (m *ManagerImpl) Handle() {}

func (m *ManagerImpl) Extra() {}
`)
	if _, err := orch.IncrementalBuild(context.Background(), []string{"impl/impl.go"}); err != nil {
		t.Fatalf("IncrementalBuild: %v", err)
	}
	if n := countDispatch(); n != 1 {
		t.Errorf("增量后 dispatch_to 边 = %d, want 1（注册点包补 Load 后保留）", n)
	}
}

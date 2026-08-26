package orchestrator

// R84 测试：按包增量分析——变更文件 → 变更包 patterns（只 Load 变更
// 包，其他包复用库中数据）；go.mod/无法定位 → 全量降级。

import (
	"context"
	"path/filepath"
	"sort"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/ssa"
	"golang.org/x/tools/go/packages"
)

// TestChangedPackagePatterns：变更文件 → 包 patterns（相对 module 目录）；
// go.mod 变更 → 全量降级；包目录删除 → 跳过。
func TestChangedPackagePatterns(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/inc\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "main.go"), "package main\nfunc main() {}\n")
	writeFile(t, filepath.Join(dir, "sub", "a.go"), "package a\n")
	o := &Orchestrator{Repo: &domain.Repository{Path: dir, Module: "example.com/inc",
		Modules: []string{"example.com/inc"}, ModuleDirs: []string{"."}}}

	// 单文件 → 单包 pattern
	pats, full := o.changedPackagePatterns([]string{"sub/a.go"})
	if full || len(pats["."]) != 1 || pats["."][0] != "./sub" {
		t.Errorf("单文件 patterns = %v, full=%v; want [./sub]", pats, full)
	}
	// 根目录文件 → "./"
	pats, _ = o.changedPackagePatterns([]string{"main.go"})
	if len(pats["."]) != 1 || pats["."][0] != "./" {
		t.Errorf("根文件 patterns = %v; want [./]", pats)
	}
	// go.mod 变更 → 全量
	if _, full := o.changedPackagePatterns([]string{"go.mod"}); !full {
		t.Error("go.mod 变更应全量降级")
	}
	// 包目录已删除 → 跳过
	if pats, _ := o.changedPackagePatterns([]string{"gone/b.go"}); len(pats) != 0 {
		t.Errorf("已删除包应跳过: %v", pats)
	}
	// 多文件去重
	pats, _ = o.changedPackagePatterns([]string{"sub/a.go", "sub/b.go"})
	if len(pats["."]) != 1 {
		t.Errorf("同包多文件应去重: %v", pats)
	}
}

// TestChangedPackagePatternsMultiModule：多 module monorepo——子 module
// 变更文件 → 相对子 module 的 pattern。
func TestChangedPackagePatternsMultiModule(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/root\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "submod", "go.mod"), "module example.com/sub\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "submod", "pkg", "x.go"), "package x\n")
	o := &Orchestrator{Repo: &domain.Repository{Path: dir, Module: "example.com/root",
		Modules: []string{"example.com/root", "example.com/sub"}, ModuleDirs: []string{".", "submod"}}}
	pats, full := o.changedPackagePatterns([]string{"submod/pkg/x.go"})
	if full || len(pats["submod"]) != 1 || pats["submod"][0] != "./pkg" {
		t.Errorf("子 module patterns = %v, full=%v; want {submod: [./pkg]}", pats, full)
	}
}

// recordAdapter 记录收到的包路径（验证按包 Load 范围）。
type recordAdapter struct {
	pkgs []string
}

func (r *recordAdapter) Name() string { return "record" }

func (r *recordAdapter) Index(_ context.Context, _ *domain.Repository, pkgs []*packages.Package, _ domain.EmitFunc) error {
	for _, p := range pkgs {
		r.pkgs = append(r.pkgs, p.PkgPath)
	}
	return nil
}

// TestIncrementalBuildLoadsOnlyChangedPackages：增量构建只 Load 变更
// 包（record adapter 收到的包 = 变更包；未变更包跳过分析但数据保留；
// rich adapter 产出变更包数据被写入）。
func TestIncrementalBuildLoadsOnlyChangedPackages(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/inc\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "main.go"), "package main\n\nfunc main() {}\n")
	writeFile(t, filepath.Join(dir, "sub", "a.go"), "package a\n\nfunc A() {}\n")
	rec := &recordAdapter{}
	funcID := domain.CanonicalID("symbol:go:example.com/inc:main")
	rich := &richAdapter{items: []domain.Item{
		{Node: &domain.CodeEntity{ID: funcID, Kind: domain.KindFunction, Name: "main", FilePath: "main.go", LineStart: 3}},
	}}
	o, repo := newTestOrchestrator(t, []domain.IndexerPort{rec, rich})
	o.Repo = &domain.Repository{Path: dir, Module: "example.com/inc", Modules: []string{"example.com/inc"}, ModuleDirs: []string{"."}}
	if err := ssa.SaveAnalyzerMarker(dir); err != nil {
		t.Fatalf("save analyzer marker: %v", err)
	}
	// 预置：未变更包 sub 的节点（模拟基底索引）
	otherID := domain.CanonicalID("symbol:go:example.com/inc/sub:A")
	if _, err := repo.SaveBatchStats([]*domain.CodeEntity{
		{ID: otherID, Kind: domain.KindFunction, Name: "A", FilePath: "sub/a.go", LineStart: 3},
	}, nil, nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	res, err := o.IncrementalBuild(context.Background(), []string{"main.go"})
	if err != nil {
		t.Fatalf("IncrementalBuild: %v", err)
	}
	if res.Status == domain.BuildFailed {
		t.Fatalf("build failed: %+v", res.Adapter)
	}
	// ① adapter 只收到变更包（main 包——sub 包未 Load）
	if len(rec.pkgs) != 1 || rec.pkgs[0] != "example.com/inc" {
		t.Errorf("adapter 收到包 = %v; want 仅 [example.com/inc]（sub 包跳过分析）", rec.pkgs)
	}
	// ② 未变更包 sub 的数据原样保留
	if n, err := repo.GetSymbol(otherID); err != nil || n.Name != "A" {
		t.Errorf("sub 包节点应保留: %+v, %v", n, err)
	}
	// ③ 变更包 main 重新产出（rich adapter 产出被写入）
	if n, err := repo.GetSymbol(funcID); err != nil || n.Name != "main" {
		t.Errorf("main 节点应重新产出: %+v, %v", n, err)
	}
}

// TestIncrementalBuildModuleSkip：多 module——某 module 无变更时该
// module 完全跳过 Load。
func TestIncrementalBuildModuleSkip(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/root\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "main.go"), "package main\n\nfunc main() {}\n")
	writeFile(t, filepath.Join(dir, "submod", "go.mod"), "module example.com/sub\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "submod", "pkg", "x.go"), "package x\n\nfunc X() {}\n")
	rec := &recordAdapter{}
	o, _ := newTestOrchestrator(t, []domain.IndexerPort{rec})
	o.Repo = &domain.Repository{Path: dir, Module: "example.com/root",
		Modules: []string{"example.com/root", "example.com/sub"}, ModuleDirs: []string{".", "submod"}}
	if err := ssa.SaveAnalyzerMarker(dir); err != nil {
		t.Fatalf("save analyzer marker: %v", err)
	}
	if _, err := o.IncrementalBuild(context.Background(), []string{"main.go"}); err != nil {
		t.Fatalf("IncrementalBuild: %v", err)
	}
	sort.Strings(rec.pkgs)
	if len(rec.pkgs) != 1 || rec.pkgs[0] != "example.com/root" {
		t.Errorf("adapter 收到包 = %v; want 仅根 module（submod 无变更跳过）", rec.pkgs)
	}
}

// TestChangedPackagePatternsGoWork：go.work 变更 → 全量降级。
func TestChangedPackagePatternsGoWork(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/inc\n\ngo 1.21\n")
	o := &Orchestrator{Repo: &domain.Repository{Path: dir, Module: "example.com/inc",
		Modules: []string{"example.com/inc"}, ModuleDirs: []string{"."}}}
	if _, full := o.changedPackagePatterns([]string{"go.work"}); !full {
		t.Error("go.work 变更应全量降级")
	}
}

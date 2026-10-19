package ssa

import (
	"errors"
	"go/types"
	"reflect"
	"sort"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"golang.org/x/tools/go/ssa"
)

// dispatchReuseFixture 含注册点 + 动态调用（多文件、同行注册位置）。
func dispatchReuseFixture() map[string]string {
	return map[string]string{
		"go.mod": moduleGoMod,
		"a.go": `package m

func regA() Iface { return &Impl{} }

type Iface interface{ M() }

type Impl struct{}

func (i Impl) M() {}
`,
		"b.go": `package m

func regB() Iface { return Impl{} }

func use(i Iface) { i.M() }
`,
	}
}

// allTypePkgs Program 的全部 *types.Package（implTypePool 构造用）。
func allTypePkgs(prog *ssa.Program) []*types.Package {
	var out []*types.Package
	for _, ap := range prog.AllPackages() {
		if ap != nil && ap.Pkg != nil {
			out = append(out, ap.Pkg)
		}
	}
	return out
}

// emitDispatchesOnce 跑一次派发发射，返回 (边集合, DispatchPkgs, err)。
func emitDispatchesOnce(t *testing.T, repo *domain.Repository, prog *ssa.Program, pool *implTypePool,
	s *funcSnapshot, regs dispatchReg, regPkgs []string, failEmit bool) (map[string]*domain.Fact, []string, error) {
	t.Helper()
	facts := map[string]*domain.Fact{}
	pkgs, err := emitDispatches(repo, prog, pool, s, regs, regPkgs, func(item domain.Item) error {
		if failEmit {
			return errors.New("emit fail")
		}
		if item.Fact != nil {
			k := string(item.Fact.SourceID) + "|" + string(item.Fact.TargetID) + "|" + string(item.Fact.Kind)
			facts[k] = item.Fact
		}
		return nil
	})
	return facts, pkgs, err
}

// TestEmitDispatchesReuseMatchesScan（todo7-1）：复用 init 阶段的注册结果
// 与重新扫描的完整派发产物（边/元数据）及 DispatchPkgs 完全一致。
func TestEmitDispatchesReuseMatchesScan(t *testing.T) {
	s, prog := testFuncSnapshotFilesAndProg(t, dispatchReuseFixture())
	repo := &domain.Repository{Module: "example.com/mtest", Modules: []string{"example.com/mtest", "example.com/other"}}
	pool := newImplTypePool(allTypePkgs(prog), repo.Modules)
	modFuncs := s.moduleFuncs(repo.Modules)
	regs, regPkgs := collectDispatchRegistrations(prog, modFuncs, repo.Modules, s, nil)

	scanFacts, scanPkgs, err := emitDispatchesOnce(t, repo, prog, pool, s, nil, nil, false)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	reuseFacts, reusePkgs, err := emitDispatchesOnce(t, repo, prog, pool, s, regs, regPkgs, false)
	if err != nil {
		t.Fatalf("reuse: %v", err)
	}
	if len(scanFacts) == 0 {
		t.Fatal("fixture 应产出 dispatch_to 边")
	}
	if !reflect.DeepEqual(scanFacts, reuseFacts) {
		t.Errorf("派发边不一致: scan=%d reuse=%d", len(scanFacts), len(reuseFacts))
	}
	sort.Strings(scanPkgs)
	sort.Strings(reusePkgs)
	if !reflect.DeepEqual(scanPkgs, reusePkgs) {
		t.Errorf("DispatchPkgs 不一致: scan=%v reuse=%v", scanPkgs, reusePkgs)
	}
	// 二次复用结果稳定
	again, againPkgs, err := emitDispatchesOnce(t, repo, prog, pool, s, regs, regPkgs, false)
	if err != nil || !reflect.DeepEqual(reuseFacts, again) {
		t.Errorf("二次复用不一致: err=%v", err)
	}
	sort.Strings(againPkgs)
	if !reflect.DeepEqual(reusePkgs, againPkgs) {
		t.Errorf("二次复用 DispatchPkgs 不一致")
	}
}

// TestEmitDispatchesInitializedEmptyNoRescan（todo7-1）：已初始化的空注册表
// （非 nil 空 map）不得再触发扫描；nil（未初始化）才回退扫描。
func TestEmitDispatchesInitializedEmptyNoRescan(t *testing.T) {
	s, prog := testFuncSnapshotFilesAndProg(t, dispatchReuseFixture())
	repo := &domain.Repository{Module: "example.com/mtest", Modules: []string{"example.com/mtest"}}
	pool := newImplTypePool(allTypePkgs(prog), repo.Modules)

	emptyFacts, emptyPkgs, err := emitDispatchesOnce(t, repo, prog, pool, s, dispatchReg{}, nil, false)
	if err != nil {
		t.Fatalf("empty regs: %v", err)
	}
	// 枚举兜底仍可能产边（origin=enum）；不得出现 register 边——若重新
	// 扫描注册点就会找到注册（origin=register, 0.9）。
	for k, f := range emptyFacts {
		if v, _ := f.Metadata["origin"].(string); v == "register" {
			t.Errorf("已初始化空注册表不应有 register 边（说明重复扫描了）: %s", k)
		}
	}
	// 动态调用包仍应保留在 DispatchPkgs（用于后续增量补 Load）
	if len(emptyPkgs) == 0 {
		t.Error("空注册表时动态调用包仍应保留")
	}
	// nil = 未初始化 → 回退扫描 → 有边
	scanFacts, _, err := emitDispatchesOnce(t, repo, prog, pool, s, nil, nil, false)
	if err != nil {
		t.Fatalf("nil regs: %v", err)
	}
	hasRegister := false
	for _, f := range scanFacts {
		if v, _ := f.Metadata["origin"].(string); v == "register" {
			hasRegister = true
		}
	}
	if !hasRegister {
		t.Error("nil（未初始化）应回退扫描并产出 register 边")
	}
}

// TestEmitDispatchesOnlyRegistrations（todo7-1）：只有注册、没有动态调用时
// 仍保留注册包，且无边。
func TestEmitDispatchesOnlyRegistrations(t *testing.T) {
	files := map[string]string{
		"go.mod":  moduleGoMod,
		"main.go": "package m\n\ntype Iface interface{ M() }\n\ntype Impl struct{}\n\nfunc (i Impl) M() {}\n\nfunc reg() Iface { return &Impl{} }\n",
	}
	s, prog := testFuncSnapshotFilesAndProg(t, files)
	repo := &domain.Repository{Module: "example.com/mtest", Modules: []string{"example.com/mtest"}}
	pool := newImplTypePool(allTypePkgs(prog), repo.Modules)
	regs, regPkgs := collectDispatchRegistrations(prog, s.moduleFuncs(repo.Modules), repo.Modules, s, nil)
	facts, pkgs, err := emitDispatchesOnce(t, repo, prog, pool, s, regs, regPkgs, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 0 {
		t.Errorf("无动态调用不应有 dispatch_to 边: %d", len(facts))
	}
	if len(pkgs) == 0 {
		t.Error("只有注册时仍应保留注册包")
	}
}

// TestEmitDispatchesOutputError（todo7-1）：输出错误传播。
func TestEmitDispatchesOutputError(t *testing.T) {
	s, prog := testFuncSnapshotFilesAndProg(t, dispatchReuseFixture())
	repo := &domain.Repository{Module: "example.com/mtest", Modules: []string{"example.com/mtest"}}
	pool := newImplTypePool(allTypePkgs(prog), repo.Modules)
	regs, regPkgs := collectDispatchRegistrations(prog, s.moduleFuncs(repo.Modules), repo.Modules, s, nil)
	_, _, err := emitDispatchesOnce(t, repo, prog, pool, s, regs, regPkgs, true)
	if err == nil || err.Error() != "emit fail" {
		t.Errorf("输出错误应传播, got %v", err)
	}
}

// TestDispatchBestSiteReusedPerMethod（todo8-6）：同一动态类型的多个方法
// 复用预计算的最佳注册位置（register_file/register_site 一致）。
func TestDispatchBestSiteReusedPerMethod(t *testing.T) {
	files := map[string]string{
		"go.mod": moduleGoMod,
		"a.go": `package m

func regA() Iface { return &Impl{} }

type Iface interface {
	M()
	N()
}

type Impl struct{}

func (i Impl) M() {}
func (i Impl) N() {}
`,
		"b.go": `package m

func regB() Iface { return Impl{} }

func use(i Iface) { i.M(); i.N() }
`,
	}
	s, prog, dir := testFuncSnapshotFilesProgDir(t, files)
	repo := &domain.Repository{Path: dir, Module: "example.com/mtest", Modules: []string{"example.com/mtest"}}
	pool := newImplTypePool(allTypePkgs(prog), repo.Modules)
	pool.SetFuncSnapshot(s)
	regs, regPkgs := collectDispatchRegistrations(prog, s.moduleFuncs(repo.Modules), repo.Modules, s, nil)
	facts, _, err := emitDispatchesOnce(t, repo, prog, pool, s, regs, regPkgs, false)
	if err != nil {
		t.Fatal(err)
	}
	got := 0
	for _, f := range facts {
		if v, _ := f.Metadata["origin"].(string); v != "register" {
			continue
		}
		got++
		if f.Metadata["register_file"] != "a.go" || f.Metadata["register_site"] != 3 {
			t.Errorf("%s 代表位置 = (%v,%v), want (a.go,3)", f.TargetID,
				f.Metadata["register_file"], f.Metadata["register_site"])
		}
	}
	if got != 2 {
		t.Errorf("register 边 = %d, want 2（M/N 各一条）", got)
	}
}

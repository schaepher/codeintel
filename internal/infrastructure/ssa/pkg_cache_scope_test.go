package ssa

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"golang.org/x/tools/go/packages"
)

var scopeFixtureFiles = map[string]string{
	"go.mod": moduleGoMod,
	"a/a.go": `package a

type A struct{ X int }

func UseA() int { v := &A{}; v.X = 1; return v.X }
`,
	"b/b.go": `package b

type B struct{ Y int }

func UseB() int { v := &B{}; v.Y = 2; return v.Y }
`,
}

func scopeFixture(t *testing.T) (string, []*packages.Package) {
	t.Helper()
	dir := t.TempDir()
	for path, content := range scopeFixtureFiles {
		writeFile(t, filepath.Join(dir, path), content)
	}
	pkgs, err := loadTestPackages(dir)
	if err != nil {
		t.Fatal(err)
	}
	return dir, pkgs
}

// TestAnalysisScopeOrderInvariant（todo9-3）：模块/包输入顺序不影响指纹，
// 且不修改调用方切片。
func TestAnalysisScopeOrderInvariant(t *testing.T) {
	_, pkgs := scopeFixture(t)
	mods := []string{"example.com/mtest/b", "example.com/mtest/a"}
	pkgsCopy := append([]*packages.Package(nil), pkgs...)
	modsCopy := append([]string(nil), mods...)

	s1, err := buildAnalysisScope(context.Background(), pkgs, mods)
	if err != nil {
		t.Fatal(err)
	}
	rev := append([]*packages.Package(nil), pkgs...)
	sort.Slice(rev, func(i, j int) bool { return rev[i].PkgPath > rev[j].PkgPath })
	revMods := []string{mods[1], mods[0]}
	s2, err := buildAnalysisScope(context.Background(), rev, revMods)
	if err != nil {
		t.Fatal(err)
	}
	if s1.fingerprint != s2.fingerprint {
		t.Fatalf("顺序变化改变了指纹: %s vs %s", s1.fingerprint, s2.fingerprint)
	}
	for i := range pkgs {
		if pkgs[i] != pkgsCopy[i] {
			t.Fatal("buildAnalysisScope 修改了 pkgs 切片顺序")
		}
	}
	for i := range mods {
		if mods[i] != modsCopy[i] {
			t.Fatal("buildAnalysisScope 修改了 modules 切片")
		}
	}
}

// TestAnalysisScopeSensitive：源码内容/语法数量/类型错误状态变化都改变指纹。
func TestAnalysisScopeSensitive(t *testing.T) {
	dir, pkgs := scopeFixture(t)
	mods := []string{"example.com/mtest"}
	base, err := buildAnalysisScope(context.Background(), pkgs, mods)
	if err != nil {
		t.Fatal(err)
	}
	// 源码内容
	writeFile(t, filepath.Join(dir, "a/a.go"), scopeFixtureFiles["a/a.go"]+"\n// changed\n")
	changed, err := buildAnalysisScope(context.Background(), pkgs, mods)
	if err != nil {
		t.Fatal(err)
	}
	if changed.fingerprint == base.fingerprint {
		t.Fatal("源码内容变化未改变范围指纹")
	}
	// 语法树数量（同一内容，Syntax 缺失）
	p := *pkgs[0]
	p.Syntax = nil
	noSyntax, err := buildAnalysisScope(context.Background(), []*packages.Package{&p, pkgs[1]}, mods)
	if err != nil {
		t.Fatal(err)
	}
	if noSyntax.fingerprint == base.fingerprint {
		t.Fatal("语法树数量变化未改变范围指纹")
	}
	// 类型错误状态
	pe := *pkgs[0]
	pe.Errors = append(pe.Errors, packages.Error{Msg: "type error"})
	pe.IllTyped = true
	withErr, err := buildAnalysisScope(context.Background(), []*packages.Package{&pe, pkgs[1]}, mods)
	if err != nil {
		t.Fatal(err)
	}
	if withErr.fingerprint == base.fingerprint {
		t.Fatal("类型错误状态变化未改变范围指纹")
	}
}

// TestAnalysisScopeReadFailure：源码读取失败返回错误（调用方禁用缓存）。
func TestAnalysisScopeReadFailure(t *testing.T) {
	pkg := &packages.Package{PkgPath: "example.com/broken", CompiledGoFiles: []string{"/nonexistent/x.go"}}
	if _, err := buildAnalysisScope(context.Background(), []*packages.Package{pkg}, nil); err == nil {
		t.Fatal("缺失文件应返回错误")
	}
}

// TestCombineScopeKey：范围不同 → 组合键不同；同输入稳定。
func TestCombineScopeKey(t *testing.T) {
	a := combineScopeKey("scope1", "pkgkey")
	if a != combineScopeKey("scope1", "pkgkey") {
		t.Fatal("同输入组合键不稳定")
	}
	if a == combineScopeKey("scope2", "pkgkey") || a == combineScopeKey("scope1", "pkgkey2") {
		t.Fatal("范围/包键变化应改变组合键")
	}
}

func indexPkgsCollectIDs(t *testing.T, dir string, pkgs []*packages.Package) []domain.CanonicalID {
	t.Helper()
	a := &Adapter{}
	a.SetWorkers(1)
	repo := &domain.Repository{Path: dir, Module: "example.com/mtest", Modules: []string{"example.com/mtest"}}
	var ids []domain.CanonicalID
	if err := a.Index(context.Background(), repo, pkgs, func(item domain.Item) error {
		if item.Node != nil {
			ids = append(ids, item.Node.ID)
		}
		return nil
	}); err != nil {
		t.Fatalf("Index: %v", err)
	}
	return ids
}

type cacheSnap struct {
	mod  int64
	data string
}

func snapshotCache(t *testing.T, dir string) map[string]cacheSnap {
	t.Helper()
	out := map[string]cacheSnap{}
	for _, f := range cacheFiles(t, dir) {
		st, err := os.Stat(f)
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		out[f] = cacheSnap{mod: st.ModTime().UnixNano(), data: string(data)}
	}
	return out
}

func idsEqual(a, b []domain.CanonicalID) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[domain.CanonicalID]int{}
	for _, id := range a {
		m[id]++
	}
	for _, id := range b {
		m[id]--
		if m[id] < 0 {
			return false
		}
	}
	return true
}

// TestPkgCacheScopeIsolation（todo9-3）：单包 → 整仓 → 整仓切换：单包与
// 整仓不共享缓存；整仓暖重放不重写；产物一致。
func TestPkgCacheScopeIsolation(t *testing.T) {
	dir, _ := scopeFixture(t)
	load := func() []*packages.Package {
		pkgs, err := loadTestPackages(dir)
		if err != nil {
			t.Fatal(err)
		}
		return pkgs
	}
	all := load()
	idsWhole1 := indexPkgsCollectIDs(t, dir, all)
	afterWhole1 := snapshotCache(t, dir)
	if len(afterWhole1) < 2 {
		t.Fatalf("整仓构建应写 >=2 个包缓存，got %d", len(afterWhole1))
	}
	pathA := pkgCachePath(dir, "example.com/mtest/a")
	pathB := pkgCachePath(dir, "example.com/mtest/b")

	// 单包分析（只加载 a）：范围不同 → a 的缓存键变化（必须重算并重写）
	var onlyA []*packages.Package
	for _, p := range load() {
		if p.PkgPath == "example.com/mtest/a" {
			onlyA = append(onlyA, p)
		}
	}
	if len(onlyA) != 1 {
		t.Fatalf("单包加载 got %d", len(onlyA))
	}
	indexPkgsCollectIDs(t, dir, onlyA)
	afterSingle := snapshotCache(t, dir)
	if afterSingle[pathA] == afterWhole1[pathA] {
		t.Fatal("单包分析命中了整仓缓存（范围未隔离）")
	}
	if afterSingle[pathB] != afterWhole1[pathB] {
		t.Fatal("单包分析不应改写未加载包 b 的缓存")
	}

	// 再整仓：a 的缓存现在是单包范围 → 必须 miss 重写；b 命中不变
	idsWhole2 := indexPkgsCollectIDs(t, dir, load())
	afterWhole2 := snapshotCache(t, dir)
	if afterWhole2[pathA] == afterSingle[pathA] {
		t.Fatal("整仓分析命中了单包缓存（范围未隔离）")
	}
	if afterWhole2[pathB] != afterSingle[pathB] {
		t.Fatal("整仓暖重放不应重写 b 的缓存")
	}
	if !idsEqual(idsWhole1, idsWhole2) {
		t.Fatal("单包/整仓切换后整仓产物不一致")
	}

	// 整仓暖重放：全部命中，不重写
	indexPkgsCollectIDs(t, dir, load())
	afterWhole3 := snapshotCache(t, dir)
	for f, want := range afterWhole2 {
		if afterWhole3[f] != want {
			t.Fatalf("整仓暖重放重写了缓存 %s", f)
		}
	}
}

// TestScopeFingerprintFailureDisablesCache（todo9-3）：指纹失败 → 警告、
// 禁用本次构建缓存、继续分析。
func TestScopeFingerprintFailureDisablesCache(t *testing.T) {
	dir, pkgs := scopeFixture(t)
	old := buildAnalysisScopeFn
	buildAnalysisScopeFn = func(context.Context, []*packages.Package, []string) (*analysisScope, error) {
		return nil, errors.New("scope boom")
	}
	defer func() { buildAnalysisScopeFn = old }()

	ids := indexPkgsCollectIDs(t, dir, pkgs)
	if len(ids) == 0 {
		t.Fatal("指纹失败后仍应完成实际分析")
	}
	if files := cacheFiles(t, dir); len(files) != 0 {
		t.Fatalf("指纹失败时不应写包缓存: %v", files)
	}
}

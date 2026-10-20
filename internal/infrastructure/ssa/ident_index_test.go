package ssa

import (
	"go/ast"
	"go/token"
	"path/filepath"
	"sync"
	"testing"

	"golang.org/x/tools/go/packages"
)

const identFixtureMain = `package m

type T struct{ A, B int }

func (t *T) Sum() int { return t.A + t.B }

func use() int {
	t := &T{}
	u := t.Sum()
	v := u + 1
	return v
}
`

const identFixtureSub = `package sub

func Add(a, b int) int { return a + b }
`

// identFixturePackages 建临时 module（两个包）并加载类型信息。
func identFixturePackages(t *testing.T) []*packages.Package {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), moduleGoMod)
	writeFile(t, filepath.Join(dir, "m.go"), identFixtureMain)
	writeFile(t, filepath.Join(dir, "sub", "sub.go"), identFixtureSub)
	pkgs, err := loadTestPackages(dir)
	if err != nil {
		t.Fatal(err)
	}
	return pkgs
}

// eagerIdents 旧实现：模块包全部 AST 的 Pos→标识符名。
func eagerIdents(pkgs []*packages.Package, modules []string) map[token.Pos]string {
	out := map[token.Pos]string{}
	for _, p := range pkgs {
		if !isInModule(p.PkgPath, modules) {
			continue
		}
		for _, f := range p.Syntax {
			ast.Inspect(f, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok {
					out[id.Pos()] = id.Name
				}
				return true
			})
		}
	}
	return out
}

// moduleFiles 模块包的全部语法文件（按 Pos 区间的 Base 排序不保证，仅测试用）。
func moduleFiles(pkgs []*packages.Package, modules []string) []*token.File {
	var out []*token.File
	for _, p := range pkgs {
		if !isInModule(p.PkgPath, modules) {
			continue
		}
		for _, f := range p.Syntax {
			if tf := p.Fset.File(f.Pos()); tf != nil {
				out = append(out, tf)
			}
		}
	}
	return out
}

// TestIdentIndexMatchesEager（todo10-3）：惰性按文件索引与旧全局索引对
// 全部标识符位置、文件内逐位置采样、NoPos 的命中/名称完全一致。
func TestIdentIndexMatchesEager(t *testing.T) {
	pkgs := identFixturePackages(t)
	mods := []string{"example.com/mtest"}
	want := eagerIdents(pkgs, mods)
	if len(want) == 0 {
		t.Fatal("fixture 未产生标识符")
	}
	ix := buildIdentIndex(pkgs, mods)
	if len(ix.byDecl) != 0 {
		t.Fatalf("索引不应预建声明表: %d", len(ix.byDecl))
	}
	for pos, name := range want {
		got, ok := ix.lookup(pos)
		if !ok || got != name {
			t.Fatalf("lookup(%d) = (%q,%v), want (%q,true)", pos, got, ok, name)
		}
	}
	if len(ix.byDecl) == 0 {
		t.Fatal("查询后应按文件建表")
	}
	// 文件内逐位置采样（覆盖非标识符位置：命中必须与旧 map 一致）
	for _, f := range moduleFiles(pkgs, mods) {
		for pos := f.Base(); pos < f.Base()+f.Size(); pos += 3 {
			got, ok := ix.lookup(token.Pos(pos))
			name, wantOK := want[token.Pos(pos)]
			if ok != wantOK || (ok && got != name) {
				t.Fatalf("位置 %d: lookp=%q,%v want=%q,%v", pos, got, ok, name, wantOK)
			}
		}
	}
	if _, ok := ix.lookup(token.NoPos); ok {
		t.Fatal("NoPos 不应命中")
	}
}

// TestIdentIndexLazyPerDecl：只构建被查询位置所属的声明；NoPos 不触发构建。
func TestIdentIndexLazyPerDecl(t *testing.T) {
	pkgs := identFixturePackages(t)
	mods := []string{"example.com/mtest"}
	files := moduleFiles(pkgs, mods)
	if len(files) < 2 {
		t.Fatalf("fixture 文件数 = %d, want >=2", len(files))
	}
	want := eagerIdents(pkgs, mods)
	ix := buildIdentIndex(pkgs, mods)
	namePos := func(f *token.File) token.Pos {
		if af := ix.files[f]; af != nil && af.Name != nil {
			return af.Name.Pos()
		}
		return token.NoPos
	}
	var pos1, pos2 token.Pos
	for pos := range want {
		p := int(pos)
		if pos1 == 0 && files[0].Base() <= p && p < files[0].Base()+files[0].Size() && pos != namePos(files[0]) {
			pos1 = pos
		}
		if pos2 == 0 && files[1].Base() <= p && p < files[1].Base()+files[1].Size() && pos != namePos(files[1]) {
			pos2 = pos
		}
	}
	if pos1 == 0 || pos2 == 0 {
		t.Fatal("fixture 未在两个文件各找到标识符")
	}
	if _, ok := ix.lookup(pos1); !ok {
		t.Fatal("首个标识符应命中")
	}
	if len(ix.byDecl) != 1 {
		t.Fatalf("只应构建 1 个声明，got %d", len(ix.byDecl))
	}
	if _, ok := ix.lookup(token.NoPos); ok {
		t.Fatal("NoPos 不应命中")
	}
	if len(ix.byDecl) != 1 {
		t.Fatal("NoPos 不应触发声明构建")
	}
	ix.lookup(pos2)
	if len(ix.byDecl) != 2 {
		t.Fatalf("第二个文件未按需构建: %d", len(ix.byDecl))
	}
}

// TestIdentIndexConcurrent：并发首次查询同一/不同文件（-race 验证）。
func TestIdentIndexConcurrent(t *testing.T) {
	pkgs := identFixturePackages(t)
	mods := []string{"example.com/mtest"}
	want := eagerIdents(pkgs, mods)
	ix := buildIdentIndex(pkgs, mods)
	var wg sync.WaitGroup
	errs := make(chan string, 64)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for pos, name := range want {
				if got, ok := ix.lookup(pos); !ok || got != name {
					errs <- "mismatch"
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
}

// TestIdentIndexModuleFilter：非模块（modules 不匹配）→ 声明表为空、
// 查询恒 false（与旧实现一致）。
func TestIdentIndexModuleFilter(t *testing.T) {
	pkgs := identFixturePackages(t)
	ix := buildIdentIndex(pkgs, []string{"example.com/other"})
	if len(ix.files) != 0 || len(ix.byDecl) != 0 {
		t.Fatal("非模块包不应登记文件")
	}
	if _, ok := ix.lookup(token.Pos(10)); ok {
		t.Fatal("空索引不应命中")
	}
	if _, ok := (&identIndex{}).lookup(token.Pos(10)); ok {
		t.Fatal("零值索引不应命中")
	}
}

// TestIdentIndexNoPosCompat（todo10-3）：旧全局 map 若含 Pos==0 的合成
// 标识符，任何 Pos==0 查询都命中它（后写覆盖）；空索引不命中。
func TestIdentIndexNoPosCompat(t *testing.T) {
	ix := &identIndex{fset: token.NewFileSet(), ordered: []*ast.File{
		{Name: ast.NewIdent("first")},
		{Name: ast.NewIdent("last")},
	}}
	name, ok := ix.lookup(token.NoPos)
	if !ok || name != "last" {
		t.Fatalf("NoPos lookup = (%q,%v), want (last,true)", name, ok)
	}
	if _, ok := (&identIndex{}).lookup(token.NoPos); ok {
		t.Fatal("空索引 NoPos 不应命中")
	}
}

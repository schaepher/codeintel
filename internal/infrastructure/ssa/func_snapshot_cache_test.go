package ssa

import (
	"go/types"
	"testing"

	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// testFuncSnapshot 用 fixture 构建一个 funcSnapshot 与一个具名函数对象。
func testFuncSnapshot(t *testing.T) (*funcSnapshot, *types.Func) {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir+"/go.mod", baseModule)
	writeFile(t, dir+"/main.go", baseMain)
	pkgs, err := loadTestPackages(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var fn *types.Func
	for _, p := range pkgs {
		if p.Types == nil {
			continue
		}
		for _, name := range p.Types.Scope().Names() {
			if f, ok := p.Types.Scope().Lookup(name).(*types.Func); ok {
				fn = f
				break
			}
		}
		if fn != nil {
			break
		}
	}
	prog, _ := ssautil.Packages(pkgs, ssa.BuilderMode(0))
	if prog == nil {
		t.Fatal("ssautil.Packages 失败")
	}
	return newFuncSnapshot(prog), fn
}

// TestFuncSnapshotIdentityCache：构建级函数身份缓存——结果与 funcIdentity
// 一致、重复调用命中缓存（map 只增一条）、nil 返回空。
func TestFuncSnapshotIdentityCache(t *testing.T) {
	s, fn := testFuncSnapshot(t)
	if fn == nil {
		t.Fatal("fixture 未找到具名函数")
	}
	want, kind, name := funcIdentity(fn)
	got := s.identityOf(fn)
	if got.id != want || got.kind != kind || got.name != name {
		t.Fatalf("identityOf = %+v; want {%s %s %s}", got, want, kind, name)
	}
	if len(s.idents) != 1 {
		t.Fatalf("首次调用应写入 1 条缓存，got %d", len(s.idents))
	}
	for i := 0; i < 5; i++ {
		_ = s.identityOf(fn)
	}
	if len(s.idents) != 1 {
		t.Fatalf("重复调用不应新增缓存，got %d", len(s.idents))
	}
	if s.identityOf(nil).id != "" {
		t.Error("nil 函数应返回空身份")
	}
}

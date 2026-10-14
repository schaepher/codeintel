package ssa

import (
	"go/types"
	"reflect"
	"testing"

	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// testFuncSnapshotFiles 用给定 fixture 构建 funcSnapshot。
func testFuncSnapshotFiles(t *testing.T, files map[string]string) *funcSnapshot {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		writeFile(t, dir+"/"+name, content)
	}
	pkgs, err := loadTestPackages(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	prog, _ := ssautil.Packages(pkgs, ssa.BuilderMode(0))
	if prog == nil {
		t.Fatal("ssautil.Packages 失败")
	}
	prog.Build() // 构建函数体（否则 Blocks 为空，静态调用点扫描不到）
	return newFuncSnapshot(prog)
}

// testFuncSnapshot 用 base fixture 构建 funcSnapshot 与一个具名函数对象。
func testFuncSnapshot(t *testing.T) (*funcSnapshot, *types.Func) {
	t.Helper()
	s := testFuncSnapshotFiles(t, map[string]string{"go.mod": baseModule, "main.go": baseMain})
	var fn *types.Func
	for _, f := range s.all() {
		if obj, ok := f.Object().(*types.Func); ok && obj != nil {
			fn = obj
			break
		}
	}
	return s, fn
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

// legacyStaticCallSites 旧实现的等价扫描（无索引：全程序遍历，顺序为
// 函数列表 → 块 → 指令）。
func legacyStaticCallSites(s *funcSnapshot) map[*ssa.Function][][]ssa.Value {
	idx := map[*ssa.Function][][]ssa.Value{}
	for _, f := range s.all() {
		for _, b := range f.Blocks {
			for _, instr := range b.Instrs {
				call, ok := instr.(*ssa.Call)
				if !ok {
					continue
				}
				callee := call.Call.StaticCallee()
				if callee == nil {
					continue
				}
				idx[callee] = append(idx[callee], call.Call.Args)
			}
		}
	}
	return idx
}

// TestStaticCallSitesIndexMatchesLegacyScan：构建级静态调用点索引与旧实现
// 的全程序扫描在「扫描范围/调用形式/实参/顺序」上逐函数一致；惰性构建
// 只做一次（sync.Once）。
func TestStaticCallSitesIndexMatchesLegacyScan(t *testing.T) {
	s := testFuncSnapshotFiles(t, map[string]string{
		"go.mod": baseModule,
		"main.go": `package main

type Svc struct{}

func (s *Svc) Handle(v string) {}

func helper(x int) int { return x + 1 }

func uncalled() {}

func main() {
	svc := &Svc{}
	svc.Handle("a")
	svc.Handle("b")
	_ = helper(1)
	_ = helper(2)
}
`,
	})
	idx := s.staticCallSites()
	if idx == nil {
		t.Fatal("索引不应为 nil")
	}
	if again := s.staticCallSites(); reflect.ValueOf(again).UnsafePointer() != reflect.ValueOf(idx).UnsafePointer() {
		t.Error("staticCallSites 应惰性构建一次（返回同一 map）")
	}
	legacy := legacyStaticCallSites(s)
	if len(idx) != len(legacy) {
		t.Fatalf("索引条目数 %d != 旧实现 %d", len(idx), len(legacy))
	}
	for fn, want := range legacy {
		if !reflect.DeepEqual(idx[fn], want) {
			t.Errorf("调用点不一致 %s: idx=%v legacy=%v", fn.Name(), idx[fn], want)
		}
	}
	// 无调用点的函数不出现在索引里（不误建）
	for _, f := range s.all() {
		if f.Name() == "uncalled" {
			if got := idx[f]; len(got) != 0 {
				t.Errorf("无调用点的函数不应有条目，got %v", got)
			}
		}
	}
}

// TestStaticCallSitesRecursive：递归调用会同时出现「自身调用点 + 外部
// 调用点」，索引不丢。
func TestStaticCallSitesRecursive(t *testing.T) {
	s := testFuncSnapshotFiles(t, map[string]string{
		"go.mod": baseModule,
		"main.go": `package main

func rec(n int) int {
	if n <= 0 {
		return 0
	}
	return rec(n-1)
}

func main() { _ = rec(3) }
`,
	})
	idx := s.staticCallSites()
	var rec *ssa.Function
	for _, f := range s.all() {
		if f.Name() == "rec" {
			rec = f
			break
		}
	}
	if rec == nil {
		t.Fatal("未找到 rec")
	}
	if got := len(idx[rec]); got != 2 {
		t.Fatalf("递归函数应有 2 个调用点（自调用 + main），got %d", got)
	}
}

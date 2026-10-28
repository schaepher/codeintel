//go:build integration

package integration

// todo25 §5：枚举类型 + 跨包常量别名/类型转换的场景选择（真实 init → CLI）。

import (
	"path/filepath"
	"testing"
)

const todo25SceneFlow = `package scene

import "example.com/scene/dep"

type Source int32

const (
	SourceA Source = 7
	SourceB Source = dep.B
)
const AliasB = SourceB

type Request struct{ Source Source }

func Check1() {}
func Check2() {}
func Check3() {}
func Check4() {}
func Check5() {}
func Check6() {}
func Check7() {}

func Entry(req Request) {
	checks := []func(){Check1, Check2, Check3, Check4}
	if req.Source == AliasB {
		checks = append(checks, Check5, Check6, Check7)
	}
	for _, check := range checks {
		check()
	}
}
`

func TestTodo25SceneEnumCrossPackageConst(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/scene\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "scene.go"), todo25SceneFlow)
	writeFile(t, filepath.Join(dir, "dep", "dep.go"), "package dep\n\nconst B = 19\n")
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "init")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit=%d", code)
	}
	sym := "symbol:go:example.com/scene:Entry"
	a := seqTreeJSON(t, dir, sym, 8, "--scene", "Source=7")
	for _, s := range []string{":Check1", ":Check2", ":Check3", ":Check4"} {
		if countSeqSym(a, s) != 1 {
			t.Errorf("Source=7 应执行 %s", s)
		}
	}
	for _, s := range []string{":Check5", ":Check6", ":Check7"} {
		if countSeqSym(a, s) != 0 {
			t.Errorf("Source=7 不应执行 %s", s)
		}
	}
	b := seqTreeJSON(t, dir, sym, 8, "--scene", "Source=19")
	for i := 1; i <= 7; i++ {
		if countSeqSym(b, ":Check"+itoaTest(i)) != 1 {
			t.Errorf("Source=19 应执行 Check%d", i)
		}
	}
}

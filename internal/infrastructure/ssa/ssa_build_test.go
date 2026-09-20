package ssa

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// Q252e：runBounded 的并发语义（上界 + 真的并行）。
func TestRunBoundedParallelismAndLimit(t *testing.T) {
	t.Run("并行度可达上界", func(t *testing.T) {
		// 确定性证明：3 个任务必须**同时在跑**才能放行（串行实现会卡死
		// 到超时——用 select 超时 fail 而不是挂测试）。
		const workers = 3
		var inFlight, maxInFlight int32
		var mu sync.Mutex
		entered := make(chan struct{}, workers)
		release := make(chan struct{})
		done := make(chan struct{})
		go func() {
			runBounded(workers, 6, func(i int) {
				cur := atomic.AddInt32(&inFlight, 1)
				mu.Lock()
				if cur > maxInFlight {
					maxInFlight = cur // nolint:staticcheck // 持锁读改写
				}
				mu.Unlock()
				if cur <= workers {
					entered <- struct{}{} // 前 workers 个任务报门
				}
				<-release
				atomic.AddInt32(&inFlight, -1)
			})
			close(done)
		}()
		for i := 0; i < workers; i++ {
			select {
			case <-entered:
			case <-time.After(10 * time.Second):
				close(release)
				t.Fatalf("只跑起来 %d 个任务（< workers=%d）——runBounded 没有并行", i, workers)
			}
		}
		close(release)
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("runBounded 未返回")
		}
		mu.Lock()
		got := maxInFlight
		mu.Unlock()
		if got > workers {
			t.Fatalf("并发上界被突破：max=%d > workers=%d", got, workers)
		}
	})

	t.Run("workers<=1 退串行", func(t *testing.T) {
		var inFlight, maxInFlight int32
		runBounded(1, 5, func(i int) {
			cur := atomic.AddInt32(&inFlight, 1)
			for {
				old := atomic.LoadInt32(&maxInFlight)
				if cur <= old || atomic.CompareAndSwapInt32(&maxInFlight, old, cur) {
					break
				}
			}
			time.Sleep(time.Millisecond)
			atomic.AddInt32(&inFlight, -1)
		})
		if maxInFlight != 1 {
			t.Fatalf("workers=1 应当串行：max=%d", maxInFlight)
		}
	})

	t.Run("全任务都执行且 n=0 安全", func(t *testing.T) {
		var seen int32
		runBounded(4, 10, func(i int) { atomic.AddInt32(&seen, 1) })
		if seen != 10 {
			t.Fatalf("任务数不符：%d != 10", seen)
		}
		runBounded(4, 0, func(i int) { t.Error("n=0 不应执行任何任务") })
	})
}

// Q252e 契约：只建模块包，依赖包不建 body（内存契约——依赖连 AST 都
// 不在内存里，建 body 会凭空多出整张依赖图；换成 prog.Build() 即破坏）。
func TestBuildModuleSSAOnlyModulePackages(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/go.mod", baseModule)
	writeFile(t, dir+"/main.go", `package main

import "fmt"

func main() {
	fmt.Sprintf("%d", greet())
}

func greet() string { return "hi" }
`)
	pkgs, err := loadTestPackages(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	prog, ssaPkgs := ssautil.Packages(pkgs, ssa.BuilderMode(0))
	if prog == nil {
		t.Fatal("ssautil.Packages 返回 nil")
	}
	built := buildModuleSSA(pkgs, ssaPkgs, []string{"example.com/mtest"}, 4)
	if built != 1 {
		t.Fatalf("模块包数不符：%d != 1", built)
	}
	if fn := mainFuncOf(ssaPkgs); fn == nil || len(fn.Blocks) == 0 {
		t.Fatal("模块包 main 未被构建（Blocks 为空）")
	}
	if !dependencyUnbuilt(t, ssaPkgs) {
		t.Fatal("依赖包被连带构建了（依赖 body 不该出现在内存里）")
	}
}

// mainFuncOf 取模块包 main 的 SSA 函数（未构建时 Blocks 为空）。
func mainFuncOf(ssaPkgs []*ssa.Package) *ssa.Function {
	for _, sp := range ssaPkgs {
		if sp == nil || sp.Pkg == nil || sp.Pkg.Path() != "example.com/mtest" {
			continue
		}
		return sp.Func("main")
	}
	return nil
}

// 依赖包 body 未构建 = 内存契约成立。
func dependencyUnbuilt(t *testing.T, ssaPkgs []*ssa.Package) bool {
	t.Helper()
	for _, sp := range ssaPkgs {
		if sp == nil || sp.Pkg == nil || sp.Pkg.Path() != "fmt" {
			continue
		}
		for _, m := range sp.Members {
			if fn, ok := m.(*ssa.Function); ok && len(fn.Blocks) > 0 {
				return false
			}
		}
	}
	return true
}

// Q252e：并发度只该影响速度，不该影响产物——workers=1 与 workers=4
// 的节点/边/摘要必须逐条一致（确定性是 Q250 的既有契约，并行不能破）。
func TestBuildDeterminismAcrossWorkers(t *testing.T) {
	serial := buildDeterminismSnapshotWorkers(t, 1)
	parallel := buildDeterminismSnapshotWorkers(t, 4)
	if len(serial.nodes) == 0 || len(serial.edges) == 0 {
		t.Fatalf("fixture 未产出（nodes=%d edges=%d）", len(serial.nodes), len(serial.edges))
	}
	for _, c := range []struct {
		name string
		a, b []string
	}{
		{"节点", serial.nodes, parallel.nodes},
		{"边", serial.edges, parallel.edges},
		{"摘要", serial.summaries, parallel.summaries},
	} {
		if d := diffLines(c.a, c.b); len(d) > 0 {
			t.Errorf("%s 在 workers=1/4 下不一致（%d 条差异，共 %d/%d）：\n  %s\n  %s\n  %s",
				c.name, len(d), len(c.a), len(c.b), text(d, 0), text(d, 1), text(d, 2))
		}
	}
}

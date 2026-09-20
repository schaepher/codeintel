package ssa

import (
	"sync"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
)

// buildModuleSSA 构建**模块内**包的 SSA 函数体，返回实际构建的包数。
//
// Q252e：原实现逐包串行 `sp.Build()`——go/ssa 的构建是 CPU 密集的，
// 8 核只用 1 核。这里照抄 `go/ssa` 自己的 `Program.Build`
// （x/tools@v0.47.0 go/ssa/builder.go：cpuLimit 信号量 + 每包一
// goroutine，且**不按依赖排序**——跨包引用拿到的是声明节点，body
// 事后就地填充，所以顺序无关），但做两处收紧：
//
//  1. **只对模块包生效**：依赖包不需要 body（Q247 起依赖连 AST 都不
//     在内存里）。直接调 `prog.Build()` 会把整个传递依赖图也建一遍，
//     峰值内存不可控。
//  2. **上界用 --workers 而非 GOMAXPROCS**：并行建图同时持有多个包的
//     SSA 中间产物，并发度直接换内存；小内存机器可 `--workers 1` 退回
//     原串行行为。
func buildModuleSSA(pkgs []*packages.Package, ssaPkgs []*ssa.Package, modules []string, workers int) int {
	targets := make([]*ssa.Package, 0, len(pkgs))
	for i, p := range pkgs {
		if !isInModule(p.PkgPath, modules) {
			continue
		}
		if sp := ssaPkgs[i]; sp != nil {
			targets = append(targets, sp)
		}
	}
	runBounded(workers, len(targets), func(i int) { targets[i].Build() })
	return len(targets)
}

// runBounded 并发执行 n 个任务（任务以索引传递），并发上界 workers；
// workers ≤ 1 或仅 1 个任务时退化为串行（保持确定性调用顺序）。
func runBounded(workers, n int, fn func(i int)) {
	if n <= 0 {
		return
	}
	if workers < 1 {
		workers = 1
	}
	if workers == 1 || n == 1 {
		for i := 0; i < n; i++ {
			fn(i)
		}
		return
	}
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		sem <- struct{}{} // 先取令牌再起 goroutine（同 Program.Build）
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			fn(i)
		}(i)
	}
	wg.Wait()
}

package ssa

// implTypePool 并发首次计算合并测试（从 impl_pool_test.go 拆出——行数治理）。

import (
	"go/types"
	"sync"
	"testing"
)

// TestImplPoolConcurrentComputeOnce（todo8-13）：同一接口并发首次 implsFor
// 只计算一次；候选集合一致。
func TestImplPoolConcurrentComputeOnce(t *testing.T) {
	pool, pkgs := implPoolTestPool(t)
	iface := ifaceNamed(t, pkgs, "example.com/mtest/ifaces", "Store")
	const n = 8
	results := make([][]pooledType, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = pool.implsFor(iface)
		}(i)
	}
	wg.Wait()
	for i := 1; i < n; i++ {
		if len(results[i]) != len(results[0]) {
			t.Fatalf("并发结果不一致: %d vs %d", len(results[i]), len(results[0]))
		}
	}
	if got := pool.computes.Load(); got != 1 {
		t.Errorf("并发首次 computes = %d, want 1", got)
	}
}

// TestImplPoolConcurrentMethodsOnce（todo8-13）：同 (接口,方法) 并发首次
// 过滤只执行一次；不同方法各自一次。
func TestImplPoolConcurrentMethodsOnce(t *testing.T) {
	pool, pkgs := implPoolTestPool(t)
	iface := ifaceNamed(t, pkgs, "example.com/mtest/ifaces", "Store")
	const n = 8
	results := make([][]*types.Func, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = pool.methodsFor(iface, "Save")
		}(i)
	}
	wg.Wait()
	for i := 1; i < n; i++ {
		if len(results[i]) != len(results[0]) {
			t.Fatalf("并发方法结果不一致: %d vs %d", len(results[i]), len(results[0]))
		}
	}
	if got := pool.methodComputes.Load(); got != 1 {
		t.Errorf("同 (接口,方法) 并发过滤 = %d, want 1", got)
	}
	// 另一个方法：再一次
	_ = pool.methodsFor(iface, "Load")
	if got := pool.methodComputes.Load(); got != 2 {
		t.Errorf("不同方法过滤 = %d, want 2", got)
	}
}

// TestImplPoolDifferentIfacesParallel（todo8-13）：不同接口各自计算一次
// （不能用全局锁串行化整个池扫描）。
func TestImplPoolDifferentIfacesParallel(t *testing.T) {
	pool, pkgs := implPoolTestPool(t)
	store := ifaceNamed(t, pkgs, "example.com/mtest/ifaces", "Store")
	closer := ifaceNamed(t, pkgs, "example.com/mtest/ifaces", "Closer")
	var wg sync.WaitGroup
	for _, iface := range []*types.Named{store, closer} {
		wg.Add(1)
		go func(iface *types.Named) {
			defer wg.Done()
			_ = pool.implsFor(iface)
		}(iface)
	}
	wg.Wait()
	if got := pool.computes.Load(); got != 2 {
		t.Errorf("两个接口 computes = %d, want 2", got)
	}
}

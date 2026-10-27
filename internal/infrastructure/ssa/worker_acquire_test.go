package ssa

// todo23 §10.2：SSA worker 令牌获取的取消边界——取消前不取新令牌；
// 取得后发现已取消立即归还（不泄漏）。

import (
	"context"
	"testing"
)

func TestAcquireWorkerCancelBoundaries(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sem := make(chan struct{}, 1)
	for i := 0; i < 100; i++ {
		if acquireWorker(ctx, sem) {
			t.Fatalf("已取消不应取得 worker 令牌")
		}
		if len(sem) != 0 {
			t.Fatalf("取消后令牌应归还，got len=%d", len(sem))
		}
	}
}

func TestAcquireWorkerSuccessAndRelease(t *testing.T) {
	sem := make(chan struct{}, 1)
	if !acquireWorker(context.Background(), sem) {
		t.Fatalf("未取消应取得令牌")
	}
	if len(sem) != 1 {
		t.Fatalf("取得后令牌应被持有，got len=%d", len(sem))
	}
	// 容量已满 + 取消 → 不能阻塞/泄漏
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if acquireWorker(ctx, sem) {
		t.Fatalf("容量满且已取消不应取得令牌")
	}
	<-sem
	if len(sem) != 0 {
		t.Fatalf("释放后应为空")
	}
}

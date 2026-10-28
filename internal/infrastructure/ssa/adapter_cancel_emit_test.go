package ssa

// T17：acquireWorker 取消边界 + 取消感知 emit 包装器回归。

import (
	"context"
	"errors"
	"testing"
)

// 已取消 + 空槽：连续运行都不启动工作（空槽立即可写也不能绕过取消）。
func TestAcquireWorkerNeverStartsAfterCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := false
	for i := 0; i < 1000; i++ {
		sem := make(chan struct{}, 1)
		if acquireWorker(ctx, sem) {
			started = true
			<-sem
		}
		if len(sem) != 0 {
			t.Fatalf("令牌泄漏: len=%d", len(sem))
		}
	}
	if started {
		t.Fatal("已取消不应启动任何工作（空槽也不例外）")
	}
}

// emit 前取消 → 底层写入 0 次；正常 context → 1 次（且错误透传）。
func TestEmitUnlessCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	ran, err := emitUnlessCanceled(ctx, func() error { calls++; return nil })
	if ran || err != nil || calls != 0 {
		t.Fatalf("取消后不得写底层: ran=%v err=%v calls=%d", ran, err, calls)
	}
	ran, err = emitUnlessCanceled(context.Background(), func() error { calls++; return nil })
	if !ran || err != nil || calls != 1 {
		t.Fatalf("正常 ctx 应写入一次: ran=%v err=%v calls=%d", ran, err, calls)
	}
	wantErr := errors.New("boom")
	if _, err := emitUnlessCanceled(context.Background(), func() error { return wantErr }); !errors.Is(err, wantErr) {
		t.Fatalf("错误应透传: %v", err)
	}
}

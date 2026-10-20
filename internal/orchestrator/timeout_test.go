package orchestrator

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/schaepher/codeintel/internal/domain"
	"golang.org/x/tools/go/packages"
)

// blockingAdapter 等适配器 context 结束后返回其错误（模拟大仓超时）。
type blockingAdapter struct {
	name string
	once sync.Once
	// started 关闭表示已进入 Index（便于测试同步，避免依赖 sleep）
	started chan struct{}
}

func (b *blockingAdapter) Name() string { return b.name }

func (b *blockingAdapter) Index(ctx context.Context, _ *domain.Repository, _ []*packages.Package, _ domain.EmitFunc) error {
	b.once.Do(func() { close(b.started) })
	<-ctx.Done()
	return ctx.Err()
}

// TestAdapterTimeoutDefaultAndValidation（todo9-1）：默认 10m；显式覆盖；
// 非法时长（0/负）被拒绝。
func TestAdapterTimeoutDefaultAndValidation(t *testing.T) {
	o := &Orchestrator{}
	if got := o.adapterTimeoutOrDefault(); got != AdapterTimeout {
		t.Fatalf("默认超时 = %s, want %s", got, AdapterTimeout)
	}
	if err := o.SetAdapterTimeout(0); err == nil {
		t.Fatal("0 应被拒绝")
	}
	if err := o.SetAdapterTimeout(-time.Second); err == nil {
		t.Fatal("负值应被拒绝")
	}
	if err := o.SetAdapterTimeout(45 * time.Minute); err != nil {
		t.Fatalf("合法覆盖失败: %v", err)
	}
	if got := o.adapterTimeoutOrDefault(); got != 45*time.Minute {
		t.Fatalf("覆盖后 = %s", got)
	}
}

// TestAdapterTimeoutDegradesBuild（todo9-1）：一个适配器超时 → 构建 degraded，
// 其他适配器数据保留（独立 context，互不取消）。
func TestAdapterTimeoutDegradesBuild(t *testing.T) {
	blocking := &blockingAdapter{name: "slow", started: make(chan struct{})}
	o, repo := newTestOrchestrator(t, []domain.IndexerPort{
		blocking,
		&fakeAdapter{name: "fast"},
	})
	if err := o.SetAdapterTimeout(40 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	res, err := o.FullBuild(context.Background())
	if err != nil {
		t.Fatalf("FullBuild: %v", err)
	}
	select {
	case <-blocking.started:
	default:
		t.Fatal("慢适配器未被启动")
	}
	if res.Status != domain.BuildDegraded {
		t.Fatalf("status = %s, want degraded (adapters=%+v)", res.Status, res.Adapter)
	}
	if res.Nodes != 1 {
		t.Fatalf("nodes = %d, want 1（快适配器数据保留）", res.Nodes)
	}
	var slowErr error
	for _, a := range res.Adapter {
		if a.Name == "slow" {
			slowErr = a.Err
		}
	}
	if slowErr == nil || !errors.Is(slowErr, context.DeadlineExceeded) {
		t.Fatalf("慢适配器错误 = %v, want context deadline", slowErr)
	}
	meta, gerr := repo.GetLatest()
	if gerr != nil || meta.Status != domain.BuildDegraded || meta.ErrorMsg == "" {
		t.Fatalf("metadata = %+v, err %v", meta, gerr)
	}
}

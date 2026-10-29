package orchestrator

// todo27 T18：AdapterTimeout 兼容字段的既有行为回归（不新增机制）：
// 适配器超时传到 emit（超时后的 late emit 被拒绝）、其他适配器结果保留、
// BuildResult 降级。

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
	"golang.org/x/tools/go/packages"
)

type timeoutAdapter struct {
	mu      sync.Mutex
	lateErr error
	done    chan struct{}
}

func (a *timeoutAdapter) Name() string { return "slow" }

func (a *timeoutAdapter) Index(ctx context.Context, _ *domain.Repository, _ []*packages.Package, emit domain.EmitFunc) error {
	<-ctx.Done()
	close(a.done)
	err := emit(domain.Item{Node: &domain.CodeEntity{
		ID: "symbol:go:example.com/e2e:late", Kind: domain.KindFunction,
		Name: "late", FilePath: "main.go",
	}})
	a.mu.Lock()
	a.lateErr = err
	a.mu.Unlock()
	return ctx.Err()
}

func (a *timeoutAdapter) LateErr() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lateErr
}

type fastAdapter struct{ emitted chan struct{} }

func (a *fastAdapter) Name() string { return "fast" }

func (a *fastAdapter) Index(_ context.Context, _ *domain.Repository, _ []*packages.Package, emit domain.EmitFunc) error {
	if err := emit(domain.Item{Node: &domain.CodeEntity{
		ID: "symbol:go:example.com/e2e:fast", Kind: domain.KindFunction,
		Name: "fast", FilePath: "main.go",
	}}); err != nil {
		return err
	}
	close(a.emitted)
	return nil
}

// TestAdapterTimeoutLateEmitRejected：超时后 emit 返回取消原因，结果降级，
// 其他适配器产物保留。
func TestAdapterTimeoutLateEmitRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/e2e\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "main.go"), "package main\n\nfunc main() {}\n")
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	slow := &timeoutAdapter{done: make(chan struct{})}
	fast := &fastAdapter{emitted: make(chan struct{})}
	orch := &Orchestrator{
		Repo: &domain.Repository{Path: dir, Module: "example.com/e2e",
			Modules: []string{"example.com/e2e"}, ModuleDirs: []string{"."}},
		RepoImpl: sqlite.NewRepo(db),
		Adapters: []domain.IndexerPort{slow, fast},
	}
	if err := orch.SetAdapterTimeout(30 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	res, err := orch.FullBuild(context.Background())
	if err != nil {
		t.Fatalf("FullBuild: %v", err)
	}
	<-slow.done
	if slow.LateErr() == nil {
		t.Errorf("超时后的 late emit 应返回取消原因")
	} else if !errors.Is(slow.LateErr(), context.DeadlineExceeded) {
		t.Errorf("late emit 错误应为 DeadlineExceeded，got %v", slow.LateErr())
	}
	select {
	case <-fast.emitted:
	default:
		t.Fatalf("快适配器应完成发射")
	}
	if res.Status != domain.BuildDegraded {
		t.Errorf("超时适配器应使结果降级（status=%s）", res.Status)
	}
	if _, err := orch.RepoImpl.GetSymbol("symbol:go:example.com/e2e:fast"); err != nil {
		t.Errorf("其他适配器产物应保留：%v", err)
	}
	if _, err := orch.RepoImpl.GetSymbol("symbol:go:example.com/e2e:late"); err == nil {
		t.Errorf("超时后的 late emit 不应写库")
	}
}

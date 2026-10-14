package orchestrator

import (
	"context"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"golang.org/x/tools/go/packages"
)

// pkgsRecorder 记录 Index 收到的包切片首个元素（守卫「适配器共享同一份
// loadPackages 结果」）。
type pkgsRecorder struct {
	name  string
	first *packages.Package
}

func (r *pkgsRecorder) Name() string { return r.name }

func (r *pkgsRecorder) Index(ctx context.Context, repo *domain.Repository, pkgs []*packages.Package, emit domain.EmitFunc) error {
	if len(pkgs) > 0 {
		r.first = pkgs[0]
	}
	return nil
}

// TestAdaptersShareLoadedPackages：AST/SSA 等适配器收到**同一份** go/packages
// 加载结果（禁止各自 Load 造成类型检查翻倍）——守卫已有共享机制不被优化
// 重新引入重复工作。
func TestAdaptersShareLoadedPackages(t *testing.T) {
	a := &pkgsRecorder{name: "a"}
	b := &pkgsRecorder{name: "b"}
	o, _ := newTestOrchestrator(t, []domain.IndexerPort{a, b})
	if _, err := o.FullBuild(context.Background()); err != nil {
		t.Fatalf("FullBuild: %v", err)
	}
	if a.first == nil || b.first == nil {
		t.Fatal("适配器未收到加载结果")
	}
	if a.first != b.first {
		t.Error("适配器未共享同一份加载结果（包指针不同）")
	}
}

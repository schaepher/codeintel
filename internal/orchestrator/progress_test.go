package orchestrator

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/schaepher/codeintel/internal/domain"
)

// recordProgress 记录型进度实现（Q253 接线验证）。
type recordProgress struct {
	mu     sync.Mutex
	begun  []string
	ended  []string
	adv    []string
	finish int
}

func (r *recordProgress) Begin(name string, depth, total int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.begun = append(r.begun, name)
}

func (r *recordProgress) Advance(name string, done int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.adv = append(r.adv, name)
}

func (r *recordProgress) End(name string, elapsed time.Duration, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ended = append(r.ended, name)
}

func (r *recordProgress) Finish() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finish++
}

func (r *recordProgress) has(list []string, want string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// Q253 接线：全量构建要把顶层阶段与每个适配器子步骤都上报给注入的实现。
func TestFullBuildReportsProgress(t *testing.T) {
	o, _ := newTestOrchestrator(t, []domain.IndexerPort{
		&fakeAdapter{name: "a"},
		&fakeAdapter{name: "b"},
	})
	rec := &recordProgress{}
	o.SetProgress(rec)
	if _, err := o.FullBuild(context.Background()); err != nil {
		t.Fatalf("FullBuild: %v", err)
	}
	for _, want := range []string{"loadPackages", "runAdapters", "adapters done", "flush done", "a", "b"} {
		if !rec.has(rec.ended, want) {
			t.Errorf("步骤 %q 未上报结束（已上报 %v）", want, rec.ended)
		}
	}
}

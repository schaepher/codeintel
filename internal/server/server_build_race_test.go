package server

// todo21-13.2：增量构建的 buildFn 读取必须在 buildMu 临界区内完成。
// 修复前 handleIncremental 在解锁后才读 s.buildFn，而 SetBuildFunc 在锁内
// 写它 —— -race 报 "Write: (*Server).SetBuildFunc / Previous read:
// (*Server).handleIncremental"。本回归用并发替换 + 请求证明无 race。

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestIncrementalConcurrentBuildFuncReplacement(t *testing.T) {
	s := &Server{ctx: context.Background()}
	fn := func() (string, error) { return "test", nil }
	s.SetBuildFunc(fn)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 10000 {
			s.SetBuildFunc(fn)
		}
	}()
	for range 1000 {
		w := httptest.NewRecorder()
		s.handleIncremental(w, httptest.NewRequest(http.MethodPost, "/incremental", nil))
		if w.Code != http.StatusAccepted && w.Code != http.StatusConflict {
			t.Fatalf("unexpected status %d", w.Code)
		}
	}
	wg.Wait()
}

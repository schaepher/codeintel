package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// newTestServer 建临时 DB + 预填数据 + 临时源码文件，返回 httptest server。
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	repoDir := t.TempDir()

	if err := os.MkdirAll(filepath.Join(repoDir, "internal", "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := "package app\n\n// Greet 打招呼。\nfunc Greet(name string) string {\n\treturn \"hi \" + name\n}\n"
	if err := os.WriteFile(filepath.Join(repoDir, "internal", "app", "app.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	db, err := sqlite.Open(repoDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)

	greet := &domain.CodeEntity{
		ID:        "symbol:go:example.com/app:Greet",
		Kind:      domain.KindFunction,
		Name:      "Greet",
		FilePath:  "internal/app/app.go",
		LineStart: 3,
		LineEnd:   5,
		Properties: map[string]any{
			"signature":   "func Greet(name string) string",
			"doc_comment": "Greet 打招呼。",
		},
	}
	main := &domain.CodeEntity{
		ID:        "symbol:go:example.com/app:main",
		Kind:      domain.KindFunction,
		Name:      "main",
		FilePath:  "cmd/main.go",
		LineStart: 1,
		LineEnd:   3,
		Properties: map[string]any{
			"serves_http": "true",
		},
	}
	svc := &domain.CodeEntity{
		ID:       "symbol:go:example.com/app:Svc",
		Kind:     domain.KindStruct,
		Name:     "Svc",
		FilePath: "internal/app/svc.go",
		Properties: map[string]any{
			"fields": []any{map[string]any{"name": "Port", "type": "int"}},
		},
	}
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{greet, main, svc}, nil, nil); err != nil {
		t.Fatalf("save nodes: %v", err)
	}
	if _, err := r.SaveBatchStats(nil, []*domain.Fact{{
		SourceID: main.ID, TargetID: greet.ID, Kind: domain.FactCalls,
		Confidence: 0.8, Metadata: map[string]any{"line_num": 2},
	}}, nil); err != nil {
		t.Fatalf("save edge: %v", err)
	}

	web := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html>codeintel</html>")},
	}
	srv := New(context.Background(), action.New(r), web, repoDir)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}
func get(t *testing.T, ts *httptest.Server, path string) (*http.Response, map[string]any) {
	t.Helper()
	resp, err := http.Get(ts.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	var m map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return resp, m
}

// TestHandleIncremental：POST /incremental（field_trace.md §20.1）——
// 未配置 buildFn → 404；配置后 → 202 + 异步执行；执行中 → 409。
//
// todo21-13.1：互斥断言必须基于**本次构建明确进入**的信号。原实现先跑一个
// 立即返回的 buildFn，再轮询全局 building 标记猜测第二个请求是否开始；
// 第一轮 handler 的 deferred building=false 未必已执行，轮询可能读到它的
// 残留状态（整包 -count=3 偶发 "执行中应 409，got 202"）。现在同一次
// buildFn 在进入后阻塞，测试拿到进入信号再断言 409。
func TestHandleIncremental(t *testing.T) {
	dir := t.TempDir()
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	web := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("x")}}
	srv := New(context.Background(), action.New(r), web, dir)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	post := func() *http.Response {
		t.Helper()
		resp, err := http.Post(ts.URL+"/incremental", "application/json", nil)
		if err != nil {
			t.Fatalf("POST /incremental: %v", err)
		}
		resp.Body.Close()
		return resp
	}

	if resp := post(); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("未配置 buildFn 应 404，got %d", resp.StatusCode)
	}

	entered := make(chan struct{}, 1) // 本次构建已进入
	block := make(chan struct{})
	finished := make(chan struct{})
	var calls int32
	var releaseOnce, finishOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(block) }) }
	defer release() // 断言失败（t.Fatal）时也要释放，不把构建 goroutine 卡到进程结束
	srv.SetBuildFunc(func() (string, error) {
		atomic.AddInt32(&calls, 1)
		select {
		case entered <- struct{}{}:
		default:
		}
		<-block
		finishOnce.Do(func() { close(finished) })
		return "build-1", nil
	})

	if resp := post(); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("空闲请求应 202，got %d", resp.StatusCode)
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("buildFn 未异步执行")
	}

	// 同一次构建执行期间再次请求 → 409（不再轮询全局标记）
	if resp := post(); resp.StatusCode != http.StatusConflict {
		t.Errorf("执行中应 409，got %d", resp.StatusCode)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Errorf("buildFn 执行 %d 次（应恰为 1 次）", n)
	}

	release()
	select { // 构建 goroutine 已退出，测试结束时不再有后台写
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("释放后 buildFn 未返回")
	}
}

// TestModuleCallsEndpoint：/api/module-calls（field_trace.md §21.3）——
// 空数据返回空数组；module-calls JSON 结构。
func TestModuleCallsEndpoint(t *testing.T) {
	ts := newTestServer(t)
	resp, m := get(t, ts, "/api/module-calls")
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	calls, ok := m["calls"].([]any)
	if !ok {
		t.Fatalf("calls 字段缺失: %v", m)
	}
	if len(calls) != 0 {
		t.Errorf("无调用数据应为空数组: %v", calls)
	}
}

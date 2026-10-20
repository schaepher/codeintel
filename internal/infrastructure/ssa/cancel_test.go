package ssa

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/schaepher/codeintel/internal/domain"
	"golang.org/x/tools/go/packages"
)

// cancelFixture 两个包（各 1 个块）——workers=1 时第二个块必然在等令牌。
var cancelFixture = map[string]string{
	"go.mod": moduleGoMod,
	"a/a.go": `package a

type A struct{ X int }

func UseA() int { v := &A{}; v.X = 1; return v.X }
`,
	"b/b.go": `package b

type B struct{ Y int }

func UseB() int { v := &B{}; v.Y = 2; return v.Y }
`,
}

func runIndexCtx(t *testing.T, ctx context.Context, workers int, emit domain.EmitFunc) (*Adapter, string, error) {
	t.Helper()
	dir := t.TempDir()
	for path, content := range cancelFixture {
		writeFile(t, filepath.Join(dir, path), content)
	}
	pkgs, err := loadTestPackages(dir)
	if err != nil {
		t.Fatal(err)
	}
	a := &Adapter{}
	a.SetWorkers(workers)
	repo := &domain.Repository{Path: dir, Module: "example.com/mtest", Modules: []string{"example.com/mtest"}}
	return a, dir, a.Index(ctx, repo, pkgs, emit)
}

func cacheFiles(t *testing.T, dir string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, ".codeintel", "cache", "*"))
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// TestIndexCancelDuringEmit（todo9-2）：发射阻塞时取消 → Index 返回
// context.Canceled，且取消的包不落盘可命中的缓存。
func TestIndexCancelDuringEmit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	var n atomic.Int32
	emit := func(domain.Item) error {
		if n.Add(1) == 1 {
			close(started)
		}
		<-ctx.Done() // 模拟 orchestrator channel 阻塞直到适配器取消
		return ctx.Err()
	}
	go func() {
		<-started
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	_, dir, err := runIndexCtx(t, ctx, 2, emit)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if files := cacheFiles(t, dir); len(files) != 0 {
		t.Fatalf("取消后不应写缓存: %v", files)
	}
}

// TestIndexCancelWhileWaitingWorker（todo9-2）：workers=1 时第二个块等待
// 令牌，取消应停止排队并返回错误（不继续投递/不死锁）。
func TestIndexCancelWhileWaitingWorker(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	var blocked atomic.Bool
	emit := func(domain.Item) error {
		if blocked.CompareAndSwap(false, true) {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		}
		return nil // 第二个 worker（若被调度）不应走到这里
	}
	go func() {
		<-started
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	_, dir, err := runIndexCtx(t, ctx, 1, emit)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if files := cacheFiles(t, dir); len(files) != 0 {
		t.Fatalf("取消后不应写缓存: %v", files)
	}
}

// TestIndexWarmReplayNoRewrite（todo9-2）：暖重放命中缓存不重写文件；
// 随后取消的构建也不改动缓存。
func TestIndexWarmReplayNoRewrite(t *testing.T) {
	firstEmit := func(domain.Item) error { return nil }
	_, dir, err := runIndexCtx(t, context.Background(), 1, firstEmit)
	if err != nil {
		t.Fatalf("first index: %v", err)
	}
	files := cacheFiles(t, dir)
	if len(files) == 0 {
		t.Fatal("首次构建应写包缓存")
	}
	type snap struct {
		mod  time.Time
		data []byte
	}
	before := make(map[string]snap, len(files))
	for _, f := range files {
		st, err := os.Stat(f)
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		before[f] = snap{mod: st.ModTime(), data: data}
	}
	// 暖重放：同一目录/范围再跑一次（不取消），缓存不得重写
	dir2 := dir
	pkgs, err := loadTestPackages(dir2)
	if err != nil {
		t.Fatal(err)
	}
	a := &Adapter{}
	a.SetWorkers(1)
	repo := &domain.Repository{Path: dir2, Module: "example.com/mtest", Modules: []string{"example.com/mtest"}}
	if err := a.Index(context.Background(), repo, pkgs, firstEmit); err != nil {
		t.Fatalf("warm index: %v", err)
	}
	for f, want := range before {
		st, err := os.Stat(f)
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if !st.ModTime().Equal(want.mod) || string(got) != string(want.data) {
			t.Fatalf("暖重放重写了缓存文件 %s", f)
		}
	}
	// 取消的构建不改缓存
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := a.Index(ctx, repo, pkgs, firstEmit); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled index err = %v", err)
	}
	for f, want := range before {
		got, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want.data) {
			t.Fatalf("取消构建改动了缓存文件 %s", f)
		}
	}
}

// TestPkgCacheKeyHashCanceled：缓存准备阶段取消返回 ctx 错误（不读文件）。
func TestPkgCacheKeyHashCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pkg := &packages.Package{CompiledGoFiles: []string{"/nonexistent/x.go"}}
	if _, err := pkgCacheKeyHash(ctx, pkg, map[string]string{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

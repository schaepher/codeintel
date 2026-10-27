package action

// todo22 P1 测试：DI 的框架式契约（Bean/Provider 注解、容器 Set）与
// **跨包注册点**（注册函数在另一个包）——都必须有可核对证据；扩展契约
// （Set/Bean）不看字段名兜底，必须有显式注解。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// TestSeqDIBeanAnnotation：provider 声明注释 `// @bean mem` → 返回类型登记。
func TestSeqDIBeanAnnotation(t *testing.T) {
	src := `package m

import "context"

type Store interface{ Add(ctx context.Context) error }

type memStore struct{}

func (s *memStore) Add(ctx context.Context) error { MemWrite(); return nil }

func MemWrite() {}

// @bean mem
func newMemStore() *memStore { return &memStore{} }

type Handler struct {
	// @inject mem
	store Store
}

func (h *Handler) Handle(ctx context.Context) error { return h.store.Add(ctx) }
`
	const pkg = "symbol:go:example.com/m:"
	acts, dir := seedDIRepo(t, src, map[string]string{"h.store.Add": pkg + "(Store).Add"})
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "Handle", RepoAbs: dir, Depth: 4})
	if err != nil {
		t.Fatalf("CodeSequence: %v", err)
	}
	nodes := seqCallNodesNamed(root, "h.store.Add")
	if len(nodes) != 1 || nodes[0].Evidence == nil {
		t.Fatalf("应有 1 个带证据的 h.store.Add 节点，got %+v", nodes)
	}
	ev := nodes[0].Evidence
	if ev.Reason != seqReasonDIBeanAnnotation {
		t.Errorf("reason 应为 %s，got %q", seqReasonDIBeanAnnotation, ev.Reason)
	}
	if ev.SymbolID != pkg+"(memStore).Add" {
		t.Errorf("应具体化到 (memStore).Add，got %q", ev.SymbolID)
	}
	if ev.RegisterSite == 0 || ev.RegisterFile == "" {
		t.Errorf("证据应带注册站点，got file=%q line=%d", ev.RegisterFile, ev.RegisterSite)
	}
}

// TestSeqDIContainerSet：容器 `Set("mem", &impl{})` + 字段注解。
func TestSeqDIContainerSet(t *testing.T) {
	src := `package m

import "context"

type Store interface{ Add(ctx context.Context) error }

type memStore struct{}

func (s *memStore) Add(ctx context.Context) error { MemWrite(); return nil }

func MemWrite() {}

type Container struct{}

func (c *Container) Set(key string, impl any) {}

func Setup() {
	c := &Container{}
	c.Set("mem", &memStore{})
}

type Handler struct {
	// @inject mem
	store Store
}

func (h *Handler) Handle(ctx context.Context) error { return h.store.Add(ctx) }
`
	const pkg = "symbol:go:example.com/m:"
	acts, dir := seedDIRepo(t, src, map[string]string{"h.store.Add": pkg + "(Store).Add"})
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "Handle", RepoAbs: dir, Depth: 4})
	if err != nil {
		t.Fatalf("CodeSequence: %v", err)
	}
	nodes := seqCallNodesNamed(root, "h.store.Add")
	if len(nodes) != 1 || nodes[0].Evidence == nil || nodes[0].Evidence.SymbolID != pkg+"(memStore).Add" {
		t.Fatalf("Set 注册应具体化到 (memStore).Add，got %+v", nodes)
	}
	if !strings.Contains(nodes[0].Evidence.Reason, "di-field") {
		t.Errorf("reason 应含 di-field，got %q", nodes[0].Evidence.Reason)
	}
}

// TestSeqDIContainerSetRequiresAnnotation：扩展契约没有字段注解时不得按
// 字段名兜底（避免把普通容器的 Set 当成 DI）。
func TestSeqDIContainerSetRequiresAnnotation(t *testing.T) {
	src := `package m

import "context"

type Store interface{ Add(ctx context.Context) error }

type memStore struct{}

func (s *memStore) Add(ctx context.Context) error { return nil }

type Container struct{}

func (c *Container) Set(key string, impl any) {}

func Setup() {
	c := &Container{}
	c.Set("store", &memStore{})
}

type Handler struct {
	store Store
}

func (h *Handler) Handle(ctx context.Context) error { return h.store.Add(ctx) }
`
	const pkg = "symbol:go:example.com/m:"
	acts, dir := seedDIRepo(t, src, map[string]string{"h.store.Add": pkg + "(Store).Add"})
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "Handle", RepoAbs: dir, Depth: 4})
	if err != nil {
		t.Fatalf("CodeSequence: %v", err)
	}
	for _, n := range seqCallNodesNamed(root, "h.store.Add") {
		if n.Evidence != nil && n.Evidence.Source == SeqEvidenceDI {
			t.Errorf("无字段注解不得走扩展 DI 契约，got %+v", n.Evidence)
		}
	}
}

// seedDICrossPkgRepo：两个包（app + di），注册在 di.Setup。
func seedDICrossPkgRepo(t *testing.T) (*Actions, string) {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, body string) {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/x\n\ngo 1.21\n")
	write("app/app.go", `package app

import (
	"context"

	"example.com/x/di"
)

type Handler struct {
	// @inject store
	store *di.Store
}

func (h *Handler) Handle(ctx context.Context) error { return h.store.Add(ctx) }
`)
	write("di/di.go", `package di

import "context"

type Store struct{}

func (s *Store) Add(ctx context.Context) error { return nil }

func Register(key string, impl any) {}

func Setup() { Register("store", &Store{}) }
`)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	// app 与 di 的索引节点（调用点靠名称唯一回退命中）
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/x/app:(Handler).Handle", Kind: domain.KindMethod,
			Name: "(Handler).Handle", FilePath: "app/app.go", LineStart: 14},
		{ID: "symbol:go:example.com/x/di:(Store).Add", Kind: domain.KindMethod,
			Name: "(Store).Add", FilePath: "di/di.go", LineStart: 7},
		{ID: "symbol:go:example.com/x/di:Setup", Kind: domain.KindFunction,
			Name: "Setup", FilePath: "di/di.go", LineStart: 11},
	}
	facts := []*domain.Fact{{
		SourceID: "symbol:go:example.com/x/app:(Handler).Handle",
		TargetID: "symbol:go:example.com/x/di:(Store).Add",
		Kind:     domain.FactCalls, ToolSource: domain.ToolCodeGraph, Confidence: 0.8,
		Metadata: map[string]any{"line_num": 14, "pos": 0},
	}}
	if _, err := r.SaveBatchStats(nodes, facts, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(&domain.BuildMeta{BuildID: "dicross1", ToolName: "fixture", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	return New(r), dir
}

// countingDIFuncsReader 统计跨包注册点定位查询次数（请求内缓存断言）。
type countingDIFuncsReader struct {
	Reader
	calls int
}

func (r *countingDIFuncsReader) GetFunctionsByNames(names []string, limit int) ([]*domain.CodeEntity, error) {
	r.calls++
	return r.Reader.GetFunctionsByNames(names, limit)
}

// TestSeqDICrossPackageRegistration：注册点在同仓另一个包的 Setup 里，
// 字段注解匹配后仍能具体化，并带跨包注册站点证据。
func TestSeqDICrossPackageRegistration(t *testing.T) {
	acts, dir := seedDICrossPkgRepo(t)
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/x/app:(Handler).Handle", RepoAbs: dir, Depth: 4})
	if err != nil {
		t.Fatalf("CodeSequence: %v", err)
	}
	nodes := seqCallNodesNamed(root, "h.store.Add")
	if len(nodes) != 1 || nodes[0].Evidence == nil {
		t.Fatalf("应有 1 个带证据的 h.store.Add 节点，got %+v", nodes)
	}
	ev := nodes[0].Evidence
	if ev.Source != SeqEvidenceDI {
		t.Fatalf("应有 DI 证据，got %+v", ev)
	}
	if ev.SymbolID != "symbol:go:example.com/x/di:(Store).Add" {
		t.Errorf("应具体化到 di.(Store).Add，got %q", ev.SymbolID)
	}
	if ev.RegisterFile != "di/di.go" || ev.RegisterSite == 0 {
		t.Errorf("证据应带跨包注册站点，got file=%q line=%d", ev.RegisterFile, ev.RegisterSite)
	}
}

// TestSeqDICrossPackageCachedPerRequest：同一请求里多处字段注入只定位一次
// 跨包引导函数（空结果/命中都缓存）。
func TestSeqDICrossPackageCachedPerRequest(t *testing.T) {
	acts, dir := seedDICrossPkgRepo(t)
	counter := &countingDIFuncsReader{Reader: acts.repo}
	acts.repo = counter
	if _, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/x/app:(Handler).Handle", RepoAbs: dir, Depth: 4}); err != nil {
		t.Fatalf("CodeSequence: %v", err)
	}
	if counter.calls != 1 {
		t.Errorf("跨包注册点定位应只查一次，got %d", counter.calls)
	}
}

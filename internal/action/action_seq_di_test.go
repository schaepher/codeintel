package action

// todo22 §3.3 测试：DI 字段注入的注册证据（注解 key / 注册站点 / AOP 包装），
// 以及"证据不足保持未知"（动态 key、多重注册、非引导注册、条件注册）。

import (
	"strings"
	"testing"
)

// TestSeqDIAnnotationRegistration：字段注解 + init 注册 → 具体化到实现方法。
func TestSeqDIAnnotationRegistration(t *testing.T) {
	src := `package m

import "context"

type Store interface{ Add(ctx context.Context) error }

type sqlStore struct{}

func (s *sqlStore) Add(ctx context.Context) error { InsertRow(); return nil }

func InsertRow() {}

type Handler struct {
	// @inject store
	store Store
}

func Register(key string, impl any) {}

func init() { Register("store", &sqlStore{}) }

func (h *Handler) Handle(ctx context.Context) error {
	return h.store.Add(ctx)
}
`
	const pkg = "symbol:go:example.com/m:"
	calls := map[string]string{"h.store.Add": pkg + "(Store).Add", "InsertRow": pkg + "InsertRow"}
	acts, dir := seedDIRepo(t, src, calls)
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "Handle", RepoAbs: dir, Depth: 4})
	if err != nil {
		t.Fatalf("CodeSequence: %v", err)
	}
	nodes := seqCallNodesNamed(root, "h.store.Add")
	if len(nodes) != 1 {
		t.Fatalf("应有 1 个 h.store.Add 节点，got %d", len(nodes))
	}
	ev := nodes[0].Evidence
	if ev == nil || ev.Source != SeqEvidenceDI {
		t.Fatalf("应有 DI 证据，got %+v", ev)
	}
	if !strings.Contains(ev.Reason, "di-field") {
		t.Errorf("reason 应含 di-field，got %q", ev.Reason)
	}
	if ev.SymbolID != pkg+"(sqlStore).Add" {
		t.Errorf("应具体化到 (sqlStore).Add，got %q", ev.SymbolID)
	}
	if ev.RegisterSite == 0 || ev.RegisterFile == "" {
		t.Errorf("证据应带注册站点，got file=%q line=%d", ev.RegisterFile, ev.RegisterSite)
	}
	if got := seqCallNodesNamed(nodes[0], "InsertRow"); len(got) != 1 {
		t.Errorf("应展开实现方法体（InsertRow 一次），got %d", len(got))
	}
}

// TestSeqDITagRegistration：struct tag 形式同样被识别（不假设只有 tag）。
func TestSeqDITagRegistration(t *testing.T) {
	src := `package m

import "context"

type Store interface{ Add(ctx context.Context) error }

type memStore struct{}

func (s *memStore) Add(ctx context.Context) error { MemWrite(); return nil }

func MemWrite() {}

type Handler struct {
	store Store ` + "`inject:\"mem\"`" + `
}

func Register(key string, impl any) {}

func Setup() { Register("mem", &memStore{}) }

func (h *Handler) Handle(ctx context.Context) error { return h.store.Add(ctx) }
`
	const pkg = "symbol:go:example.com/m:"
	calls := map[string]string{"h.store.Add": pkg + "(Store).Add"}
	acts, dir := seedDIRepo(t, src, calls)
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "Handle", RepoAbs: dir, Depth: 4})
	if err != nil {
		t.Fatalf("CodeSequence: %v", err)
	}
	nodes := seqCallNodesNamed(root, "h.store.Add")
	if len(nodes) != 1 || nodes[0].Evidence == nil || nodes[0].Evidence.SymbolID != pkg+"(memStore).Add" {
		t.Fatalf("tag 注解应具体化到 (memStore).Add，got %+v", nodes)
	}
	if got := seqCallNodesNamed(nodes[0], "MemWrite"); len(got) != 1 {
		t.Errorf("应展开 MemWrite，got %d", len(got))
	}
}

// TestSeqDIInsufficientEvidence：证据不足必须保持未知，不制造实现。
func TestSeqDIInsufficientEvidence(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"动态 key", `func init() { k := "store"; Register(k, &sqlStore{}) }`},
		{"多重注册", `func init() { Register("store", &sqlStore{}); Register("store", &memStore{}) }`},
		{"非引导函数注册", `func RegisterLater() { Register("store", &sqlStore{}) }`},
		{"条件注册", `func init() { if flag { Register("store", &sqlStore{}) } }`},
	}
	for _, c := range cases {
		src := `package m

import "context"

type Store interface{ Add(ctx context.Context) error }

type sqlStore struct{}

func (s *sqlStore) Add(ctx context.Context) error { SqlRow() }

func SqlRow() {}

type memStore struct{}

func (s *memStore) Add(ctx context.Context) error { MemRow() }

func MemRow() {}

type Handler struct {
	// @inject store
	store Store
}

var flag bool

func Register(key string, impl any) {}

` + c.body + `

func (h *Handler) Handle(ctx context.Context) error { return h.store.Add(ctx) }
`
		const pkg = "symbol:go:example.com/m:"
		calls := map[string]string{"h.store.Add": pkg + "(Store).Add"}
		acts, dir := seedDIRepo(t, src, calls)
		root, err := acts.CodeSequence(CodeSequenceRequest{Target: "Handle", RepoAbs: dir, Depth: 4})
		if err != nil {
			t.Fatalf("%s: CodeSequence: %v", c.name, err)
		}
		for _, forbidden := range []string{"SqlRow", "MemRow"} {
			if got := seqCallNodesNamed(root, forbidden); len(got) != 0 {
				t.Errorf("%s: 证据不足时不得展开实现（%s 出现 %d 次）", c.name, forbidden, len(got))
			}
		}
		// 动态 key / 非引导 / 条件注册：没有 DI 证据 → 不写 DI 符号
		nodes := seqCallNodesNamed(root, "h.store.Add")
		if len(nodes) == 1 && nodes[0].Evidence != nil &&
			nodes[0].Evidence.Source == SeqEvidenceDI && nodes[0].Evidence.SymbolID != "" {
			t.Errorf("%s: 不应产出确定的 DI 实现，got %+v", c.name, nodes[0].Evidence)
		}
		if c.name == "多重注册" {
			if len(nodes) != 1 || nodes[0].Stop == nil || !strings.Contains(nodes[0].Stop.Detail, "多重") {
				t.Errorf("多重注册应给明确 unknown（含原因），got %+v", nodes)
			}
		}
	}
}

// TestSeqDIAOPWrapper：注册的是包装器（AOP）→ 目标是包装器返回类型，
// 证据标 aop-wrapper，且不跳过包装层直接接业务方法。
func TestSeqDIAOPWrapper(t *testing.T) {
	src := `package m

import "context"

type Store interface{ Add(ctx context.Context) error }

type txStore struct{ inner Store }

func (s *txStore) Add(ctx context.Context) error { TxBegin(); s.inner.Add(ctx); return nil }

func TxBegin() {}

type sqlStore struct{}

func (s *sqlStore) Add(ctx context.Context) error { SqlRow() }

func SqlRow() {}

type Handler struct {
	// @inject store
	store Store
}

func Register(key string, impl any) {}

func Wrap(s Store) Store { return &txStore{inner: s} }

func init() { Register("store", Wrap(&sqlStore{})) }

func (h *Handler) Handle(ctx context.Context) error { return h.store.Add(ctx) }
`
	const pkg = "symbol:go:example.com/m:"
	calls := map[string]string{"h.store.Add": pkg + "(Store).Add"}
	acts, dir := seedDIRepo(t, src, calls)
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "Handle", RepoAbs: dir, Depth: 4})
	if err != nil {
		t.Fatalf("CodeSequence: %v", err)
	}
	nodes := seqCallNodesNamed(root, "h.store.Add")
	if len(nodes) != 1 || nodes[0].Evidence == nil {
		t.Fatalf("应有 h.store.Add 证据，got %+v", nodes)
	}
	ev := nodes[0].Evidence
	if ev.Source != SeqEvidenceDI || !strings.Contains(ev.Reason, "aop-wrapper") {
		t.Errorf("AOP 注册应标 di-aop-wrapper，got %+v", ev)
	}
	if !strings.HasSuffix(ev.SymbolID, "(txStore).Add") {
		t.Errorf("目标应是包装器方法 (txStore).Add，got %q", ev.SymbolID)
	}
	// 包装层必须先于业务方法（TxBegin 出现，业务实现不在注册点被直接接上）
	if got := seqCallNodesNamed(nodes[0], "TxBegin"); len(got) != 1 {
		t.Errorf("应展开包装器函数体（TxBegin 一次），got %d", len(got))
	}
}

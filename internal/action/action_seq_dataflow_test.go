package action

// R97-2 测试：receiver 字段数据流具体化——s.manager.SubmitOrder 的
// s.manager 赋值来源（构造函数 &orderManagerImpl{}）→ 直接落到具体
// 实现方法（优先于接口类型匹配）。

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// TestReceiverFieldDataflow：字段赋值来源（摘要 direct_write + 源码
// 赋值表达式）→ 具体化到 (orderManagerImpl).SubmitOrder。
func TestReceiverFieldDataflow(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `package m

type IManager interface {
	SubmitOrder() error
}

type orderManagerImpl struct{}

func (o *orderManagerImpl) SubmitOrder() error { return nil }

type orderServiceImpl struct {
	manager IManager
}

// 构造函数：字段赋值来源（数据流锚点）
func NewOrderServiceImpl() *orderServiceImpl {
	s := &orderServiceImpl{}
	s.manager = &orderManagerImpl{}
	return s
}

func (s *orderServiceImpl) SubmitOrder() error {
	return s.manager.SubmitOrder()
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:IManager", Kind: domain.KindInterface, Name: "IManager"},
		{ID: "symbol:go:example.com/m:(IManager).SubmitOrder", Kind: domain.KindMethod, Name: "(IManager).SubmitOrder"},
		{ID: "symbol:go:example.com/m:orderManagerImpl", Kind: domain.KindStruct, Name: "orderManagerImpl"},
		{ID: "symbol:go:example.com/m:(orderManagerImpl).SubmitOrder", Kind: domain.KindMethod, Name: "(orderManagerImpl).SubmitOrder", FilePath: "main.go", LineStart: 9},
		{ID: "symbol:go:example.com/m:orderServiceImpl", Kind: domain.KindStruct, Name: "orderServiceImpl"},
		{ID: "symbol:go:example.com/m:(orderServiceImpl).SubmitOrder", Kind: domain.KindMethod, Name: "(orderServiceImpl).SubmitOrder", FilePath: "main.go", LineStart: 22},
		{ID: "symbol:go:example.com/m:NewOrderServiceImpl", Kind: domain.KindFunction, Name: "NewOrderServiceImpl", FilePath: "main.go", LineStart: 16},
	}, []*domain.Fact{
		// SubmitOrder 内调 s.manager.SubmitOrder（line 27——接口方法）
		{SourceID: "symbol:go:example.com/m:(orderServiceImpl).SubmitOrder",
			TargetID: "symbol:go:example.com/m:(IManager).SubmitOrder",
			Kind:     domain.FactCalls, Confidence: 1.0, Metadata: map[string]any{"line_num": 23}},
		// 接口实现
		{SourceID: "symbol:go:example.com/m:IManager", TargetID: "symbol:go:example.com/m:orderManagerImpl",
			Kind: domain.FactImplements, Confidence: 1.0},
	}, []*domain.FunctionFieldSummary{
		// 构造函数 direct_write：字段 manager 的写入（数据流锚点）
		{FunctionID: "symbol:go:example.com/m:NewOrderServiceImpl",
			AccessKind: domain.SummaryDirectWrite, FieldPath: ".orderServiceImpl.manager",
			InstancePath: "s.manager", LineStart: 18},
	}); err != nil {
		t.Fatal(err)
	}
	acts := New(sqlite.NewRepo(db))
	// 数据流具体化：字段赋值来源（构造函数 &orderManagerImpl{}）→
	// (orderManagerImpl).SubmitOrder（而非接口 fallback）
	if got := acts.receiverFieldImpl(CodeSequenceRequest{RepoAbs: dir}, "orderServiceImpl", "manager", "SubmitOrder"); got != "symbol:go:example.com/m:(orderManagerImpl).SubmitOrder" {
		t.Fatalf("receiverFieldImpl = %q; want (orderManagerImpl).SubmitOrder", got)
	}
	root, err := acts.CodeSequence(CodeSequenceRequest{Target: "symbol:go:example.com/m:(orderServiceImpl).SubmitOrder", RepoAbs: dir, Depth: 2})
	if err != nil {
		t.Fatal(err)
	}
	// 子调用应为具体实现方法（数据流：字段赋值 &orderManagerImpl{}）
	// ——而非接口 fallback
	if root == nil || len(root.Nodes) == 0 {
		t.Fatalf("无子调用:\n%+v", root)
	}
	child := root.Nodes[0]
	if child.Label != "s.manager.SubmitOrder" {
		t.Fatalf("子调用 = %q; want s.manager.SubmitOrder", child.Label)
	}
	// 实现方法内部无调用（return nil）——Nodes 空为正确；数据流命中
	// 由 receiverFieldImpl 断言覆盖（避免接口 fallback 走 (IManager)
	// .SubmitOrder——其 FilePath 空无法展开，Nodes 同样空但语义不同）
}

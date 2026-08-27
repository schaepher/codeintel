package action

// P0-7 测试（用户实测场景）：接口实现由外部 DI 框架注入构造器参数——
// NewOrderServiceImpl(manager IOrderManager) 只做 manager: manager 赋值
// （参数注入，函数内无显式创建）。s.manager.SubmitOrder() 数据流查询
// 失败（赋值来源是参数）→ 应 fallback 接口实现枚举（InterfaceMethodImpl，
// 业务实现优先、grpc 实现排后）→ 展开到 (orderManagerImpl).SubmitOrder。

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// TestSeqParamDI：参数注入形态——构造器参数是接口、字段赋值自参数。
func TestSeqParamDI(t *testing.T) {
	src := `package m

type IOrderManager interface {
	SubmitOrder() error
}

type orderManagerImpl struct{}

func (o *orderManagerImpl) SubmitOrder() error { return nil }

type orderServiceImpl struct {
	manager IOrderManager
}

// 构造器：Manager 接口由外部 DI 框架注入参数
func NewOrderServiceImpl(manager IOrderManager) *orderServiceImpl {
	return &orderServiceImpl{manager: manager}
}

func (s *orderServiceImpl) SubmitOrder() error {
	return s.manager.SubmitOrder()
}
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(seqDataflowGoMod), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:IOrderManager", Kind: domain.KindInterface, Name: "IOrderManager"},
		{ID: "symbol:go:example.com/m:(IOrderManager).SubmitOrder", Kind: domain.KindMethod, Name: "(IOrderManager).SubmitOrder", FilePath: "main.go", LineStart: 4},
		{ID: "symbol:go:example.com/m:orderManagerImpl", Kind: domain.KindStruct, Name: "orderManagerImpl"},
		{ID: "symbol:go:example.com/m:(orderManagerImpl).SubmitOrder", Kind: domain.KindMethod, Name: "(orderManagerImpl).SubmitOrder", FilePath: "main.go", LineStart: 9},
		{ID: "symbol:go:example.com/m:orderServiceImpl", Kind: domain.KindStruct, Name: "orderServiceImpl"},
		{ID: "symbol:go:example.com/m:(orderServiceImpl).SubmitOrder", Kind: domain.KindMethod, Name: "(orderServiceImpl).SubmitOrder", FilePath: "main.go", LineStart: 20},
		{ID: "symbol:go:example.com/m:NewOrderServiceImpl", Kind: domain.KindFunction, Name: "NewOrderServiceImpl", FilePath: "main.go", LineStart: 16},
	}, []*domain.Fact{
		// s.manager.SubmitOrder（line 21——接口方法；数据流查不到：
		// 赋值来源是参数 manager）
		{SourceID: "symbol:go:example.com/m:(orderServiceImpl).SubmitOrder",
			TargetID: "symbol:go:example.com/m:(IOrderManager).SubmitOrder",
			Kind:     domain.FactCalls, Confidence: 1.0, Metadata: map[string]any{"line_num": 21}},
		// 接口实现（业务实现——外部 DI 框架注入的就是它）
		{SourceID: "symbol:go:example.com/m:IOrderManager", TargetID: "symbol:go:example.com/m:orderManagerImpl",
			Kind: domain.FactImplements, Confidence: 1.0},
	}, []*domain.FunctionFieldSummary{
		// 构造器 direct_write：字段 manager 赋值自参数（数据流锚点——
		// fieldWriteTypes 对 Ident 参数无初始化 → 返回空 → fallback）
		{FunctionID: "symbol:go:example.com/m:NewOrderServiceImpl",
			AccessKind: domain.SummaryDirectWrite, FieldPath: ".orderServiceImpl.manager",
			InstancePath: "s.manager", LineStart: 16},
	}); err != nil {
		t.Fatal(err)
	}
	acts := New(sqlite.NewRepo(db))
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/m:(orderServiceImpl).SubmitOrder", RepoAbs: dir, Depth: 2})
	if err != nil {
		t.Fatal(err)
	}
	if root == nil || len(root.Nodes) == 0 {
		t.Fatalf("无子调用:\n%+v", root)
	}
	child := root.Nodes[0]
	if child.Label != "s.manager.SubmitOrder" {
		t.Fatalf("子调用 = %q; want s.manager.SubmitOrder", child.Label)
	}
	// 参数注入形态：数据流（字段赋值自参数）失败 → CalleesConcrete
	// （ResolveIfaceCalls——implements 枚举）已把调用边 target 具体化
	// 到业务实现（Type = m.orderManagerImpl——外部 DI 注入的实现）
	if child.Type != "m.orderManagerImpl" {
		t.Errorf("Type = %q; want m.orderManagerImpl（CalleesConcrete 接口具体化——外部 DI 注入的实现）", child.Type)
	}
}

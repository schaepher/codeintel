package action

import "os"
import "path/filepath"
import "testing"
import "github.com/schaepher/codeintel/internal/domain"
import "github.com/schaepher/codeintel/internal/infrastructure/sqlite"

// TestSeqNodeImplType：P0-5——数据流具体化命中时 ImplType 记录实现
// 类型、Type 保留声明（接口）类型——参与者第二行"声明接口 → 数据流
// 实现"双行显示。
func TestSeqNodeImplType(t *testing.T) {
	src := `package m

type IManager interface {
	SubmitOrder() error
}

type orderManagerImpl struct{}

func (o *orderManagerImpl) SubmitOrder() error { return nil }

type orderServiceImpl struct {
	manager IManager
}

func NewOrderServiceImpl() *orderServiceImpl {
	s := &orderServiceImpl{}
	s.manager = &orderManagerImpl{}
	return s
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
		{ID: "symbol:go:example.com/m:IManager", Kind: domain.KindInterface, Name: "IManager"},
		{ID: "symbol:go:example.com/m:(IManager).SubmitOrder", Kind: domain.KindMethod, Name: "(IManager).SubmitOrder", FilePath: "main.go", LineStart: 2},
		{ID: "symbol:go:example.com/m:orderManagerImpl", Kind: domain.KindStruct, Name: "orderManagerImpl"},
		{ID: "symbol:go:example.com/m:(orderManagerImpl).SubmitOrder", Kind: domain.KindMethod, Name: "(orderManagerImpl).SubmitOrder", FilePath: "main.go", LineStart: 8},
		{ID: "symbol:go:example.com/m:orderServiceImpl", Kind: domain.KindStruct, Name: "orderServiceImpl"},
		{ID: "symbol:go:example.com/m:(orderServiceImpl).SubmitOrder", Kind: domain.KindMethod, Name: "(orderServiceImpl).SubmitOrder", FilePath: "main.go", LineStart: 21},
		{ID: "symbol:go:example.com/m:NewOrderServiceImpl", Kind: domain.KindFunction, Name: "NewOrderServiceImpl", FilePath: "main.go", LineStart: 15},
	}, []*domain.Fact{

		{SourceID: "symbol:go:example.com/m:(orderServiceImpl).SubmitOrder",
			TargetID: "symbol:go:example.com/m:(IManager).SubmitOrder",
			Kind:     domain.FactCalls, Confidence: 1.0, Metadata: map[string]any{"line_num": 22}},
		{SourceID: "symbol:go:example.com/m:IManager", TargetID: "symbol:go:example.com/m:orderManagerImpl",
			Kind: domain.FactImplements, Confidence: 1.0},
	}, []*domain.FunctionFieldSummary{
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

	if child.Type != "m.orderManagerImpl" {
		t.Errorf("Type = %q; want m.orderManagerImpl（CalleesConcrete 具体化）", child.Type)
	}
	if child.DeclType != "IManager" {
		t.Errorf("DeclType = %q; want IManager（receiver 字段声明接口类型——字段声明原文）", child.DeclType)
	}
	if child.ImplType != "m.orderManagerImpl" {
		t.Errorf("ImplType = %q; want m.orderManagerImpl（数据流实现类型）", child.ImplType)
	}
}

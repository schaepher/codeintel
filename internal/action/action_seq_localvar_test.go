package action

// P0-6 测试（用户实测反馈）：调用点 X 是局部变量时的 DI 注入具体化——
// m := newOrderManager()（构造器返回接口、函数体 return 真实实现）后
// m.SubmitOrder() 应具体化到 (orderManagerImpl).SubmitOrder。R97-2 只
// 覆盖 receiver 字段（s.manager）形态，局部变量形态缺失。

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// TestSeqLocalVarDI：局部变量初始化自构造器（返回接口 + return 具体
// 实现）→ 调用接口方法 → 具体化到实现（ImplType + 展开）。
func TestSeqLocalVarDI(t *testing.T) {
	src := `package m

type IManager interface {
	SubmitOrder() error
}

type orderManagerImpl struct{}

func (o *orderManagerImpl) SubmitOrder() error { return nil }

// DI 构造器：返回接口，函数体 return 真实实现
func newOrderManager() IManager {
	return &orderManagerImpl{}
}

type orderServiceImpl struct {
	manager IManager
}

func (s *orderServiceImpl) SubmitOrder() error {
	m := newOrderManager()
	return m.SubmitOrder()
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
		{ID: "symbol:go:example.com/m:(IManager).SubmitOrder", Kind: domain.KindMethod, Name: "(IManager).SubmitOrder", FilePath: "main.go", LineStart: 4},
		{ID: "symbol:go:example.com/m:orderManagerImpl", Kind: domain.KindStruct, Name: "orderManagerImpl"},
		{ID: "symbol:go:example.com/m:(orderManagerImpl).SubmitOrder", Kind: domain.KindMethod, Name: "(orderManagerImpl).SubmitOrder", FilePath: "main.go", LineStart: 9},
		{ID: "symbol:go:example.com/m:newOrderManager", Kind: domain.KindFunction, Name: "newOrderManager", FilePath: "main.go", LineStart: 12},
		{ID: "symbol:go:example.com/m:orderServiceImpl", Kind: domain.KindStruct, Name: "orderServiceImpl"},
		{ID: "symbol:go:example.com/m:(orderServiceImpl).SubmitOrder", Kind: domain.KindMethod, Name: "(orderServiceImpl).SubmitOrder", FilePath: "main.go", LineStart: 20},
	}, []*domain.Fact{
		// m.SubmitOrder（line 22——接口方法；CalleesConcrete 无实现
		// 追踪时 target 为接口方法）
		{SourceID: "symbol:go:example.com/m:(orderServiceImpl).SubmitOrder",
			TargetID: "symbol:go:example.com/m:(IManager).SubmitOrder",
			Kind:     domain.FactCalls, Confidence: 1.0, Metadata: map[string]any{"line_num": 22}},
		{SourceID: "symbol:go:example.com/m:IManager", TargetID: "symbol:go:example.com/m:orderManagerImpl",
			Kind: domain.FactImplements, Confidence: 1.0},
	}, nil); err != nil {
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
	// 找 m.SubmitOrder 节点（Nodes[0] 是 m := newOrderManager() 的 RHS
	// 调用——赋值内调用也生成步骤）
	var child *CodeSeqNode
	for _, n := range root.Nodes {
		if n.Label == "m.SubmitOrder" {
			child = n
			break
		}
	}
	if child == nil {
		t.Fatalf("未找到 m.SubmitOrder 子调用:\n%+v", root.Nodes)
	}
	// 局部变量 DI 注入 → 具体化到构造器 return 的真实实现
	if child.ImplType != "m.orderManagerImpl" {
		t.Errorf("ImplType = %q; want m.orderManagerImpl（局部变量初始化自 newOrderManager）", child.ImplType)
	}
	// 展开到实现方法内部（(orderManagerImpl).SubmitOrder 体为空——
	// 展开成功即 Nodes 存在；与接口方法（FilePath 存在但无方法体）
	// 语义区分由 ImplType 断言覆盖）
}

// TestSeqLocalVarLiteral：局部变量直接字面量（m := &orderManagerImpl{}）
// → 同样具体化（变量链形态兜底）。
func TestSeqLocalVarLiteral(t *testing.T) {
	src := `package m

type IManager interface {
	SubmitOrder() error
}

type orderManagerImpl struct{}

func (o *orderManagerImpl) SubmitOrder() error { return nil }

type orderServiceImpl struct {
	manager IManager
}

func (s *orderServiceImpl) SubmitOrder() error {
	m := &orderManagerImpl{}
	return m.SubmitOrder()
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
		{ID: "symbol:go:example.com/m:(IManager).SubmitOrder", Kind: domain.KindMethod, Name: "(IManager).SubmitOrder", FilePath: "main.go", LineStart: 4},
		{ID: "symbol:go:example.com/m:orderManagerImpl", Kind: domain.KindStruct, Name: "orderManagerImpl"},
		{ID: "symbol:go:example.com/m:(orderManagerImpl).SubmitOrder", Kind: domain.KindMethod, Name: "(orderManagerImpl).SubmitOrder", FilePath: "main.go", LineStart: 9},
		{ID: "symbol:go:example.com/m:orderServiceImpl", Kind: domain.KindStruct, Name: "orderServiceImpl"},
		{ID: "symbol:go:example.com/m:(orderServiceImpl).SubmitOrder", Kind: domain.KindMethod, Name: "(orderServiceImpl).SubmitOrder", FilePath: "main.go", LineStart: 15},
	}, []*domain.Fact{
		{SourceID: "symbol:go:example.com/m:(orderServiceImpl).SubmitOrder",
			TargetID: "symbol:go:example.com/m:(IManager).SubmitOrder",
			Kind:     domain.FactCalls, Confidence: 1.0, Metadata: map[string]any{"line_num": 17}},
		{SourceID: "symbol:go:example.com/m:IManager", TargetID: "symbol:go:example.com/m:orderManagerImpl",
			Kind: domain.FactImplements, Confidence: 1.0},
	}, nil); err != nil {
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
	if got := root.Nodes[0].ImplType; got != "m.orderManagerImpl" {
		t.Errorf("字面量局部变量 ImplType = %q; want m.orderManagerImpl", got)
	}
}

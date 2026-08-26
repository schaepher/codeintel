package action

// P0-3 测试：数据流具体化扩展——字段赋值来源从"仅字面量"扩展到
// 构造器调用（同包/跨包）、跨包类型（pkg.T）、变量链、条件分支
// 多候选（R97-2 只支持 &T{} 字面量）。

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// seedSeqDataflow 建库 + 写源码 + seed 摘要/边（复用 R97-2 形态）。
func seedSeqDataflow(t *testing.T, files map[string]string, summaryFn string, summaryField string) (*Actions, string) {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:IManager", Kind: domain.KindInterface, Name: "IManager"},
		{ID: "symbol:go:example.com/m:orderServiceImpl", Kind: domain.KindStruct, Name: "orderServiceImpl"},
		{ID: "symbol:go:example.com/m:orderManagerImpl", Kind: domain.KindStruct, Name: "orderManagerImpl"},
		{ID: "symbol:go:example.com/m:(orderManagerImpl).SubmitOrder", Kind: domain.KindMethod, Name: "(orderManagerImpl).SubmitOrder"},
		{ID: domain.CanonicalID(summaryFn), Kind: domain.KindFunction, Name: summaryFn, FilePath: "main.go", LineStart: 1},
	}, []*domain.Fact{
		{SourceID: "symbol:go:example.com/m:IManager", TargetID: "symbol:go:example.com/m:orderManagerImpl",
			Kind: domain.FactImplements, Confidence: 1.0},
	}, []*domain.FunctionFieldSummary{
		{FunctionID: domain.CanonicalID(summaryFn),
			AccessKind: domain.SummaryDirectWrite, FieldPath: summaryField,
			InstancePath: "s.manager", LineStart: 1},
	}); err != nil {
		t.Fatal(err)
	}
	return New(sqlite.NewRepo(db)), dir
}

const seqDataflowGoMod = "module example.com/m\n\ngo 1.21\n"

// TestFieldWriteCtorCall：构造器调用形态——s.manager = newOrderManager()
// → 函数声明返回接口、函数体 return 具体实现 → 具体化 orderManagerImpl。
func TestFieldWriteCtorCall(t *testing.T) {
	src := `package m

type IManager interface {
	SubmitOrder() error
}

type orderManagerImpl struct{}

func (o *orderManagerImpl) SubmitOrder() error { return nil }

// 构造器：返回接口，函数体 return 具体实现
func newOrderManager() IManager {
	return &orderManagerImpl{}
}

type orderServiceImpl struct {
	manager IManager
}

func NewOrderServiceImpl() *orderServiceImpl {
	s := &orderServiceImpl{}
	s.manager = newOrderManager()
	return s
}

func (s *orderServiceImpl) SubmitOrder() error {
	return s.manager.SubmitOrder()
}
`
	acts, dir := seedSeqDataflow(t, map[string]string{
		"go.mod":  seqDataflowGoMod,
		"main.go": src,
	}, "symbol:go:example.com/m:NewOrderServiceImpl", ".orderServiceImpl.manager")
	got := acts.receiverFieldImpl(CodeSequenceRequest{RepoAbs: dir}, "orderServiceImpl", "manager", "SubmitOrder")
	if got != "symbol:go:example.com/m:(orderManagerImpl).SubmitOrder" {
		t.Fatalf("构造器调用形态 = %q; want (orderManagerImpl).SubmitOrder", got)
	}
}

// TestFieldWriteCrossPkg：跨包类型形态——s.manager = &svc.OrderManagerImpl{}
// （SelectorExpr）→ import 映射解析包路径。
func TestFieldWriteCrossPkg(t *testing.T) {
	svc := `package svc

type OrderManagerImpl struct{}

func (o *OrderManagerImpl) SubmitOrder() error { return nil }
`
	mainSrc := `package m

import "example.com/m/svc"

type IManager interface {
	SubmitOrder() error
}

type orderServiceImpl struct {
	manager IManager
}

func NewOrderServiceImpl() *orderServiceImpl {
	s := &orderServiceImpl{}
	s.manager = &svc.OrderManagerImpl{}
	return s
}
`
	acts, dir := seedSeqDataflow(t, map[string]string{
		"go.mod":     seqDataflowGoMod,
		"main.go":    mainSrc,
		"svc/svc.go": svc,
	}, "symbol:go:example.com/m:NewOrderServiceImpl", ".orderServiceImpl.manager")
	got := acts.receiverFieldImpl(CodeSequenceRequest{RepoAbs: dir}, "orderServiceImpl", "manager", "SubmitOrder")
	if got != "symbol:go:example.com/m/svc:(OrderManagerImpl).SubmitOrder" {
		t.Fatalf("跨包类型形态 = %q; want svc:(OrderManagerImpl).SubmitOrder", got)
	}
}

// TestFieldWriteVarChain：变量链形态——m := &orderManagerImpl{}; s.manager = m
// → 回溯中间变量初始化。
func TestFieldWriteVarChain(t *testing.T) {
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
	m := &orderManagerImpl{}
	s.manager = m
	return s
}
`
	acts, dir := seedSeqDataflow(t, map[string]string{
		"go.mod":  seqDataflowGoMod,
		"main.go": src,
	}, "symbol:go:example.com/m:NewOrderServiceImpl", ".orderServiceImpl.manager")
	got := acts.receiverFieldImpl(CodeSequenceRequest{RepoAbs: dir}, "orderServiceImpl", "manager", "SubmitOrder")
	if got != "symbol:go:example.com/m:(orderManagerImpl).SubmitOrder" {
		t.Fatalf("变量链形态 = %q; want (orderManagerImpl).SubmitOrder", got)
	}
}

// TestFieldWriteBranchMultiCandidate：条件分支多候选——if/else 分别赋值
// 不同实现 → fieldWriteTypes 收集两个候选；receiverFieldImpl 命中第一个。
func TestFieldWriteBranchMultiCandidate(t *testing.T) {
	src := `package m

type IManager interface {
	SubmitOrder() error
}

type orderManagerImpl struct{}

func (o *orderManagerImpl) SubmitOrder() error { return nil }

type v2ManagerImpl struct{}

func (o *v2ManagerImpl) SubmitOrder() error { return nil }

type orderServiceImpl struct {
	manager IManager
}

func NewOrderServiceImpl() *orderServiceImpl {
	s := &orderServiceImpl{}
	if true {
		s.manager = &orderManagerImpl{}
	} else {
		s.manager = &v2ManagerImpl{}
	}
	return s
}
`
	acts, dir := seedSeqDataflow(t, map[string]string{
		"go.mod":  seqDataflowGoMod,
		"main.go": src,
	}, "symbol:go:example.com/m:NewOrderServiceImpl", ".orderServiceImpl.manager")
	// 多候选收集（两个分支都命中）
	cands := acts.fieldWriteTypes(CodeSequenceRequest{RepoAbs: dir}, "symbol:go:example.com/m:NewOrderServiceImpl", "manager")
	if len(cands) != 2 {
		t.Fatalf("条件分支候选数 = %d, want 2（if/else 两分支）: %+v", len(cands), cands)
	}
	// receiverFieldImpl 尝试全部候选，命中第一个可构造的方法
	got := acts.receiverFieldImpl(CodeSequenceRequest{RepoAbs: dir}, "orderServiceImpl", "manager", "SubmitOrder")
	if got != "symbol:go:example.com/m:(orderManagerImpl).SubmitOrder" {
		t.Fatalf("条件分支形态 = %q; want (orderManagerImpl).SubmitOrder", got)
	}
}

// TestSeqNodeImplType：P0-5——数据流具体化命中时 ImplType 记录实现
// 类型、Type 保留声明（接口）类型——参与者第二行"声明接口 → 数据流
// 实现"双行显示。

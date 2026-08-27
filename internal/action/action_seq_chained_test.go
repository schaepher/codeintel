package action

// 用户疑问验证：aa.NewService().DoSth() 链式调用——调用边存在
// （ast 层已验证），时序图能否继续正确展开到 DoSth 内部。

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// TestSeqChainedCallExpand：aa.NewService().DoSth() → 时序图节点
// Label 含完整链式文本、Actor = receiver（aa）、展开到 (Service).DoSth
// 内部（Nodes 含 s.helper）。
func TestSeqChainedCallExpand(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "aa"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(`package m

import "example.com/m/aa"

func run() {
	aa.NewService().DoSth()
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "aa", "aa.go"), []byte(`package aa

type Service struct{}

func (s *Service) DoSth() {
	s.helper()
}

func (s *Service) helper() {}

func NewService() *Service {
	return &Service{}
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:run", Kind: domain.KindFunction, Name: "run", FilePath: "main.go", LineStart: 5},
		{ID: "symbol:go:example.com/m/aa:Service", Kind: domain.KindStruct, Name: "Service"},
		{ID: "symbol:go:example.com/m/aa:(Service).DoSth", Kind: domain.KindMethod, Name: "(Service).DoSth", FilePath: "aa/aa.go", LineStart: 5},
		{ID: "symbol:go:example.com/m/aa:(Service).helper", Kind: domain.KindMethod, Name: "(Service).helper", FilePath: "aa/aa.go", LineStart: 9},
		{ID: "symbol:go:example.com/m/aa:NewService", Kind: domain.KindFunction, Name: "NewService", FilePath: "aa/aa.go", LineStart: 11},
	}, []*domain.Fact{
		// run 调 aa.NewService().DoSth()（main.go line 6）
		{SourceID: "symbol:go:example.com/m:run", TargetID: "symbol:go:example.com/m/aa:(Service).DoSth",
			Kind: domain.FactCalls, Confidence: 1.0, Metadata: map[string]any{"line_num": 6}},
		// DoSth 调 s.helper()（aa.go line 6）
		{SourceID: "symbol:go:example.com/m/aa:(Service).DoSth", TargetID: "symbol:go:example.com/m/aa:(Service).helper",
			Kind: domain.FactCalls, Confidence: 1.0, Metadata: map[string]any{"line_num": 6}},
	}, nil); err != nil {
		t.Fatal(err)
	}
	acts := New(sqlite.NewRepo(db))
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/m:run", RepoAbs: dir, Depth: 2})
	if err != nil {
		t.Fatal(err)
	}
	if root == nil || len(root.Nodes) == 0 {
		t.Fatalf("无子调用:\n%+v", root)
	}
	child := root.Nodes[0]
	// Label = 完整链式文本（X 源码 + 方法名）
	if child.Label != "aa.NewService().DoSth" {
		t.Errorf("Label = %q; want aa.NewService().DoSth", child.Label)
	}
	// Actor = 链式 receiver（aa 包）
	if child.Actor != "aa" {
		t.Errorf("Actor = %q; want aa（链式调用 receiver）", child.Actor)
	}
	// 展开到 (Service).DoSth 内部（Nodes 含 s.helper）
	if len(child.Nodes) == 0 {
		t.Fatalf("DoSth 内部未展开（Nodes 空）——链式调用无法继续展开:\n%+v", child)
	}
	if child.Nodes[0].Label != "s.helper" {
		t.Errorf("展开子调用 = %q; want s.helper", child.Nodes[0].Label)
	}
}

// TestSeqExternalCallNotExpanded（用户要求）：第三方依赖包调用建边
// 但时序图不深入展开——外部方法（c.Get，FilePath 空）节点保留、
// Nodes 空（不深入第三方内部）。
func TestSeqExternalCallNotExpanded(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(`package m

type Client interface{ Get() }

func run() {
	var c Client
	c.Get()
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:run", Kind: domain.KindFunction, Name: "run", FilePath: "main.go", LineStart: 5},
		// 外部依赖方法：FilePath 空（第三方包无 Syntax——不深入）
		{ID: "symbol:go:example.com/ext:(Client).Get", Kind: domain.KindMethod, Name: "(Client).Get"},
	}, []*domain.Fact{
		{SourceID: "symbol:go:example.com/m:run", TargetID: "symbol:go:example.com/ext:(Client).Get",
			Kind: domain.FactCalls, Confidence: 1.0, Metadata: map[string]any{"line_num": 6}},
	}, nil); err != nil {
		t.Fatal(err)
	}
	acts := New(sqlite.NewRepo(db))
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/m:run", RepoAbs: dir, Depth: 2})
	if err != nil {
		t.Fatal(err)
	}
	if root == nil || len(root.Nodes) == 0 {
		t.Fatalf("无子调用:\n%+v", root)
	}
	child := root.Nodes[0]
	if child.Label != "c.Get" {
		t.Fatalf("子调用 = %q; want c.Get", child.Label)
	}
	// 外部依赖：节点保留但不深入（Nodes 空——第三方内部不展开）
	if len(child.Nodes) != 0 {
		t.Errorf("外部调用不应深入展开（Nodes 空）——第三方依赖内部不解析: %+v", child.Nodes)
	}
}

// TestSeqChainedCallIfaceExpand（用户追问）：NewService 返回接口
// （type Service interface）→ aa.NewService().DoSth() 时序图应展开
// 到接口的具体实现 (svcImpl).DoSth 内部（构造器 return 追踪 +
// CalleesConcrete 具体化）。
func TestSeqChainedCallIfaceExpand(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "aa"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(`package m

import "example.com/m/aa"

func run() {
	aa.NewService().DoSth()
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "aa", "aa.go"), []byte(`package aa

type Service interface {
	DoSth()
}

type svcImpl struct{}

func (s *svcImpl) DoSth() {
	s.helper()
}

func (s *svcImpl) helper() {}

// 构造器：声明返回接口、函数体 return 具体实现
func NewService() Service {
	return &svcImpl{}
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:run", Kind: domain.KindFunction, Name: "run", FilePath: "main.go", LineStart: 5},
		{ID: "symbol:go:example.com/m/aa:Service", Kind: domain.KindInterface, Name: "Service"},
		{ID: "symbol:go:example.com/m/aa:(Service).DoSth", Kind: domain.KindMethod, Name: "(Service).DoSth", FilePath: "aa/aa.go", LineStart: 3},
		{ID: "symbol:go:example.com/m/aa:svcImpl", Kind: domain.KindStruct, Name: "svcImpl"},
		{ID: "symbol:go:example.com/m/aa:(svcImpl).DoSth", Kind: domain.KindMethod, Name: "(svcImpl).DoSth", FilePath: "aa/aa.go", LineStart: 9},
		{ID: "symbol:go:example.com/m/aa:(svcImpl).helper", Kind: domain.KindMethod, Name: "(svcImpl).helper", FilePath: "aa/aa.go", LineStart: 13},
		{ID: "symbol:go:example.com/m/aa:NewService", Kind: domain.KindFunction, Name: "NewService", FilePath: "aa/aa.go", LineStart: 16},
	}, []*domain.Fact{
		// 真实构建：接口分支 concreteMethodFor 追踪构造器 return →
		// 边直接指向具体实现 (svcImpl).DoSth
		{SourceID: "symbol:go:example.com/m:run", TargetID: "symbol:go:example.com/m/aa:(svcImpl).DoSth",
			Kind: domain.FactCalls, Confidence: 1.0, Metadata: map[string]any{"line_num": 6}},
		{SourceID: "symbol:go:example.com/m/aa:(svcImpl).DoSth", TargetID: "symbol:go:example.com/m/aa:(svcImpl).helper",
			Kind: domain.FactCalls, Confidence: 1.0, Metadata: map[string]any{"line_num": 10}},
		{SourceID: "symbol:go:example.com/m/aa:Service", TargetID: "symbol:go:example.com/m/aa:svcImpl",
			Kind: domain.FactImplements, Confidence: 1.0},
	}, nil); err != nil {
		t.Fatal(err)
	}
	acts := New(sqlite.NewRepo(db))
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/m:run", RepoAbs: dir, Depth: 2})
	if err != nil {
		t.Fatal(err)
	}
	if root == nil || len(root.Nodes) == 0 {
		t.Fatalf("无子调用:\n%+v", root)
	}
	child := root.Nodes[0]
	if child.Label != "aa.NewService().DoSth" {
		t.Fatalf("Label = %q; want aa.NewService().DoSth", child.Label)
	}
	// 接口返回：具体化到实现类型
	if child.Type != "aa.svcImpl" {
		t.Errorf("Type = %q; want aa.svcImpl（接口返回 → 构造器 return 追踪到实现）", child.Type)
	}
	// 展开到 (svcImpl).DoSth 内部
	if len(child.Nodes) == 0 || child.Nodes[0].Label != "s.helper" {
		t.Errorf("接口返回的链式调用应展开到实现内部（s.helper）:\n%+v", child.Nodes)
	}
}

// S6 测试：if err := f(); err != nil 同行——Init 里的调用生成步骤。
func TestSeqIfInitCall(t *testing.T) {
	src := `package m

func f() error { return nil }

func run() {
	if err := f(); err != nil {
		handle(err)
	}
}

func handle(e error) {}
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
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
		{ID: "symbol:go:example.com/m:run", Kind: domain.KindFunction, Name: "run", FilePath: "main.go", LineStart: 5},
		{ID: "symbol:go:example.com/m:f", Kind: domain.KindFunction, Name: "f", FilePath: "main.go", LineStart: 3},
	}, []*domain.Fact{
		{SourceID: "symbol:go:example.com/m:run", TargetID: "symbol:go:example.com/m:f",
			Kind: domain.FactCalls, Confidence: 1.0, Metadata: map[string]any{"line_num": 6, "pos": float64(85)}},
	}, nil); err != nil {
		t.Fatal(err)
	}
	acts := New(sqlite.NewRepo(db))
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/m:run", RepoAbs: dir, Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if root == nil || len(root.Nodes) == 0 {
		t.Fatalf("无子调用:\n%+v", root)
	}
	// Init 里的 f() 应生成调用步骤（在 branch 之前）
	if root.Nodes[0].Kind != "call" || root.Nodes[0].Label != "f" {
		t.Errorf("Init 调用应为第一个步骤（f）: %+v", root.Nodes[0])
	}
	if root.Nodes[1].Kind != "branch" {
		t.Errorf("第二个步骤应为 branch: %+v", root.Nodes[1])
	}
}

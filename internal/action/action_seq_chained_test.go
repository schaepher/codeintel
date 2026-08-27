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

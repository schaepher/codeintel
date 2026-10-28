//go:build integration

package integration

// todo25 §11.1：字段来源预算（构造器→接口 getter→包级单例→setter→跨包 init）。

import (
	"path/filepath"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
)

const todo25BudgetFlow = `package flow

import (
	"example.com/flow/api"
	_ "example.com/flow/impl"
)

type Service struct{ Biz api.API }

func NewService() *Service { return &Service{Biz: api.Get()} }
func (s *Service) Run()    { s.Biz.Run() }
func Entry()               { NewService().Run() }
`

const todo25BudgetAPI = `package api

type API interface{ Run() }

var current API

func Get() API      { return current }
func Inject(v API)  { current = v }
`

const todo25BudgetImpl = `package impl

import "example.com/flow/api"

type Impl struct{}

func (*Impl) Run() { Sink() }
func Sink()        {}

func init() { api.Inject(&Impl{}) }
`

func todo25FieldRepo(t *testing.T, implSrc, extra string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/flow\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "flow.go"), todo25BudgetFlow)
	writeFile(t, filepath.Join(dir, "api", "api.go"), todo25BudgetAPI)
	writeFile(t, filepath.Join(dir, "impl", "init.go"), implSrc)
	if extra != "" {
		writeFile(t, filepath.Join(dir, "impl", "extra.go"), extra)
	}
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "init")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit=%d", code)
	}
	return dir
}

func todo25BizNode(t *testing.T, dir string, depth int) *action.CodeSeqNode {
	t.Helper()
	root := seqTreeJSON(t, dir, "symbol:go:example.com/flow:(Service).Run", depth, "--evidence")
	var hit *action.CodeSeqNode
	walkSeq(root, func(n *action.CodeSeqNode) {
		if n.Label == "s.Biz.Run" {
			hit = n
		}
	})
	if hit == nil {
		t.Fatalf("缺少 s.Biz.Run: %+v", root)
	}
	return hit
}

func TestTodo25FieldSourceBudget(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := todo25FieldRepo(t, todo25BudgetImpl, "")
	for _, depth := range []int{1, 8} {
		n := todo25BizNode(t, dir, depth)
		if n.Evidence == nil || n.Evidence.SymbolID != "symbol:go:example.com/flow/impl:(Impl).Run" {
			t.Errorf("depth=%d 目标 = %+v; want Impl.Run", depth, n.Evidence)
		}
		if n.Evidence != nil && n.Evidence.Source != action.SeqEvidenceFieldFlow {
			t.Errorf("depth=%d 来源 = %q; want field_dataflow", depth, n.Evidence.Source)
		}
	}
	if !hasSymbolWith(todo25BizNode(t, dir, 8), "flow/impl:Sink") {
		t.Errorf("depth=8 应展开 Sink")
	}
}

func TestTodo25FieldSourceBudgetNegative(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	// 另一个具体实现写者 → 不得保留唯一 Impl 证明。
	impl2 := `
type Impl2 struct{}

func (*Impl2) Run() {}

func init() { api.Inject(&Impl2{}) }
`
	dir := todo25FieldRepo(t, todo25BudgetImpl, impl2)
	n := todo25BizNode(t, dir, 8)
	if n.Evidence != nil && n.Evidence.SymbolID == "symbol:go:example.com/flow/impl:(Impl).Run" && !n.Evidence.Ambiguous {
		t.Errorf("两个具体写者不得保留唯一 Impl: %+v", n.Evidence)
	}

	// 无 init 写入 → 不得借用 Impl。
	noWrite := `package impl

type Impl struct{}

func (*Impl) Run() {}
`
	dir2 := todo25FieldRepo(t, noWrite, "")
	n2 := todo25BizNode(t, dir2, 8)
	if n2.Evidence != nil && n2.Evidence.SymbolID == "symbol:go:example.com/flow/impl:(Impl).Run" {
		t.Errorf("无写入不应证明 Impl: %+v", n2.Evidence)
	}
}

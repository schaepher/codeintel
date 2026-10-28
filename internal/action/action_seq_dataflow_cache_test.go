package action

// todo22 P2 §7.3 测试：receiver 字段类型/工厂来源的请求内缓存——同一字段的
// 多处调用只查一次字段写入者、只解析一次写入函数源码（量化证据：查询次数），
// 结论不变（每处仍具体化到同一实现）。

import (
	"go/ast"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

type countingFieldWritersReader struct {
	Reader
	queries int
}

func (r *countingFieldWritersReader) GetFieldWriters(fieldPath string) ([]string, error) {
	r.queries++
	return r.Reader.GetFieldWriters(fieldPath)
}

const fieldCacheSrc = `package m

type IManager interface{ SubmitOrder() error }

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

func (s *orderServiceImpl) Run() error {
	if err := s.manager.SubmitOrder(); err != nil {
		return err
	}
	if err := s.manager.SubmitOrder(); err != nil {
		return err
	}
	return s.manager.SubmitOrder()
}
`

func seedFieldCacheRepo(t *testing.T) (*Actions, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(fieldCacheSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	lineOf := func(sub string, nth int) int {
		idx := -1
		for i := 0; i <= nth; i++ {
			j := strings.Index(fieldCacheSrc[idx+1:], sub)
			if j < 0 {
				t.Fatalf("缺少第 %d 个 %q", nth, sub)
			}
			idx += 1 + j
		}
		return strings.Count(fieldCacheSrc[:idx], "\n") + 1
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:IManager", Kind: domain.KindInterface, Name: "IManager"},
		{ID: "symbol:go:example.com/m:(IManager).SubmitOrder", Kind: domain.KindMethod, Name: "(IManager).SubmitOrder"},
		{ID: "symbol:go:example.com/m:orderManagerImpl", Kind: domain.KindStruct, Name: "orderManagerImpl"},
		{ID: "symbol:go:example.com/m:(orderManagerImpl).SubmitOrder", Kind: domain.KindMethod,
			Name: "(orderManagerImpl).SubmitOrder", FilePath: "main.go", LineStart: lineOf("func (o *orderManagerImpl) SubmitOrder", 0)},
		{ID: "symbol:go:example.com/m:orderServiceImpl", Kind: domain.KindStruct, Name: "orderServiceImpl"},
		{ID: "symbol:go:example.com/m:(orderServiceImpl).Run", Kind: domain.KindMethod,
			Name: "(orderServiceImpl).Run", FilePath: "main.go", LineStart: lineOf("func (s *orderServiceImpl) Run", 0)},
		{ID: "symbol:go:example.com/m:NewOrderServiceImpl", Kind: domain.KindFunction,
			Name: "NewOrderServiceImpl", FilePath: "main.go", LineStart: lineOf("func NewOrderServiceImpl", 0)},
	}
	var facts []*domain.Fact
	for i := 0; i < 3; i++ {
		facts = append(facts, &domain.Fact{
			SourceID: "symbol:go:example.com/m:(orderServiceImpl).Run",
			TargetID: "symbol:go:example.com/m:(IManager).SubmitOrder",
			Kind:     domain.FactCalls, Confidence: 1.0,
			Metadata: map[string]any{"line_num": lineOf("s.manager.SubmitOrder", i)},
		})
	}
	facts = append(facts, &domain.Fact{
		SourceID: "symbol:go:example.com/m:IManager", TargetID: "symbol:go:example.com/m:orderManagerImpl",
		Kind: domain.FactImplements, Confidence: 1.0,
	})
	summaries := []*domain.FunctionFieldSummary{{
		FunctionID: "symbol:go:example.com/m:NewOrderServiceImpl",
		AccessKind: domain.SummaryDirectWrite, FieldPath: ".orderServiceImpl.manager",
		InstancePath: "s.manager", LineStart: lineOf("s.manager =", 0),
	}}
	if _, err := r.SaveBatchStats(nodes, facts, summaries); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(&domain.BuildMeta{BuildID: "fieldcache1", ToolName: "test", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	return New(r), dir
}

// TestReceiverFieldTypeCachePerRequest：同一请求里同一字段 3 次调用 →
// GetFieldWriters 只查 1 次；三处都具体化到 orderManagerImpl。
func TestReceiverFieldTypeCachePerRequest(t *testing.T) {
	acts, dir := seedFieldCacheRepo(t)
	counter := &countingFieldWritersReader{Reader: acts.repo}
	acts.repo = counter
	root, err := acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/m:(orderServiceImpl).Run", RepoAbs: dir, Depth: 3})
	if err != nil {
		t.Fatalf("CodeSequence: %v", err)
	}
	var calls []*CodeSeqNode
	var walk func(nodes []*CodeSeqNode)
	walk = func(nodes []*CodeSeqNode) {
		for _, n := range nodes {
			if n.Kind == "call" && n.Label == "s.manager.SubmitOrder" {
				calls = append(calls, n)
			}
			walk(n.Nodes)
		}
	}
	walk(root.Nodes)
	if len(calls) != 3 {
		t.Fatalf("应有 3 个 s.manager.SubmitOrder 节点，got %d", len(calls))
	}
	for _, n := range calls {
		if n.ImplType != "m.orderManagerImpl" {
			t.Errorf("每处都应具体化到 m.orderManagerImpl，got %q", n.ImplType)
		}
	}
	if counter.queries != 1 {
		t.Errorf("同一字段的写入者查询应只 1 次（请求内缓存），got %d", counter.queries)
	}
	if root.Meta == nil || root.Meta.Timing == nil || root.Meta.Timing.CacheHits == 0 {
		t.Errorf("源码 AST 缓存应有命中（Timing.CacheHits>0），got %+v", root.Meta)
	}
	t.Logf("[field-cache] queries=%d cache_hits=%d cache_misses=%d",
		counter.queries, root.Meta.Timing.CacheHits, root.Meta.Timing.CacheMisses)
}

// TestIndexedConstructorReturnCache：跨包构造器返回类型按 (pkg,func,depth)
// 缓存（同一请求返回同一结果，不重复解析）。
func TestIndexedConstructorReturnCache(t *testing.T) {
	dir := t.TempDir()
	src := `package m

type A struct{}

func NewA() *A { return &A{} }
`
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
	defer db.Close()
	r := sqlite.NewRepo(db)
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{{
		ID: "symbol:go:example.com/m:NewA", Kind: domain.KindFunction, Name: "NewA",
		FilePath: "main.go", LineStart: 5,
	}}, nil, nil); err != nil {
		t.Fatal(err)
	}
	acts := New(r)
	req := CodeSequenceRequest{RepoAbs: dir,
		fileCache: map[string]*seqParsedFile{},
		funcRets:  map[string][]writeSource{}}
	first := indexedFuncReturnTypes(acts, req, "example.com/m", "NewA", 3)
	second := indexedFuncReturnTypes(acts, req, "example.com/m", "NewA", 3)
	if len(first) != 1 || first[0].typeName != "A" {
		t.Fatalf("构造器返回类型应为 A，got %+v", first)
	}
	if len(second) != 1 || &first[0] != &second[0] {
		t.Errorf("第二次应命中缓存（同一底层数组），got %+v vs %+v", first, second)
	}
}

// TestVarChainCacheKeyedByFile：同包两个不同文件里的同名变量不能命中同一
// 缓存条目（每个文件独立 FileSet，token 位置会重复——键必须用文件身份）。
func TestVarChainCacheKeyedByFile(t *testing.T) {
	acts := New(nil)
	req := CodeSequenceRequest{varChains: map[string][]writeSource{}}
	fset1, f1 := parseSeqFile(t, `package m

func F() {
	v := &A{}
	_ = v
}
`)
	_, f2 := parseSeqFile(t, `package m

func G() {
	v := &B{}
	_ = v
}
`)
	_ = fset1
	out1 := varChainTypes(acts, req, f1, importAliases(f1), "v", nil, 3, "example.com/m")
	out2 := varChainTypes(acts, req, f2, importAliases(f2), "v", nil, 3, "example.com/m")
	if len(out1) != 1 || out1[0].typeName != "A" {
		t.Errorf("文件 1 的 v 应为 A，got %+v", out1)
	}
	if len(out2) != 1 || out2[0].typeName != "B" {
		t.Errorf("文件 2 的 v 应为 B，got %+v", out2)
	}
}

// TestVarChainRespectsObjectIdentity：同一文件不同函数的同名局部变量不能
// 互相借用（todo23 §7.3：局部声明先按 AST 对象排除）。
func TestVarChainRespectsObjectIdentity(t *testing.T) {
	src := `package m

type A struct{}
type B struct{}

func F() {
	worker := &A{}
	_ = worker
}

func G() {
	worker := &B{}
	_ = worker
}
`
	fset, f := parseSeqFile(t, src)
	var objF, objG *ast.Object
	ast.Inspect(f, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != 1 {
			return true
		}
		id, ok := as.Lhs[0].(*ast.Ident)
		if !ok || id.Name != "worker" {
			return true
		}
		switch fset.Position(as.Pos()).Line {
		case 7:
			objF = id.Obj
		case 12:
			objG = id.Obj
		}
		return true
	})
	if objF == nil || objG == nil || objF == objG {
		t.Fatalf("夹具对象解析失败: %p %p", objF, objG)
	}
	acts := New(nil)
	req := CodeSequenceRequest{varChains: map[string][]writeSource{}}
	outF := varChainTypes(acts, req, f, importAliases(f), "worker", objF, 3, "example.com/m")
	outG := varChainTypes(acts, req, f, importAliases(f), "worker", objG, 3, "example.com/m")
	if len(outF) != 1 || outF[0].typeName != "A" {
		t.Errorf("F 的 worker 应为 A，got %+v", outF)
	}
	if len(outG) != 1 || outG[0].typeName != "B" {
		t.Errorf("G 的 worker 应为 B，got %+v", outG)
	}
}

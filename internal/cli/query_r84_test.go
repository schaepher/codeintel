package cli

// R84 测试：grpc 服务入口接口（动态入口——无直接 caller）不当作
// interface 节点停止解析：
//  1. codeSequence 入口是接口方法 → 具体化到实现方法再解析
//  2. chainSymbols BFS 遇到接口方法/类型 → 经 implements 边继续

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// seedIfaceEntryRepo fixture：接口方法（无方法体——grpc 服务入口形态）
// + implements 边 → 实现方法（有方法体，内部调用 helper）。
func seedIfaceEntryRepo(t *testing.T) string {
	t.Helper()
	dir := seedRepo(t)
	src := `package m

type Svc interface {
	Run() error
}

type svcImpl struct{}

func (s *svcImpl) Run() error {
	return helper()
}

func helper() error { return nil }
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
		// 接口 + 接口方法（无方法体——动态入口）
		{ID: "symbol:go:example.com/m:Svc", Kind: domain.KindInterface, Name: "Svc", FilePath: "main.go", LineStart: 3},
		{ID: "symbol:go:example.com/m:(Svc).Run", Kind: domain.KindMethod, Name: "(Svc).Run", FilePath: "main.go", LineStart: 4},
		// 实现类型 + 实现方法（有方法体）
		{ID: "symbol:go:example.com/m:svcImpl", Kind: domain.KindStruct, Name: "svcImpl", FilePath: "main.go", LineStart: 8},
		{ID: "symbol:go:example.com/m:(svcImpl).Run", Kind: domain.KindMethod, Name: "(svcImpl).Run", FilePath: "main.go", LineStart: 10},
		{ID: "symbol:go:example.com/m:helper", Kind: domain.KindFunction, Name: "helper", FilePath: "main.go", LineStart: 14},
	}, []*domain.Fact{
		// 接口 → 实现
		{SourceID: "symbol:go:example.com/m:Svc", TargetID: "symbol:go:example.com/m:svcImpl",
			Kind: domain.FactImplements, Confidence: 1.0},
		// 实现方法内部调用 helper
		{SourceID: "symbol:go:example.com/m:(svcImpl).Run", TargetID: "symbol:go:example.com/m:helper",
			Kind: domain.FactCalls, Confidence: 1.0, Metadata: map[string]any{"line_num": 11}},
	}, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestCodeSequenceIfaceEntry：接口方法作为查询入口（动态入口无方法体）
// → 具体化到实现方法后展开其内部调用。
func TestCodeSequenceIfaceEntry(t *testing.T) {
	dir := seedIfaceEntryRepo(t)
	acts, err := newTestActions(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	root := codeSequence(acts, dir, "symbol:go:example.com/m:(Svc).Run", 1)
	if root == nil {
		t.Fatal("codeSequence 接口方法入口应具体化到实现方法（nil 返回）")
	}
	// 实现方法内部调用了 helper——展开后应出现
	found := false
	var walk func(n *codeSeqNode)
	walk = func(n *codeSeqNode) {
		if strings.Contains(n.Label, "helper") {
			found = true
		}
		for _, c := range n.Nodes {
			walk(c)
		}
		if n.Else != nil {
			for _, c := range n.Else {
				walk(c)
			}
		}
	}
	walk(root)
	if !found {
		t.Errorf("接口方法入口应展开实现方法内部调用（helper 缺失）:\n%+v", root)
	}
}

// TestChainSymbolsIfaceConcrete：BFS 链上接口方法/类型 → 经 implements
// 边具体化到实现（不停止解析——grpc 服务入口动态分派语义）。
func TestChainSymbolsIfaceConcrete(t *testing.T) {
	dir := seedIfaceEntryRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
	acts, err2 := newTestActions(t, dir)
	if err2 != nil {
		t.Fatal(err2)
	}
	// 调用点：caller 调接口类型 + 接口方法（grpc handler 形态）
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:caller", Kind: domain.KindFunction, Name: "caller", FilePath: "main.go", LineStart: 1},
	}, []*domain.Fact{
		{SourceID: "symbol:go:example.com/m:caller", TargetID: "symbol:go:example.com/m:Svc",
			Kind: domain.FactCalls, Confidence: 1.0},
		{SourceID: "symbol:go:example.com/m:caller", TargetID: "symbol:go:example.com/m:(Svc).Run",
			Kind: domain.FactCalls, Confidence: 1.0},
	}, nil); err != nil {
		t.Fatal(err)
	}
	seen := chainSymbols(acts, r, "symbol:go:example.com/m:caller")
	for _, want := range []string{"symbol:go:example.com/m:svcImpl", "symbol:go:example.com/m:(svcImpl).Run", "symbol:go:example.com/m:helper"} {
		if !seen[want] {
			t.Errorf("chainSymbols 链应含接口具体化 %q; got %v", want, seen)
		}
	}
	// Unimplemented 桩不入链
	if seen["symbol:go:example.com/m:UnimplementedSvc"] {
		t.Errorf("Unimplemented 桩不应入链: %v", seen)
	}
}

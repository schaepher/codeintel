package action

// todo22 §3.3 测试基建：DI 夹具（源码 + 节点 + 方法调用边）。

import (
	"go/ast"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// seedDIRepo 写源码 + 节点（结构体/方法/接口）+ 方法调用边（带 pos）。
func seedDIRepo(t *testing.T, src string, calls map[string]string) (*Actions, string) {
	t.Helper()
	dir := writeSeqFixture(t, src)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)

	fset, f := parseSeqFile(t, src)
	var nodes []*domain.CodeEntity
	var edges []*domain.Fact
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		id := "symbol:go:example.com/m:" + fd.Name.Name
		kind := domain.KindFunction
		if fd.Recv != nil && len(fd.Recv.List) > 0 {
			id = "symbol:go:example.com/m:(" + recvTypeName(fd.Recv.List[0].Type) + ")." + fd.Name.Name
			kind = domain.KindMethod
		}
		nodes = append(nodes, &domain.CodeEntity{ID: domain.CanonicalID(id), Kind: kind,
			Name: fd.Name.Name, FilePath: "flow.go", LineStart: fset.Position(fd.Pos()).Line})
		if fd.Body == nil {
			continue
		}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			target, ok := calls[callLabel(fset, []byte(src), call.Fun)]
			if !ok {
				return true
			}
			edges = append(edges, &domain.Fact{
				SourceID: domain.CanonicalID(id), TargetID: domain.CanonicalID(target),
				Kind: domain.FactCalls, ToolSource: domain.ToolCodeGraph, Confidence: 0.8,
				Metadata: map[string]any{
					"line_num": fset.Position(call.Lparen).Line,
					"pos":      fset.Position(call.Lparen).Offset,
				}})
			return true
		})
	}
	// 调用边端点必须存在（否则 SaveBatchStats 静默跳过悬挂边）——
	// 接口方法没有 FuncDecl，需显式建节点。
	have := map[string]bool{}
	for _, n := range nodes {
		have[string(n.ID)] = true
	}
	for _, target := range calls {
		if have[target] {
			continue // 已有真实 FuncDecl 节点（保留真实行号——行号 1 会覆盖定位）
		}
		nodes = append(nodes, &domain.CodeEntity{ID: domain.CanonicalID(target),
			Kind: domain.KindMethod, Name: seqDeclNameOf(target), FilePath: "flow.go", LineStart: 1})
	}
	// 类型节点（struct/interface）供 implTypeShort / kind 判定
	for _, name := range []string{"Store", "sqlStore", "memStore", "Handler"} {
		if strings.Contains(src, "type "+name+" ") {
			kind := domain.KindStruct
			if name == "Store" {
				kind = domain.KindInterface
			}
			nodes = append(nodes, &domain.CodeEntity{ID: domain.CanonicalID("symbol:go:example.com/m:" + name),
				Kind: kind, Name: name, FilePath: "flow.go", LineStart: 1})
		}
	}
	if _, err := r.SaveBatchStats(nodes, edges, nil); err != nil {
		t.Fatalf("save: %v", err)
	}
	return New(r), dir
}

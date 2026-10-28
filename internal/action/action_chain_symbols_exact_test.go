package action

// todo22 P1 测试：ext-chain 的全链 BFS 不再一次性加载全量 calls/implements
// 边与全部节点 ID——逐节点用整数邻接查询，接口具体化用精确 implements
// 查询，方法存在性用精确 GetSymbol。语义与旧实现逐项等价（接口实现/
// Unimplemented 排除/首个实现规则）。

import (
	"context"
	"errors"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// chainStrictReader 包一层 Reader：拒绝全量边/全节点扫描，记录逐节点查询次数。
type chainStrictReader struct {
	Reader
	t *testing.T

	fullFacts       int // GetFactsByKinds（全量边）
	allImplements   int // GetImplementsEdges（全量 implements 边）
	allNodeIDs      int // AllNodeIDs（全节点）
	outgoingQueries int // GetOutgoingFactsByKinds（允许）
	exactImpls      int // InterfaceImplsExact（允许）
	exactSymbols    int // GetSymbol（方法存在性精确查询）
}

func (r *chainStrictReader) GetFactsByKinds(kinds ...string) ([]*domain.Fact, error) {
	r.fullFacts++
	return nil, errors.New("禁止：链 BFS 不得一次加载全量 calls/grpc_call 边")
}

func (r *chainStrictReader) GetImplementsEdges() ([]*domain.Fact, error) {
	r.allImplements++
	return nil, errors.New("禁止：链 BFS 不得加载全量 implements 边")
}

func (r *chainStrictReader) AllNodeIDs() ([]domain.CanonicalID, error) {
	r.allNodeIDs++
	return nil, errors.New("禁止：链 BFS 不得加载全部节点 ID")
}

func (r *chainStrictReader) GetOutgoingFactsByKinds(ctx context.Context, id domain.CanonicalID, kinds ...string) ([]*domain.Fact, error) {
	r.outgoingQueries++
	return r.Reader.GetOutgoingFactsByKinds(ctx, id, kinds...)
}

func (r *chainStrictReader) InterfaceImplsExact(ifaceID string) ([]string, error) {
	r.exactImpls++
	return r.Reader.InterfaceImplsExact(ifaceID)
}

// T16：精确接口查询的 ctx 版本（同一计数——两个入口同源）。
func (r *chainStrictReader) GetInterfaceImplements(ctx context.Context, ifaceID string) ([]domain.CanonicalID, error) {
	r.exactImpls++
	return r.Reader.GetInterfaceImplements(ctx, ifaceID)
}

func (r *chainStrictReader) GetSymbol(id domain.CanonicalID) (*domain.CodeEntity, error) {
	r.exactSymbols++
	return r.Reader.GetSymbol(id)
}

// TestChainSymbolsUsesExactAdjacency：链符号 BFS 只走逐节点精确查询，
// 结果与旧全量实现一致（接口方法具体化到实现、Unimplemented 桩不入链）。
func TestChainSymbolsUsesExactAdjacency(t *testing.T) {
	acts, dir := seedIfaceEntryRepo(t)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := sqlite.NewRepo(db)
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
	strict := &chainStrictReader{Reader: acts.repo, t: t}
	acts.repo = strict

	seen := chainSymbols(acts, "symbol:go:example.com/m:caller")
	for _, want := range []string{
		"symbol:go:example.com/m:svcImpl",
		"symbol:go:example.com/m:(svcImpl).Run",
		"symbol:go:example.com/m:helper",
	} {
		if !seen[want] {
			t.Errorf("链应含 %q; got %v", want, seen)
		}
	}
	if seen["symbol:go:example.com/m:UnimplementedSvc"] {
		t.Errorf("Unimplemented 桩不应入链: %v", seen)
	}
	if strict.fullFacts != 0 || strict.allImplements != 0 || strict.allNodeIDs != 0 {
		t.Errorf("不得全量扫描：GetFactsByKinds=%d GetImplementsEdges=%d AllNodeIDs=%d",
			strict.fullFacts, strict.allImplements, strict.allNodeIDs)
	}
	if strict.outgoingQueries == 0 || strict.exactImpls == 0 || strict.exactSymbols == 0 {
		t.Errorf("应使用逐节点精确查询：outgoing=%d exactImpls=%d exactSymbols=%d",
			strict.outgoingQueries, strict.exactImpls, strict.exactSymbols)
	}
	t.Logf("[chain] outgoing=%d exactImpls=%d exactSymbols=%d",
		strict.outgoingQueries, strict.exactImpls, strict.exactSymbols)
}

// TestChainGrpcHTTPUsesExactAdjacency：ext-chain 的 grpc/http 出站收集
// 也只查链上节点的出边（不再全量扫 grpc_call/http_call/calls）。
func TestChainGrpcHTTPUsesExactAdjacency(t *testing.T) {
	acts, _ := seedExtChainRepo(t)
	strict := &chainStrictReader{Reader: acts.repo, t: t}
	acts.repo = strict

	out, diags := chainGrpcHTTPDiag(acts, "symbol:go:example.com/m/daemon:(D).Run")
	if len(diags) != 0 {
		t.Errorf("不应有诊断: %+v", diags)
	}
	if len(out.Grpc) == 0 {
		t.Fatalf("应收集到 grpc 出站调用: %+v", out)
	}
	if strict.fullFacts != 0 || strict.allImplements != 0 || strict.allNodeIDs != 0 {
		t.Errorf("不得全量扫描：GetFactsByKinds=%d GetImplementsEdges=%d AllNodeIDs=%d",
			strict.fullFacts, strict.allImplements, strict.allNodeIDs)
	}
	if strict.outgoingQueries == 0 {
		t.Errorf("应逐节点查链上出边（0 次）")
	}
}

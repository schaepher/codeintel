package action

// todo22 §4.1 单测：RPC 调用点续接（调用点出站边 → 服务身份 → 注册实现），
// 以及"正常未命中不反复全库发现"的请求内缓存（接口实现缓存同口径）。

import (
	"go/ast"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

// countingRPCReader 统计 RPC 相关查询次数（其余走嵌入的 Reader）。
type countingRPCReader struct {
	Reader
	exact      int
	byProtocol int
	impls      int
	forwards   map[domain.CanonicalID]int
	duplicate  int // 同一源函数重复查询（请求内缓存失效）
}

func (c *countingRPCReader) GetRPCServicesByFullIdentity(protocol, full string) ([]*domain.CodeEntity, error) {
	c.exact++
	return c.Reader.GetRPCServicesByFullIdentity(protocol, full)
}

func (c *countingRPCReader) GetRPCServicesByProtocol(protocol string) ([]*domain.CodeEntity, error) {
	c.byProtocol++
	return c.Reader.GetRPCServicesByProtocol(protocol)
}

func (c *countingRPCReader) GetGrpcImplNodes(svcID domain.CanonicalID) ([]*domain.CodeEntity, error) {
	c.impls++
	return c.Reader.GetGrpcImplNodes(svcID)
}

func (c *countingRPCReader) GetRPCForwards(source domain.CanonicalID) ([]*domain.RPCForward, error) {
	if c.forwards == nil {
		c.forwards = map[domain.CanonicalID]int{}
	}
	c.forwards[source]++
	if c.forwards[source] > 1 {
		c.duplicate++
	}
	return c.Reader.GetRPCForwards(source)
}

const seqRPCSrc = `package m

type Client interface{ Apply(); Other() }

type Server struct{}

func Entry(c Client) {
	c.Apply()
	c.Apply()
	c.Other()
}

func (s *Server) Apply() { Noop() }

func (s *Server) Other() {}

func Noop() {}
`

// seedRPCRepo 写源码 + 节点 + grpc_call/grpc_impl/calls 边。
func seedRPCRepo(t *testing.T) (*Actions, *countingRPCReader) {
	t.Helper()
	dir := writeSeqFixture(t, seqRPCSrc)
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	r := sqlite.NewRepo(db)
	fset, f := parseSeqFile(t, seqRPCSrc)

	lineOf := func(name string) int {
		for _, decl := range f.Decls {
			if fd, ok := decl.(*ast.FuncDecl); ok && fd.Name.Name == name {
				return fset.Position(fd.Pos()).Line
			}
		}
		return 0
	}
	// 调用点位置（Entry 内按顺序）
	var entryCallLines, entryCallPos []int
	ast.Inspect(f, func(n ast.Node) bool {
		fd, ok := n.(*ast.FuncDecl)
		if !ok || fd.Name.Name != "Entry" {
			return true
		}
		ast.Inspect(fd.Body, func(m ast.Node) bool {
			if call, ok := m.(*ast.CallExpr); ok {
				entryCallLines = append(entryCallLines, fset.Position(call.Lparen).Line)
				entryCallPos = append(entryCallPos, fset.Position(call.Lparen).Offset)
			}
			return true
		})
		return false
	})
	if len(entryCallLines) != 3 {
		t.Fatalf("Entry 调用点数 = %d; want 3", len(entryCallLines))
	}

	nodes := []*domain.CodeEntity{
		{ID: "symbol:go:example.com/m:Entry", Kind: domain.KindFunction, Name: "Entry",
			FilePath: "flow.go", LineStart: lineOf("Entry")},
		{ID: "symbol:go:example.com/m:svc.AccountService", Kind: domain.KindGrpcService,
			Name: "svc.AccountService", FilePath: "flow.go", LineStart: 1,
			Properties: map[string]any{"service_name": "AccountService", "handler_name": "AccountService",
				"rpc_protocol": "micro", "rpc_service": "example.flow.AccountService",
				"rpc_client_service": "example.flow.AccountService", "methods": "Apply"}},
		{ID: "symbol:go:example.com/m:svc.Missing", Kind: domain.KindGrpcService,
			Name: "svc.Missing", FilePath: "flow.go", LineStart: 1,
			Properties: map[string]any{"service_name": "Missing", "handler_name": "Missing",
				"rpc_protocol": "micro", "rpc_client_service": "example.flow.Missing", "methods": "Other"}},
		{ID: "symbol:go:example.com/m:Server", Kind: domain.KindStruct, Name: "Server",
			FilePath: "flow.go", LineStart: lineOf("Server")},
		{ID: "symbol:go:example.com/m:(Server).Apply", Kind: domain.KindMethod, Name: "(Server).Apply",
			FilePath: "flow.go", LineStart: lineOf("Apply")},
		{ID: "symbol:go:example.com/m:(Server).Other", Kind: domain.KindMethod, Name: "(Server).Other",
			FilePath: "flow.go", LineStart: lineOf("Other")},
		{ID: "symbol:go:example.com/m:Noop", Kind: domain.KindFunction, Name: "Noop",
			FilePath: "flow.go", LineStart: lineOf("Noop")},
	}
	edges := []*domain.Fact{
		// 两条 Apply 调用：同一服务（身份解析应只查一次）
		{SourceID: "symbol:go:example.com/m:Entry", TargetID: "symbol:go:example.com/m:svc.AccountService",
			Kind: domain.FactGrpcCall, ToolSource: domain.ToolCodeGraph, Confidence: 1, Key: "Apply",
			Metadata: map[string]any{"method": "Apply", "rpc_protocol": "micro",
				"line_num": entryCallLines[0], "pos": entryCallPos[0]}},
		{SourceID: "symbol:go:example.com/m:Entry", TargetID: "symbol:go:example.com/m:svc.AccountService",
			Kind: domain.FactGrpcCall, ToolSource: domain.ToolCodeGraph, Confidence: 1, Key: "Apply",
			Metadata: map[string]any{"method": "Apply", "rpc_protocol": "micro",
				"line_num": entryCallLines[1], "pos": entryCallPos[1]}},
		// 一条未注册服务（正常未命中：命中/未命中都缓存）
		{SourceID: "symbol:go:example.com/m:Entry", TargetID: "symbol:go:example.com/m:svc.Missing",
			Kind: domain.FactGrpcCall, ToolSource: domain.ToolCodeGraph, Confidence: 1, Key: "Other",
			Metadata: map[string]any{"method": "Other", "rpc_protocol": "micro",
				"line_num": entryCallLines[2], "pos": entryCallPos[2]}},
		{SourceID: "symbol:go:example.com/m:Server", TargetID: "symbol:go:example.com/m:svc.AccountService",
			Kind: domain.FactGrpcImpl, ToolSource: domain.ToolCodeGraph, Confidence: 1},
		{SourceID: "symbol:go:example.com/m:(Server).Apply", TargetID: "symbol:go:example.com/m:Noop",
			Kind: domain.FactCalls, ToolSource: domain.ToolCodeGraph, Confidence: 0.8,
			Metadata: map[string]any{"line_num": lineOf("Apply"), "pos": 1}},
	}
	if _, err := r.SaveBatchStats(nodes, edges, nil); err != nil {
		t.Fatalf("SaveBatchStats: %v", err)
	}
	cr := &countingRPCReader{Reader: r}
	return &Actions{repo: cr}, cr
}

func TestSeqRPCContinuationAndRequestCache(t *testing.T) {
	a, counter := seedRPCRepo(t)
	root, err := a.CodeSequence(CodeSequenceRequest{
		Target:  "symbol:go:example.com/m:Entry",
		RepoAbs: a.repo.RepoPath(),
		Depth:   4,
	})
	if err != nil {
		t.Fatalf("CodeSequence: %v", err)
	}
	var rpcNodes []*CodeSeqNode
	var missing []*CodeSeqNode
	walkSeqTree([]*CodeSeqNode{root}, func(n *CodeSeqNode) {
		if n.Evidence != nil && n.Evidence.Source == SeqEvidenceRPC {
			rpcNodes = append(rpcNodes, n)
		}
		if n.Stop != nil && n.Stop.Code == SeqStopRPCImplMissing {
			missing = append(missing, n)
		}
	})
	if len(rpcNodes) != 3 {
		t.Fatalf("RPC 续接节点 = %d; want 3", len(rpcNodes))
	}
	sym := map[string]int{}
	for _, n := range rpcNodes {
		if n.Stop != nil && n.Stop.Code == SeqStopRPCImplMissing {
			continue
		}
		sym[n.Evidence.SymbolID]++
		if n.Evidence.RPCService != "example.flow.AccountService" || n.Evidence.RPCProtocol != "micro" {
			t.Errorf("证据 = %+v; want AccountService/micro", n.Evidence)
		}
		if !strings.HasPrefix(n.Evidence.Reason, "micro-register-identity") ||
			n.Evidence.Verdict != SeqVerdictCorroborated {
			t.Errorf("证据依据 = %q/%q; want micro-register-identity*/corroborated",
				n.Evidence.Reason, n.Evidence.Verdict)
		}
	}
	if sym["symbol:go:example.com/m:(Server).Apply"] != 2 {
		t.Errorf("两条 Apply 调用都应续到 (Server).Apply: %v", sym)
	}
	// 未注册服务：明确诊断（不是"查询失败"），且不展开实现
	if len(missing) != 1 {
		t.Fatalf("未注册服务诊断节点 = %d; want 1", len(missing))
	}
	if hasSeqChildWithLabel(rpcNodes[len(rpcNodes)-1], "Noop") {
		t.Errorf("未注册服务不得展开到任何实现")
	}
	// 请求内缓存：同一服务身份只查一次；未命中同样缓存
	if counter.exact != 2 { // AccountService + Missing 各一次
		t.Errorf("精确身份查询次数 = %d; want 2（同服务只查一次）", counter.exact)
	}
	// 完整身份（AccountService）命中时不枚举；未注册服务（Missing 只有客户端
	// 声明身份）需要一次服务枚举（kind 过滤，不是全库扫描）。
	if counter.byProtocol != 1 {
		t.Errorf("服务枚举次数 = %d; want 1（仅缺少注册身份的服务）", counter.byProtocol)
	}
	if counter.impls != 2 { // AccountService + Missing（未命中实现）
		t.Errorf("实现查询次数 = %d; want 2（命中/未命中都缓存）", counter.impls)
	}
	if counter.duplicate != 0 {
		t.Errorf("同一源函数的出站边被重复查询 %d 次（请求内缓存失效）: %v", counter.duplicate, counter.forwards)
	}
	if len(counter.forwards) == 0 {
		t.Errorf("应至少查询一次调用点出站边")
	}
	// 服务端子树展开：Apply → Noop
	found := false
	for _, n := range rpcNodes {
		if hasSeqChildWithLabel(n, "Noop") {
			found = true
		}
	}
	if !found {
		t.Errorf("应展开 (Server).Apply → Noop")
	}
}

// hasSeqChildWithLabel 子树里是否有指定标签的节点（不含自身）。
func hasSeqChildWithLabel(n *CodeSeqNode, label string) bool {
	if n == nil {
		return false
	}
	found := false
	for _, c := range n.Nodes {
		if c.Label == label {
			return true
		}
		if hasSeqChildWithLabel(c, label) {
			found = true
		}
	}
	for _, c := range n.Else {
		if hasSeqChildWithLabel(c, label) {
			found = true
		}
	}
	return found
}

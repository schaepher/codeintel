package action

// todo22 §4.1 跨仓续接验收：客户端仓库（只有客户端声明身份）→ 候选仓库
// （服务端注册身份 + 实现）续接；候选范围外不做（repo-only 明确未命中）；
// 目标视图在本请求结束时关闭（资源关闭覆盖全部子树使用期）。

import (
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"testing"

	"github.com/schaepher/codeintel/internal/domain"
	"github.com/schaepher/codeintel/internal/infrastructure/sqlite"
)

const rpcRemoteClientSrc = `package cli

type Client interface{ Apply() }

func Entry(c Client) {
	c.Apply()
}
`

const rpcRemoteServerSrc = `package srv

type AccountServer struct{}

func (h *AccountServer) Apply() { SaveAudit() }

func SaveAudit() {}
`

// rpcRemoteRepo 一个夹具仓库（源码 + DB）。
type rpcRemoteRepo struct {
	dir   string
	acts  *Actions
	db    *sqlite.DB
	opens int
	done  int
}

func (r *rpcRemoteRepo) close() { _ = r.db.Close() }

// seedRPCClientRepo 客户端仓库：Entry 有 go-micro 调用点出站边，但**没有**
// 服务端注册（只有客户端声明身份 rpc_client_service）。
func seedRPCClientRepo(t *testing.T) *rpcRemoteRepo {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/client\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "flow.go"), []byte(rpcRemoteClientSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := sqlite.NewRepo(db)
	fset, f := parseSeqFile(t, rpcRemoteClientSrc)
	line, pos := callSiteOf(t, fset, f, "Entry", "Apply")
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/client:Entry", Kind: domain.KindFunction, Name: "Entry",
			FilePath: "flow.go", LineStart: funcLine(fset, f, "Entry")},
		{ID: "symbol:go:example.com/client:svc.AccountService", Kind: domain.KindGrpcService,
			Name: "svc.AccountService", FilePath: "flow.go", LineStart: 1,
			Properties: map[string]any{"service_name": "AccountService", "handler_name": "AccountService",
				"rpc_protocol": "micro", "rpc_client_service": "example.flow.AccountService", "methods": "Apply"}},
	}, []*domain.Fact{
		{SourceID: "symbol:go:example.com/client:Entry", TargetID: "symbol:go:example.com/client:svc.AccountService",
			Kind: domain.FactGrpcCall, Confidence: 1.0, Key: "Apply",
			Metadata: map[string]any{"method": "Apply", "rpc_protocol": "micro",
				"line_num": line, "pos": pos}},
	}, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(&domain.BuildMeta{BuildID: "client", ToolName: "all", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	return &rpcRemoteRepo{dir: dir, acts: New(r), db: db}
}

// seedRPCServerRepo 服务端仓库：注册身份 + 实现 + 实现方法 → 下游调用。
func seedRPCServerRepo(t *testing.T) *rpcRemoteRepo {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/server\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "srv.go"), []byte(rpcRemoteServerSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := sqlite.NewRepo(db)
	fset, f := parseSeqFile(t, rpcRemoteServerSrc)
	saveLine := func(name string) int { return methodOrFuncLine(fset, f, name) }
	structLine := typeLine(fset, f, "AccountServer")
	applyLine, applyPos := callSiteOf(t, fset, f, "Apply", "SaveAudit")
	if _, err := r.SaveBatchStats([]*domain.CodeEntity{
		{ID: "symbol:go:example.com/server:svc.AccountService", Kind: domain.KindGrpcService,
			Name: "svc.AccountService", FilePath: "srv.go", LineStart: 1,
			Properties: map[string]any{"service_name": "AccountService", "handler_name": "AccountService",
				"rpc_protocol": "micro", "rpc_service": "example.flow.AccountService", "methods": "Apply"}},
		{ID: "symbol:go:example.com/server:AccountServer", Kind: domain.KindStruct, Name: "AccountServer",
			FilePath: "srv.go", LineStart: structLine},
		{ID: "symbol:go:example.com/server:(AccountServer).Apply", Kind: domain.KindMethod,
			Name: "(AccountServer).Apply", FilePath: "srv.go", LineStart: saveLine("Apply")},
		{ID: "symbol:go:example.com/server:SaveAudit", Kind: domain.KindFunction,
			Name: "SaveAudit", FilePath: "srv.go", LineStart: saveLine("SaveAudit")},
	}, []*domain.Fact{
		{SourceID: "symbol:go:example.com/server:AccountServer", TargetID: "symbol:go:example.com/server:svc.AccountService",
			Kind: domain.FactGrpcImpl, ToolSource: domain.ToolCodeGraph, Confidence: 1.0},
		{SourceID: "symbol:go:example.com/server:(AccountServer).Apply", TargetID: "symbol:go:example.com/server:SaveAudit",
			Kind: domain.FactCalls, ToolSource: domain.ToolCodeGraph, Confidence: 0.8,
			Metadata: map[string]any{"line_num": applyLine, "pos": applyPos}},
	}, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(&domain.BuildMeta{BuildID: "server", ToolName: "all", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	return &rpcRemoteRepo{dir: dir, acts: New(r), db: db}
}

// TestSeqRPCCrossRepo：候选仓库内解析到注册实现 → 目标仓库子树展开；
// 请求结束时关闭目标视图；无候选范围时只报本仓未命中（不打开别的仓库）。
func TestSeqRPCCrossRepo(t *testing.T) {
	client := seedRPCClientRepo(t)
	defer client.close()
	server := seedRPCServerRepo(t)
	defer server.close()

	opener := func(repoPath string) (*Actions, func() error, error) {
		if repoPath != server.dir {
			t.Fatalf("打开了非候选仓库: %s", repoPath)
		}
		server.opens++
		return server.acts, func() error { server.done++; return nil }, nil
	}
	root, err := client.acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/client:Entry", RepoAbs: client.dir, Depth: 4,
		CandidateRepos: []string{server.dir}, Open: opener, Scope: "workspace:ws1",
	})
	if err != nil {
		t.Fatalf("CodeSequence: %v", err)
	}
	var rpc *CodeSeqNode
	for _, n := range root.Nodes {
		if n.Evidence != nil && n.Evidence.Source == SeqEvidenceRPC {
			rpc = n
		}
	}
	if rpc == nil {
		t.Fatalf("应有 RPC 续接节点: %+v", root.Nodes)
	}
	if rpc.Stop != nil {
		t.Fatalf("跨仓续接不应停止: %+v", rpc.Stop)
	}
	if rpc.Evidence.SymbolID != "symbol:go:example.com/server:(AccountServer).Apply" {
		t.Errorf("续接目标 = %q", rpc.Evidence.SymbolID)
	}
	if rpc.Evidence.RemoteRepo != server.dir {
		t.Errorf("RemoteRepo = %q; want %q", rpc.Evidence.RemoteRepo, server.dir)
	}
	if rpc.Evidence.ServiceID != "symbol:go:example.com/server:svc.AccountService" {
		t.Errorf("ServiceID = %q（应为目标仓库的服务节点）", rpc.Evidence.ServiceID)
	}
	if rpc.Evidence.RPCService != "example.flow.AccountService" ||
		rpc.Evidence.Reason != "micro-register-identity" {
		t.Errorf("身份证据 = %q/%q", rpc.Evidence.RPCService, rpc.Evidence.Reason)
	}
	if !hasSeqChildWithLabel(rpc, "SaveAudit") {
		t.Errorf("应展开目标仓库的实现方法体（Apply → SaveAudit）")
	}
	if server.opens != 1 {
		t.Errorf("候选仓库视图打开次数 = %d; want 1", server.opens)
	}
	if server.done != 1 {
		t.Errorf("请求结束时必须关闭目标视图（实际关闭 %d 次；资源覆盖子树使用期）", server.done)
	}

	// 无候选范围：只报本仓未命中，不打开任何仓库
	noScope, err := client.acts.CodeSequence(CodeSequenceRequest{
		Target: "symbol:go:example.com/client:Entry", RepoAbs: client.dir, Depth: 4,
		Open: func(repoPath string) (*Actions, func() error, error) {
			t.Fatalf("没有候选范围时不得打开仓库: %s", repoPath)
			return nil, nil, nil
		},
	})
	if err != nil {
		t.Fatalf("CodeSequence(repo-only): %v", err)
	}
	found := false
	walkSeqTree([]*CodeSeqNode{noScope}, func(n *CodeSeqNode) {
		if n.Stop != nil && n.Stop.Code == SeqStopRPCRegistrationMissing {
			found = true
		}
	})
	if !found {
		t.Errorf("repo-only 应报 rpc_registration_missing（正常未命中）")
	}
}

// funcLine 顶层函数声明行。
func funcLine(fset *token.FileSet, f *ast.File, name string) int {
	for _, decl := range f.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok && fd.Recv == nil && fd.Name.Name == name {
			return fset.Position(fd.Pos()).Line
		}
	}
	return 0
}

// methodOrFuncLine 函数或方法声明行（按名字）。
func methodOrFuncLine(fset *token.FileSet, f *ast.File, name string) int {
	for _, decl := range f.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok && fd.Name.Name == name {
			return fset.Position(fd.Pos()).Line
		}
	}
	return 0
}

// typeLine 顶层类型声明行。
func typeLine(fset *token.FileSet, f *ast.File, name string) int {
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			if ts, ok := spec.(*ast.TypeSpec); ok && ts.Name.Name == name {
				return fset.Position(ts.Pos()).Line
			}
		}
	}
	return 0
}

// callSiteOf 指定函数体里某个方法调用的 (行号, 字节偏移)。
func callSiteOf(t *testing.T, fset *token.FileSet, f *ast.File, funcName, method string) (int, int) {
	t.Helper()
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Name.Name != funcName || fd.Body == nil {
			continue
		}
		var line, pos int
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == method {
				line = fset.Position(call.Lparen).Line
				pos = fset.Position(call.Lparen).Offset
				return false
			}
			return true
		})
		return line, pos
	}
	t.Fatalf("找不到调用点 %s 里的 %s", funcName, method)
	return 0, 0
}

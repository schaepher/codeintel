//go:build integration

package integration

// todo22 §4.1：go-micro 服务协议与注册——真实 init → CLI 查询验收。
//
//   - 客户端只请求 example.flow.AccountService.Apply → 只续到 AccountServer.Apply
//   - 同名 Apply 属于 OtherService 时不得串线（两条入口分别验证）
//   - 移除注册 / 改变服务身份 / 改变注册协议 → 不串到 OtherServer，给出明确诊断
//   - depth=1 保留准确 RPC 目标 + depth 诊断（不为展开付代价）

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
)

// seqTreeJSON 跑 query sequence --code --json 并解析根节点。
func seqTreeJSON(t *testing.T, dir, sym string, depth int, extra ...string) *action.CodeSeqNode {
	t.Helper()
	args := []string{"query", "sequence", sym, "--code", "--depth", itoaTest(depth), "--json", "--repo", dir}
	args = append(args, extra...)
	code, out := runCLIOut(t, args...)
	if code != 0 {
		t.Fatalf("query sequence exit = %d\n%s", code, out)
	}
	var root action.CodeSeqNode
	if err := json.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("sequence JSON: %v\n%s", err, out)
	}
	return &root
}

func itoaTest(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// walkSeq 遍历整棵树（含分支/else）。
func walkSeq(n *action.CodeSeqNode, fn func(*action.CodeSeqNode)) {
	if n == nil {
		return
	}
	fn(n)
	for _, c := range n.Nodes {
		walkSeq(c, fn)
	}
	for _, c := range n.Else {
		walkSeq(c, fn)
	}
}

// rpcSeqNodes 树上的 RPC 续接节点（evidence.source=rpc）。
func rpcSeqNodes(root *action.CodeSeqNode) []*action.CodeSeqNode {
	var out []*action.CodeSeqNode
	walkSeq(root, func(n *action.CodeSeqNode) {
		if n.Evidence != nil && n.Evidence.Source == action.SeqEvidenceRPC {
			out = append(out, n)
		}
	})
	return out
}

// requiredRPCNode 唯一 RPC 节点（多/少都失败——不静默取第一个）。
func requiredRPCNode(t *testing.T, root *action.CodeSeqNode) *action.CodeSeqNode {
	t.Helper()
	rpcs := rpcSeqNodes(root)
	if len(rpcs) != 1 {
		t.Fatalf("RPC 续接节点数 = %d; want 1", len(rpcs))
	}
	return rpcs[0]
}

// hasSymbolWith 树上是否有 evidence.SymbolID 含 substr 的节点。
func hasSymbolWith(root *action.CodeSeqNode, substr string) bool {
	found := false
	walkSeq(root, func(n *action.CodeSeqNode) {
		if n.Evidence != nil && strings.Contains(n.Evidence.SymbolID, substr) {
			found = true
		}
	})
	return found
}

func TestTodo22MicroRPCSameRepo(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	f := writeMicroFixture(t, nil)
	clearIndex(t, f.dir)
	if code := runCLI(t, "init", "--repo", f.dir, "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}

	root := seqTreeJSON(t, f.dir, "symbol:go:example.com/microflow:Entry", 6)
	rpc := requiredRPCNode(t, root)
	ev := rpc.Evidence
	if ev.SymbolID != "symbol:go:example.com/microflow:(AccountServer).Apply" {
		t.Errorf("RPC 续接目标 = %q; want (AccountServer).Apply", ev.SymbolID)
	}
	if ev.RPCService != "example.flow.AccountService" {
		t.Errorf("rpc_service = %q; want example.flow.AccountService（完整身份）", ev.RPCService)
	}
	if ev.RPCProtocol != "micro" || ev.RPCMethod != "Apply" {
		t.Errorf("协议/方法 = %q/%q; want micro/Apply", ev.RPCProtocol, ev.RPCMethod)
	}
	if ev.Reason != "micro-register-identity" {
		t.Errorf("reason = %q; want micro-register-identity（完整身份精确匹配）", ev.Reason)
	}
	if ev.Verdict != action.SeqVerdictCorroborated {
		t.Errorf("verdict = %q; want corroborated", ev.Verdict)
	}
	if ev.RegisterFile != "app.go" || ev.RegisterSite <= 0 {
		t.Errorf("注册站点 = %s:%d; want app.go 的注册行", ev.RegisterFile, ev.RegisterSite)
	}
	// 服务端子树：AccountServer.Apply 内的 SaveAudit 可见
	if !hasSymbolWith(rpc, "microflow:SaveAudit") {
		t.Errorf("RPC 子树应包含 AccountServer.Apply → SaveAudit: %+v", rpc.Nodes)
	}
	// 同名方法不得串到 OtherServer（本调用只请求 AccountService）
	if hasSymbolWith(root, "(OtherServer)") {
		t.Errorf("不得续接到 OtherServer: %+v", rpcSeqNodes(root))
	}
	// 客户端桩源码（NewRequest/Call）不应作为业务流程展开
	walkSeq(root, func(n *action.CodeSeqNode) {
		if n.Label == "c.c.Call" || n.Label == "c.c.NewRequest" {
			t.Errorf("桩源码不应展开成流程节点: %q", n.Label)
		}
	})

	// 另一个入口：OtherService.Apply 只能到 OtherServer.Apply
	rootOther := seqTreeJSON(t, f.dir, "symbol:go:example.com/microflow:EntryOther", 6)
	rpcOther := requiredRPCNode(t, rootOther)
	if rpcOther.Evidence.SymbolID != "symbol:go:example.com/microflow:(OtherServer).Apply" {
		t.Errorf("Other 入口目标 = %q; want (OtherServer).Apply", rpcOther.Evidence.SymbolID)
	}
	if hasSymbolWith(rootOther, "(AccountServer)") {
		t.Errorf("Other 入口不得串到 AccountServer")
	}
	if !hasSymbolWith(rpcOther, "microflow:SaveOtherAudit") {
		t.Errorf("Other 子树应包含 SaveOtherAudit")
	}

	// depth=1：保留准确目标 + depth 诊断，不展开服务端
	shallow := seqTreeJSON(t, f.dir, "symbol:go:example.com/microflow:Entry", 1)
	rpcShallow := requiredRPCNode(t, shallow)
	if rpcShallow.Stop == nil || rpcShallow.Stop.Code != action.SeqStopDepthLimit {
		t.Errorf("depth=1 RPC 节点诊断 = %+v; want depth", rpcShallow.Stop)
	}
	if !strings.Contains(rpcShallow.Evidence.SymbolID, "svc.AccountService") {
		t.Errorf("depth=1 仍须保留准确 RPC 目标: %q", rpcShallow.Evidence.SymbolID)
	}
}

func TestTodo22MicroRPCNegative(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	removedApp := strings.Replace(microFixtureApp,
		`	pb.RegisterAccountServiceHandler(svc.Server(), new(AccountServer))
	other := micro.NewService(micro.Name("example.flow.OtherService"))
	pb.RegisterOtherServiceHandler(other.Server(), new(OtherServer))`,
		`	_ = svc.Server()`, 1)
	if removedApp == microFixtureApp {
		t.Fatal("fixture 替换失效（注册行未找到）")
	}
	protocolApp := strings.Replace(microFixtureApp,
		`	pb.RegisterAccountServiceHandler(svc.Server(), new(AccountServer))`,
		`	registerOutsideProto(svc.Server(), new(AccountServer))`, 1) + `
func registerOutsideProto(s any, h any) {}
`
	renamedApp := strings.Replace(microFixtureApp,
		`micro.NewService(micro.Name("example.flow.AccountService"))`,
		`micro.NewService(micro.Name("example.flow.Renamed"))`, 1)
	if renamedApp == microFixtureApp {
		t.Fatal("fixture 替换失效（服务名未找到）")
	}

	cases := []struct {
		name     string
		app      string
		wantCode string // RPC 节点的 Stop.Code（空=应成功续接）
		wantSym  string
		wantWhy  string
	}{
		{name: "registration-removed", app: removedApp, wantCode: action.SeqStopRPCImplMissing,
			wantWhy: "注册实参"},
		{name: "protocol-changed", app: protocolApp, wantCode: action.SeqStopRPCImplMissing},
		{name: "service-identity-changed", app: renamedApp, wantCode: action.SeqStopRPCIdentityConflict,
			wantWhy: "注册身份"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := writeMicroFixture(t, map[string]string{"app.go": tc.app})
			clearIndex(t, f.dir)
			if code := runCLI(t, "init", "--repo", f.dir, "--progress", "none"); code != 0 {
				t.Fatalf("init exit = %d", code)
			}
			root := seqTreeJSON(t, f.dir, "symbol:go:example.com/microflow:Entry", 6)
			rpc := requiredRPCNode(t, root)
			if hasSymbolWith(root, "(OtherServer)") {
				t.Fatalf("任何变体都不得续到 OtherServer: %+v", rpc.Evidence)
			}
			if rpc.Stop == nil || rpc.Stop.Code != tc.wantCode {
				t.Fatalf("Stop = %+v; want code %s", rpc.Stop, tc.wantCode)
			}
			if tc.wantWhy != "" && !strings.Contains(rpc.Stop.Detail, tc.wantWhy) {
				t.Errorf("Stop.Detail = %q; want 含 %q（可核对原因）", rpc.Stop.Detail, tc.wantWhy)
			}
			if hasSymbolWith(root, "(AccountServer)") {
				t.Errorf("未命中/身份冲突时不得展开 AccountServer")
			}
		})
	}
}

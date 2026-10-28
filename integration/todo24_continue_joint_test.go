//go:build integration

package integration

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/schaepher/codeintel/internal/action"
)

// seqPreOrder 先序收集（call 节点标签 + 证据 symbol_id），用于与 Mermaid 对照。
func seqPreOrder(n *action.CodeSeqNode, labels *[]string, syms *[]string) {
	if n == nil {
		return
	}
	if n.Kind == "call" {
		*labels = append(*labels, n.Label)
		if n.Evidence != nil {
			*syms = append(*syms, n.Evidence.SymbolID)
		}
	}
	for _, c := range n.Nodes {
		seqPreOrder(c, labels, syms)
	}
	for _, c := range n.Else {
		seqPreOrder(c, labels, syms)
	}
}

func countSeqSym(n *action.CodeSeqNode, suffix string) int {
	c := 0
	walkSeq(n, func(x *action.CodeSeqNode) {
		if x.Evidence != nil && strings.HasSuffix(x.Evidence.SymbolID, suffix) {
			c++
		}
	})
	return c
}

func seqDiagnosticCount(n *action.CodeSeqNode, code string) int {
	if n == nil || n.Meta == nil {
		return 0
	}
	for _, d := range n.Meta.Diagnostics {
		if d.Code == code {
			return d.Count
		}
	}
	return 0
}

// todo24 §2（联合夹具）：go-micro→注册→DI/AOP→装饰器列表→事务回调→存储，
// depth=2 + 续查仍保持工厂/包装顺序与终点（RPC 前沿也须续接）。
// 大树 JSON 超过管道缓冲：用外部进程跑 CLI 再解析（同 todo22 flow 矩阵）。

// TestContinueSegmentsJointFlowMatrix：go-micro→注册→DI/AOP→装饰器列表→外部事务
// 回调→存储，depth=2 + 续查仍保持工厂/包装顺序与终点（RPC 前沿也须续接）。
func TestContinueSegmentsJointFlowMatrix(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	// 大树 JSON 超过管道缓冲：用外部进程跑 CLI 再解析（同 todo22 flow 矩阵）。
	bin, dir := flowMatrixCLI(t)
	code, out := flowRun(t, bin, "query", "sequence",
		"symbol:go:example.com/flowmatrix:(App).Flow21NewEntityA",
		"--repo", dir, "--code", "--depth", "2", "--continue-segments", "20",
		"--scene", "Source=7", "--json")
	if code != 0 {
		t.Fatalf("sequence exit=%d\n%s", code, out)
	}
	var root action.CodeSeqNode
	if err := json.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("JSON: %v\n%s", err, out)
	}
	var labels, syms []string
	var rootPtr = &root
	seqPreOrder(rootPtr, &labels, &syms)
	firstWith := func(want string) int {
		for i, s := range syms {
			if strings.HasSuffix(s, want) {
				return i
			}
		}
		return -1
	}
	for _, want := range []string{":SaveTrace", "ApplyRemote", "(AccountServer).Apply",
		"MakeFirst", "Setup", "First", "Second", "BeforeFirst", "BeforeSecond",
		"AfterFirst", "AfterSecond", "(storeAOP).Insert", "(Store).Insert",
		"(DB).ExecContext", "ApproveTrace"} {
		if firstWith(want) < 0 {
			t.Errorf("depth=2 + 续查链路缺少 %s", want)
		}
	}
	// 包装顺序：MakeFirst/Setup 在装饰器应用之前；AOP 包装器先于真实实现。
	if setup := firstWith("Setup"); setup < 0 || setup > firstWith("BeforeFirst") {
		t.Errorf("工厂求值 Setup 应在装饰器执行之前（setup=%d beforeFirst=%d）", setup, firstWith("BeforeFirst"))
	}
	if aop, store := firstWith("(storeAOP).Insert"), firstWith("(Store).Insert"); aop < 0 || aop > store {
		t.Errorf("AOP 包装器应先于真实实现（aop=%d store=%d）", aop, store)
	}
	if root.Meta == nil || root.Meta.Continuation == nil {
		t.Fatalf("缺少 meta.continuation: %+v", root.Meta)
	}
	if root.Meta.Continuation.SegmentsUsed < 2 || root.Meta.Continuation.BudgetStops != 0 {
		t.Errorf("联合夹具应续查 ≥2 段且无预算停止: %+v", root.Meta.Continuation)
	}
}

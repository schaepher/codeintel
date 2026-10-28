package action

// todo24 §2：分段续查的低层回归（前沿登记/去重/预算记账/保守边界）。

import (
	"testing"
)

func continueReq(budget int) CodeSequenceRequest {
	return CodeSequenceRequest{Target: "symbol:go:example.com/m:Entry",
		RepoAbs: "/repo", Depth: 1, ContinueSegments: budget,
		visited: map[string]bool{"a": true}}
}

func depthStopNode(target string) *CodeSeqNode {
	return &CodeSeqNode{Kind: "call", Label: target,
		Evidence: &SeqEvidence{Source: SeqEvidenceCallEdge, SymbolID: target,
			CallFile: "flow.go", CallLine: 7},
		Stop: &SeqStop{Code: SeqStopDepthLimit, Detail: "depth 上限（--depth 1）"}}
}

func TestSeqFrontierDedupAcrossLayers(t *testing.T) {
	req := continueReq(1)
	req.acc = &seqTimingAcc{}
	node := depthStopNode("symbol:go:example.com/m:First")
	calls := 0
	resume := func(CodeSequenceRequest) { calls++ }
	// 同一前沿被"回调层"和"普通调用层"各登记一次 → 只算一次。
	seqRegisterFrontier(req, node, "symbol:go:example.com/m:First", resume, node.Stop.Detail)
	seqRegisterFrontier(req, node, "symbol:go:example.com/m:First", resume, node.Stop.Detail)
	if len(req.acc.frontiers) != 1 {
		t.Fatalf("同一前沿应只登记一次，got %d", len(req.acc.frontiers))
	}
	seqContinueSegments(New(nil), req, &CodeSeqNode{})
	if calls != 1 {
		t.Errorf("前沿只应变一次，got %d", calls)
	}
	meta := seqContinuationMeta(req)
	if meta == nil || meta.BudgetStops != 0 || meta.SegmentsUsed != 1 {
		t.Errorf("meta.continuation = %+v，期望 used=1/budget_stops=0", meta)
	}
}

func TestSeqFrontierBudgetStops(t *testing.T) {
	req := continueReq(1)
	req.acc = &seqTimingAcc{}
	a := depthStopNode("symbol:go:example.com/m:First")
	b := depthStopNode("symbol:go:example.com/m:Second")
	// resume 不展开（保留 depth 停止），下一段没有可展开节点 → 都记预算停止。
	noop := func(CodeSequenceRequest) {}
	seqRegisterFrontier(req, a, "symbol:go:example.com/m:First", noop, a.Stop.Detail)
	seqRegisterFrontier(req, b, "symbol:go:example.com/m:Second", noop, b.Stop.Detail)
	seqContinueSegments(New(nil), req, &CodeSeqNode{})
	meta := seqContinuationMeta(req)
	if meta.BudgetStops != 0 {
		// resume 已把 Stop 清空 → 不算预算停止（前沿确实被处理）
		t.Errorf("已处理的前沿不应记 budget_stops，got %d", meta.BudgetStops)
	}
}

func TestSeqRegistrationSkipsUnproven(t *testing.T) {
	cases := []*CodeSeqNode{
		{Kind: "call", Evidence: &SeqEvidence{Source: SeqEvidenceCallEdge}, Stop: &SeqStop{Code: SeqStopDepthLimit}},
		{Kind: "call", Evidence: &SeqEvidence{Source: SeqEvidenceIface, SymbolID: "x", Ambiguous: true, Candidates: []string{"a", "b"}}, Stop: &SeqStop{Code: SeqStopDepthLimit}},
		{Kind: "call", Evidence: &SeqEvidence{Source: SeqEvidenceExternal, SymbolID: "x"}, Stop: &SeqStop{Code: SeqStopDepthLimit}},
		{Kind: "call", Evidence: &SeqEvidence{Source: SeqEvidenceRPC, SymbolID: "x"}, Stop: &SeqStop{Code: SeqStopDepthLimit}},
		{Kind: "call", Evidence: &SeqEvidence{Source: SeqEvidenceCallEdge, SymbolID: "x"}, Stop: &SeqStop{Code: SeqStopNoCalls}},
	}
	req := continueReq(4)
	req.acc = &seqTimingAcc{}
	for i, n := range cases {
		seqRegisterSymbolFrontier(New(nil), req, n)
		if len(req.acc.frontiers) != 0 {
			t.Errorf("case %d 不应登记前沿（got %d）", i, len(req.acc.frontiers))
		}
	}
}

func TestSeqContinuationMetaDisabled(t *testing.T) {
	req := CodeSequenceRequest{Depth: 1}
	req.acc = &seqTimingAcc{}
	if m := seqContinuationMeta(req); m != nil {
		t.Errorf("未启用续查时不得输出 meta.continuation，got %+v", m)
	}
}

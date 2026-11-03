package action

// U06b：候选截断与两个续查入口的候选边界（不依赖业务索引）。

import (
	"fmt"
	"go/token"
	"testing"
)

// TestU06bCandidateResumeBoundary 候选节点不作为前沿、不 replay/不消耗预算。
func TestU06bCandidateResumeBoundary(t *testing.T) {
	n := &CodeSeqNode{Candidate: true, Evidence: &SeqEvidence{
		Source: SeqEvidenceCallEdge, SymbolID: "symbol:go:example.com/receiver:Hit"},
		Stop: &SeqStop{Code: SeqStopDepthLimit}}
	if seqCanContinueFrontier(n) {
		t.Fatalf("候选节点不得作为续查前沿")
	}
	acc := &seqTimingAcc{}
	req := CodeSequenceRequest{Depth: 1, ContinueSegments: 2, acc: acc}
	replays := 0
	seqRegisterFrontier(req, n, n.Evidence.SymbolID, func(sub CodeSequenceRequest) {
		replays++
	}, "depth")
	seqContinueSegments(nil, req, n)
	if replays != 0 || acc.continueExpansions != 0 {
		t.Fatalf("候选节点不应 replay/消耗预算: replays=%d expanded=%d", replays, acc.continueExpansions)
	}
	// 请求级候选边界（Continue 兼容入口共用同一引擎）。
	acc2 := &seqTimingAcc{}
	req2 := CodeSequenceRequest{Depth: 1, ContinueSegments: 2, acc: acc2, inCandidate: true}
	replays2 := 0
	seqRegisterFrontier(req2, n, n.Evidence.SymbolID, func(sub CodeSequenceRequest) {
		replays2++
	}, "depth")
	seqContinueSegments(nil, req2, n)
	if replays2 != 0 || acc2.continueExpansions != 0 {
		t.Fatalf("请求级候选边界仍 replay: %d", replays2)
	}
	fmt.Printf("frontier_allowed=%t replays=%d expanded=%d\n",
		seqCanContinueFrontier(n), replays, acc.continueExpansions)
}

// TestU06bCandidateBudgetMarked 预算截断节点必须带 candidate=true。
func TestU06bCandidateBudgetMarked(t *testing.T) {
	node := &CodeSeqNode{Evidence: &SeqEvidence{Source: SeqEvidenceIface,
		SymbolID: "symbol:go:example.com:Many.Run"}}
	cands := make([]string, 0, 14)
	for i := 0; i < 14; i++ {
		cands = append(cands, "symbol:go:example.com/receiver:(M"+itoaSeq(i)+").Run")
	}
	_, truncated := capSeqCandidates(cands)
	if !truncated {
		t.Fatalf("14 个候选应触发预算截断")
	}
	// 无 Actions 时不展开候选体（只验证预算截断节点的标记）。
	seqExpandCandidateBranches(nil, CodeSequenceRequest{acc: &seqTimingAcc{}}, node, nil,
		2, token.Position{Line: 1}, truncated, len(cands))
	var budget *CodeSeqNode
	var walk func([]*CodeSeqNode)
	walk = func(nodes []*CodeSeqNode) {
		for _, n := range nodes {
			if n != nil && n.Stop != nil && n.Stop.Code == SeqStopCandidateBudget {
				budget = n
			}
			walk(n.Nodes)
			walk(n.Else)
		}
	}
	walk(node.Nodes)
	if budget == nil || !budget.Candidate {
		t.Fatalf("candidate_budget 节点必须 candidate=true: %+v", budget)
	}
}

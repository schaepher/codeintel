package action

// todo22 §3.2：接口派发证据构造（从 action_seq_walk_call.go 拆出——行数治理）。

// seqIfaceEvidence 接口派发的证据：多候选时保留集合 + 依据 + ambiguous
// （要求：不能静默选择一个实现）。
func seqIfaceEvidence(callFile string, callLine int, chosen string, candidates []string, confidence float64) *SeqEvidence {
	ev := &SeqEvidence{Source: SeqEvidenceIface, CallFile: callFile, CallLine: callLine,
		SymbolID: chosen, Confidence: confidence, Candidates: candidates}
	switch len(candidates) {
	case 0, 1:
		ev.Reason = "unique"
	default:
		ev.Reason = "first-of-" + itoaSeq(len(candidates))
		ev.Ambiguous = true
	}
	return ev
}

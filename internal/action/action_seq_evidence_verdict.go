package action

// 证据支持程度判定（从 action_seq_evidence.go 拆出——行数治理）。

// seqEvidenceVerdict 按证据来源与歧义给出支持程度。
func seqEvidenceVerdict(ev *SeqEvidence) string {
	if ev == nil {
		return SeqVerdictUnverified
	}
	if ev.Ambiguous {
		return SeqVerdictUnverified
	}
	switch ev.Source {
	case SeqEvidenceCallEdge:
		if ev.Reason == SeqReasonAggregatedCallEdge {
			// 位置→目标的对应关系由汇总边推断（不是该位置的直接 metadata）
			return SeqVerdictCorroborated
		}
		if ev.Confidence >= MinConfidence {
			return SeqVerdictDirect
		}
		return SeqVerdictCorroborated
	case SeqEvidenceAST:
		// 只有调用表达式存在、没有目标身份 → 不是已被证明的调用
		// （todo20-9：不能把语法节点存在误读为目标已解析）。
		if ev.SymbolID == "" {
			return SeqVerdictUnverified
		}
		return SeqVerdictCorroborated
	case SeqEvidenceFieldFlow, SeqEvidenceLocalVar, SeqEvidenceGrpcImpl, SeqEvidenceIface:
		return SeqVerdictCorroborated
	case SeqEvidenceDI:
		// T06：字段注入 = 注解 key + 准确注册 + 容器注入代码（三者齐备才走到
		// 这里；缺证据时调用方已标 unverified/unknown）。
		if ev.SymbolID == "" {
			return SeqVerdictUnverified
		}
		return SeqVerdictCorroborated
	case SeqEvidenceClosure:
		// 语法级数据流（绑定证明）：有明确绑定 → corroborated；
		// 未绑定/多值未消歧（Ambiguous）→ 已在前面判为 unverified。
		return SeqVerdictCorroborated
	case SeqEvidenceRPC:
		return seqRPCEvidenceVerdict(ev) // todo22 §4.1（见 action_seq_rpc_evidence.go）
	case SeqEvidenceHTTPServer:
		// T11：HTTP 服务端 handler 由 origin+method+注册三项证明（歧义在前
		// 面已判 unverified；这里要求 handler canonical ID 存在）。
		if ev.SymbolID == "" {
			return SeqVerdictUnverified
		}
		return SeqVerdictCorroborated
	}
	return SeqVerdictUnverified
}

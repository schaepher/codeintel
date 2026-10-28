package action

// U06：接口字段/接收者实例来源未知的边界判定（从 action_seq_walk_call.go
// 拆出——行数治理）：唯一实现不等于实际实例来源。

// markSeqUnknownReceiver 把节点标成"实例来源未知"：保留接口调用与未验证
// 边界（Ambiguous 让渲染加 "? " 前缀），不展开任何实现。
func markSeqUnknownReceiver(node *CodeSeqNode, curFile string, line int,
	confidence float64, recvType, field string) {
	node.Evidence = &SeqEvidence{Source: SeqEvidenceFieldFlow, CallFile: curFile,
		CallLine: line, Confidence: confidence,
		Reason: "unknown_receiver", Ambiguous: true}
	node.Stop = &SeqStop{Code: SeqStopUnknownReceiver,
		Detail: "字段 " + recvType + "." + field + " 无写入/注入证明（唯一实现不等于实际实例来源，未展开）"}
}

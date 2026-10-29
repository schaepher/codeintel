package action

// 调用点签名附加（从 newSeqCallNode 拆出——行数治理）。

import "time"

// seqAttachCallSignature 解析目标签名并写进调用节点（参数/返回值类型），
// 同时累计签名查询的耗时/次数（诊断口径）。
func seqAttachCallSignature(a *Actions, req CodeSequenceRequest, node *CodeSeqNode, sigID string) {
	sigStart := time.Now()
	args, rets, ok2 := sigTypesOf(a, sigID)
	req.acc.sigLookups++
	req.acc.symLookups++ // 签名解析内部 GetSymbol（精确查询，14.1）
	req.acc.sqlQueries++
	req.acc.symDur += time.Since(sigStart)
	req.acc.sigDur += time.Since(sigStart)
	if ok2 && node != nil {
		node.Args, node.Returns = args, rets
	}
}

package action

// T12b：语句结束同步续查本语句登记的已证明前沿。

// seqContinueAtStatement 语句结束时同步展开本语句登记的已证明前沿（T12b：
// 在调用点完成续查，随后调用方即可绑定结果、继续处理后续语句）。
func seqContinueAtStatement(a *Actions, req CodeSequenceRequest) {
	acc := req.acc
	if req.ContinueSegments <= 0 || acc == nil || acc.inContinuation ||
		req.inCandidate || acc.continueSpent >= req.ContinueSegments {
		return
	}
	stmt := acc.stmtSeq
	if seqContinueBatch(a, req, stmt) {
		seqResolvePendingResults(req)
	}
}

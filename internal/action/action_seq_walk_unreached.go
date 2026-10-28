package action

// U03：已判定 taken 且以 return 结束的分支之后不可达（从
// action_seq_walk.go 拆出——行数治理）。

// seqTakenBranchReturns 分支被场景判定为 taken 且其（then）路径以 return 结束
// ——后续语句在该路径不可达。
func seqTakenBranchReturns(n *CodeSeqNode) bool {
	if n == nil || n.Kind != "branch" || n.Scene == nil || n.Scene.Status != SceneTaken {
		return false
	}
	for _, c := range n.Nodes {
		if c.Terminates {
			return true
		}
	}
	return false
}

package action

// 实例树复制与条件化标注（从 action_seq_workflow_instance.go 拆出）。

// seqWFAnnotateTree 复制树 + 条件化标注 + 可达写入（不修改原树）。
func seqWFAnnotateTree(tree *CodeSeqNode, readVals map[string]map[string]*SeqStorageOrigin,
	guards map[string]bool, entry, repo string) (*CodeSeqNode, []seqWFWrite, []string) {
	cp := seqCloneNodeTree(tree)
	var writes []seqWFWrite
	var proofs []string
	var walk func(nodes []*CodeSeqNode) []*CodeSeqNode
	walk = func(nodes []*CodeSeqNode) []*CodeSeqNode {
		for _, n := range nodes {
			if n == nil {
				continue
			}
			switch n.Kind {
			case "branch":
				truth, left, right := seqWFEvalCondition(n.Condition, readVals, guards, &proofs)
				premises := []string{}
				if left != nil && left.Kind == SeqOriginReadColumn {
					premises = appendUniqueStr(premises, SeqProofReadSuccess)
					premises = appendUniqueStr(premises, SeqScanWhenSuccess)
				}
				ic := &SeqInstanceCondition{Truth: truth, Premises: premises, Left: cloneSeqScalarOrigin(left),
					Right: cloneSeqScalarOrigin(right)}
				if n.Scene != nil {
					if (n.Scene.Status == SceneTaken && truth == "false") ||
						(n.Scene.Status == SceneExcluded && truth == "true") {
						ic.Conflict = true
						ic.Reason = "instance_fact_conflict"
						ic.Truth = "unknown"
						truth = "unknown"
					}
				}
				n.InstanceCondition = ic
				if truth != "false" {
					n.Nodes = walk(n.Nodes)
				} else {
					n.Nodes = nil
				}
				if truth != "true" {
					n.Else = walk(n.Else)
				} else {
					n.Else = nil
				}
			case "loop":
				// 循环可能执行 0..N 次：分支判定不完整，保留内容并标 partial。
				proofs = appendUniqueStr(proofs, "instance_loop_boundary")
				n.Nodes = walk(n.Nodes)
			default:
				n.Nodes = walk(n.Nodes)
				n.Else = walk(n.Else)
			}
			if n.Evidence != nil && n.Evidence.Storage != nil &&
				(n.Evidence.Storage.Operation == SeqStorageInsert ||
					n.Evidence.Storage.Operation == SeqStorageUpdate) && !n.Candidate {
				writes = append(writes, seqWFWriteFromNode(n, entry, repo))
			}
		}
		return nodes
	}
	cp.Nodes = walk(cp.Nodes)
	return cp, writes, proofs
}

// seqWFWriteFromNode 实例可达写入（与 collected 写入同身份）。
func seqWFWriteFromNode(n *CodeSeqNode, entry, repo string) seqWFWrite {
	st := n.Evidence.Storage
	id := ""
	if st.ErrorResult != nil {
		id = st.ErrorResult.CallID
	}
	if id == "" {
		id = "write@" + n.Evidence.CallFile + ":" + itoaSeq(n.Evidence.CallLine)
	}
	return seqWFWrite{Entry: entry, Repo: repo, File: n.Evidence.CallFile,
		Line: n.Evidence.CallLine, ID: id, Table: st.Table, Op: st.Operation,
		Guard: st.Guard, Values: st.Writes, Preds: st.Predicates}
}

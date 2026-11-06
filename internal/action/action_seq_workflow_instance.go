package action

// U04f：条件化请求实例构建（从连接生成；不把实例数量当成请求已经发生）。

import (
	"sort"
	"strings"
)

// seqWFReadValuesMap 连接读取值 → read id/column 索引。
func seqWFReadValuesMap(conns []SeqWorkflowConnection, idxs []int) map[string]map[string]*SeqStorageOrigin {
	out := map[string]map[string]*SeqStorageOrigin{}
	for _, i := range idxs {
		if i < 0 || i >= len(conns) {
			continue
		}
		for _, v := range conns[i].ReadValues {
			if out[v.ReadID] == nil {
				out[v.ReadID] = map[string]*SeqStorageOrigin{}
			}
			if prev, ok := out[v.ReadID][v.Column]; ok {
				// 不同写入给同一读取列冲突值 → 未知（不混历史值与最终值）。
				if !sameSeqScalarOrigin(prev, v.Origin) {
					out[v.ReadID][v.Column] = nil
				}
				continue
			}
			out[v.ReadID][v.Column] = v.Origin
		}
	}
	return out
}

// seqWFConnKeySig 连接键签名（同源同键的多次读取合并为一个实例）。
func seqWFConnKeySig(c SeqWorkflowConnection) string {
	var parts []string
	for _, k := range c.Keys {
		parts = append(parts, k.Written.Column+"="+seqWFOriginKey(k.Written.Origin)+
			"->"+k.Requested.Column+"="+seqWFOriginKey(k.Requested.Origin))
	}
	sort.Strings(parts)
	return c.To.Entry + "|" + strings.Join(parts, ",")
}

// seqBuildWorkflowInstances 从连接生成实例（BFS；共享取消/预算）。
func seqBuildWorkflowInstances(entries []SeqWorkflowEntry, conns []SeqWorkflowConnection,
	ctxErr func() error,
	reanalyze func(entry string, seeds map[string]string,
		params []*SeqStorageOrigin) (*CodeSeqNode, error),
	snap *RuntimeSnapshot, reads []seqWFRead) ([]SeqWorkflowInstance, bool, []string, error) {
	if len(entries) == 0 {
		return nil, false, nil, nil
	}
	root := entries[0]
	rootTree := seqWFEntryTree(entries, root.Symbol)
	if rootTree == nil {
		return nil, false, nil, nil
	}
	// U04fR：节点上限必须在克隆/加入结果**之前**检查（超限 → 0 实例）。
	if countSeqNodes(rootTree) > seqWFInstanceNodes {
		return nil, true, []string{"instance_node_budget"}, nil
	}
	guards := map[string]bool{}
	seqWFScanGuards(rootTree, guards)
	rootSeq, rootWrites, rootProofs := seqWFAnnotateTree(rootTree, nil, guards, root.Symbol, root.Repo)
	valueBudget := false
	if seqWFHasOversizeWrite(rootWrites) {
		valueBudget = true
	}
	rootInst := &SeqWorkflowInstance{ID: "instance-1", Kind: "selected_root",
		Verdict: "unproven", Entry: root.Symbol, Repo: root.Repo, ConnectionIndex: 0,
		Ordering: SeqOrderingUnproven, MissingProofs: appendUniqueStr(rootProofs, SeqProofRequestOrder),
		Writes: seqWFInstanceWrites(rootWrites), Sequence: rootSeq}
	rootState := &seqWFInstState{inst: rootInst, writes: rootWrites, guards: guards,
		ancestors: map[string]bool{}}
	out := []SeqWorkflowInstance{*rootInst}
	partial := valueBudget
	var boundaries []string
	if valueBudget {
		boundaries = appendUniqueStr(boundaries, "instance_value_origin")
	}
	totalNodes := countSeqNodes(rootSeq)
	queue := []*seqWFInstState{rootState}
	for len(queue) > 0 {
		if ctxErr != nil && ctxErr() != nil {
			return nil, false, nil, ErrSeqCanceled
		}
		st := queue[0]
		queue = queue[1:]
		reach := map[string]bool{}
		for _, w := range st.writes {
			reach[w.ID] = true
		}
		if len(reach) == 0 {
			continue
		}
		depth := len(st.ancestors)
		groups := map[string][]int{}
		var order []string
		for i := range conns {
			c := &conns[i]
			if c.From.Entry != st.inst.Entry || !reach[c.WriteID] {
				continue
			}
			sig := seqWFConnKeySig(*c)
			if _, ok := groups[sig]; !ok {
				order = append(order, sig)
			}
			groups[sig] = append(groups[sig], i)
		}
		for _, sig := range order {
			idxs := groups[sig]
			if depth >= seqWFInstanceDepth {
				partial = true
				boundaries = appendUniqueStr(boundaries, "instance_depth_limit")
				break
			}
			if len(out) >= seqWFInstanceMax {
				partial = true
				boundaries = appendUniqueStr(boundaries, "instance_budget")
				break
			}
			c := conns[idxs[0]]
			target := seqWFEntryTree(entries, c.To.Entry)
			if target == nil {
				continue
			}
			// 目标入口参数绑定 + 读取值合并。
			params := map[int]*SeqStorageOrigin{}
			var bindings []SeqInstanceBinding
			readIdxs := append([]int{}, idxs...)
			for _, i := range idxs {
				for _, k := range conns[i].Keys {
					idx, ok := seqWFParamIndex(k.Requested.Origin, c.To.Entry)
					if !ok {
						continue
					}
					resolved := seqWFResolveOrigin(st, k.Written.Origin)
					if params[idx] != nil && !sameSeqScalarOrigin(params[idx], resolved) {
						continue // 冲突绑定拒绝
					}
					params[idx] = resolved
				}
			}
			maxIdx := -1
			for idx := range params {
				if idx > maxIdx {
					maxIdx = idx
				}
			}
			plist := make([]*SeqStorageOrigin, maxIdx+1)
			for idx, o := range params {
				plist[idx] = o
				bindings = append(bindings, SeqInstanceBinding{Parameter: idx,
					Origin: cloneSeqScalarOrigin(o), Source: st.inst.ID})
			}
			readVals := seqWFReadValuesMap(conns, readIdxs)
			// U04fR：超过 64KiB 的值来源不能参与读取替换/后续连接。
			if seqWFDropOversizeReadValues(readVals) {
				partial = true
				boundaries = appendUniqueStr(boundaries, "instance_value_origin")
			}
			// B08：实例读取值参与运行时快照查询（配置表等）。
			seqWFRuntimeReadValues(snap, reads, readVals)
			guards2 := map[string]bool{}
			seqWFScanGuards(target, guards2)
			var proofs []string
			baseTree := target
			// B07：用实例新事实重解析受读取值影响的工厂/接口/回调前沿。
			if reanalyze != nil && len(readVals) > 0 && seqWFNeedsReanalysis(target) {
				if seeds := seqWFInstanceSeeds(target, conns, idxs); len(seeds) > 0 {
					if rt, rerr := reanalyze(c.To.Entry, seeds, plist); rerr != nil {
						return nil, false, nil, rerr
					} else if rt != nil {
						seqWFMergeBranchConditions(rt, target)
						mg := seqWFMergeGuards(guards2, func() map[string]bool {
							m := map[string]bool{}
							seqWFScanGuards(rt, m)
							return m
						}())
						guards2 = mg
						baseTree = rt
						proofs = appendUniqueStr(proofs, "instance_reanalysis")
					}
				}
			}
			// U04fR：加入结果前先检查节点预算（超限不派生该实例）。
			if totalNodes+countSeqNodes(baseTree) > seqWFInstanceNodes {
				return out, true, appendUniqueStr(boundaries, "instance_node_budget"), nil
			}
			seq, writes, anProofs := seqWFAnnotateTree(baseTree, readVals, guards2, c.To.Entry, c.To.Repo)
			proofs = appendMissing(proofs, anProofs...)
			if seqWFHasOversizeWrite(writes) {
				partial = true
				boundaries = appendUniqueStr(boundaries, "instance_value_origin")
			}
			// related reads（同一实例里的额外读取）。
			var related []SeqRelatedRead
			for _, i := range idxs {
				related = append(related, SeqRelatedRead{ReadID: conns[i].ReadID,
					WriteID: conns[i].WriteID, ConnectionIndex: i, ReadValues: conns[i].ReadValues})
			}
			if len(idxs) > 1 {
				proofs = appendUniqueStr(proofs, SeqProofJointReads)
				for _, i := range idxs {
					for _, p := range conns[i].MissingProofs {
						proofs = appendUniqueStr(proofs, p)
					}
				}
			}
			id := "instance-" + itoaSeq(len(out)+1)
			inst := SeqWorkflowInstance{ID: id, Kind: "conditional_external_request",
				Verdict: "conditional", Entry: c.To.Entry, Repo: c.To.Repo,
				ParentID: st.inst.ID, ConnectionIndex: idxs[0], Ordering: SeqOrderingUnproven,
				Bindings: bindings, ReadValues: flattenWFReadValues(conns, idxs),
				RelatedReads: related, Writes: seqWFInstanceWrites(writes),
				MissingProofs: appendMissing(append([]string{}, c.MissingProofs...), proofs...),
				Sequence:      seq}
			out = append(out, inst)
			totalNodes += countSeqNodes(seq)
			// 祖先循环/重复状态 → 不再派生（标 partial）。
			key := inst.Entry + "|" + strings.Join(seqWFWriteIDs(writes), ",")
			if st.ancestors[key] {
				partial = true
				boundaries = appendUniqueStr(boundaries, "instance_cycle")
				continue
			}
			anc := copyBoolMap(st.ancestors)
			anc[key] = true
			child := &seqWFInstState{inst: &out[len(out)-1], params: plist,
				writes: writes, guards: guards2, ancestors: anc}
			if len(writes) > 0 {
				queue = append(queue, child)
			}
		}
	}
	return out, partial, boundaries, nil
}

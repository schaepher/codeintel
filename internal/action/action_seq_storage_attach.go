package action

// 结构化 storage 证据的挂载（U04b/U04c/U04d/U04e）。

import "go/ast"

// seqAttachWriteErrorResult 写入证据 → 错误返回槽（U04h）。
func seqAttachWriteErrorResult(node *CodeSeqNode, call *ast.CallExpr, c *seqPersistCtx) {
	if node == nil || node.Evidence == nil || node.Evidence.Storage == nil {
		return
	}
	if c == nil || c.req == nil {
		return
	}
	res := seqWriteErrorResult(*c.req, node, call, c)
	if res == nil {
		return
	}
	node.Evidence.Storage.ErrorResult = res
	if c.req.writeCalls != nil {
		c.req.writeCalls[call] = res
	}
}

// seqNoSnapshotRuntime 无快照时的运行时边界（T19）。
func seqNoSnapshotRuntime(rd *SeqStorageRead) *SeqRuntimeEvidence {
	if rd == nil {
		return nil
	}
	ev := &SeqRuntimeEvidence{Reason: "snapshot-missing", Result: "unknown",
		Table: rd.Table, Columns: append([]string(nil), rd.Columns...),
		Symbol: rd.ResourceDeclaration}
	for _, p := range rd.Predicates {
		val := p.Value.Expression
		if p.Value.Kind == SeqStorageValueLiteral {
			val = p.Value.SQL
		}
		ev.Query = append(ev.Query, p.Column+" eq "+val)
	}
	if rd.Table != "" && len(rd.Columns) > 0 {
		ev.Boundary = seqRuntimeBoundaryFor(SeqRuntimeCodeSnapshot)
		return ev
	}
	// 查询形态/动态 SQL 未证明：只给分析边界，不索要事实。
	ev.Reason = "query-not-proven"
	ev.Boundary = seqRuntimeBoundaryFor(SeqRuntimeCodeQuery)
	ev.Table, ev.Columns = "", nil
	return ev
}

// seqAttachORMEvidence 挂载 XORM 操作证据（U04f）。
func seqAttachORMEvidence(node *CodeSeqNode, call *ast.CallExpr, c *seqPersistCtx) {
	if node == nil || node.Evidence == nil {
		return
	}
	if orm := seqXormEvidence(node, call, c); orm != nil {
		node.Evidence.ORM = orm
	}
}

// seqAttachStorageRead 把结构化读取证据写到调用节点（U04e；资源绑定复用
// 运行时快照结论，无快照省略）。
func seqAttachStorageRead(node *CodeSeqNode, call *ast.CallExpr, c *seqPersistCtx) {
	if node == nil || node.Evidence == nil {
		return
	}
	rd := seqStorageReadEvidence(node, call, c)
	if rd == nil {
		return
	}
	if c != nil && c.runtime != nil {
		if res := c.runtime.writeResource(call); res != nil {
			cp := *res
			rd.Resource = &cp
		} else {
			rd.Resource = &SeqStorageResource{Status: SeqResourceNotVisited,
				SnapshotHash: c.runtime.snap.Hash}
		}
	} else if node.Evidence.Runtime == nil {
		// T19：没有快照时，已证明的读取至少给出**可采集的缺口**（不索要配置）。
		node.Evidence.Runtime = seqNoSnapshotRuntime(rd)
	}
	node.Evidence.StorageRead = rd
}

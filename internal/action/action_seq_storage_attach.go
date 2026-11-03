package action

// 结构化 storage 证据的挂载（U04b/U04c/U04d/U04e）。

import "go/ast"

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
	}
	node.Evidence.StorageRead = rd
}

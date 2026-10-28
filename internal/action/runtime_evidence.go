package action

import "go/ast"

// todo25 §13：运行时快照证据（挂在 SQL/Scan 调用节点上；JSON 与文本共用）。

// SeqEvidenceRuntime 运行时快照来源（SQL/Scan 调用节点）。
const SeqEvidenceRuntime = "runtime_snapshot"

// attachRuntimeEvidence 把快照读取证据挂到调用节点（不覆盖已有调用边证据）。
func attachRuntimeEvidence(req CodeSequenceRequest, node *CodeSeqNode, call *ast.CallExpr) {
	ev := req.runtime.fact(call)
	if ev == nil {
		return
	}
	if node.Evidence == nil {
		node.Evidence = &SeqEvidence{Source: SeqEvidenceRuntime, CallFile: req.curFile,
			CallLine: node.Line, Reason: "runtime-snapshot"}
	}
	node.Evidence.Runtime = ev
}

// SeqRuntimeEvidence 一次快照读取的可核对来源。
type SeqRuntimeEvidence struct {
	SnapshotHash string   `json:"snapshot_hash"`
	SnapshotPath string   `json:"snapshot_path,omitempty"`
	Version      int      `json:"version"`
	SourceID     string   `json:"source_id"`
	Table        string   `json:"table"`
	Columns      []string `json:"columns,omitempty"`
	Query        []string `json:"query,omitempty"` // 实际查询谓词（field op value）
	Args         []string `json:"args,omitempty"`
	Scope        []string `json:"scope,omitempty"`
	Rows         int      `json:"rows"`
	Result       string   `json:"result,omitempty"` // row|rows|empty|unknown
	Reason       string   `json:"reason,omitempty"`
	ObservedAt   string   `json:"observed_at,omitempty"`
	Revision     string   `json:"revision,omitempty"`
	ScanTarget   string   `json:"scan_target,omitempty"`
	ReturnSlot   string   `json:"return_slot,omitempty"`
	Compare      string   `json:"compare,omitempty"` // 运行时比较位置/条件
}

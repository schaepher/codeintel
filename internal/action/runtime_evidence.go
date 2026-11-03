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

// 运行时事实边界（T19）：缺口必须有明确类别与所需事实。
const (
	SeqRuntimeKindMissingFact = "missing_fact"
	SeqRuntimeKindAnalysis    = "analysis_boundary"
	SeqRuntimeKindAmbiguous   = "ambiguous_fact"
	SeqRuntimeCodeSnapshot    = "snapshot_missing"
	SeqRuntimeCodeBinding     = "binding_missing"
	SeqRuntimeCodeRows        = "incomplete_rows"
	SeqRuntimeCodeColumns     = "columns_missing"
	SeqRuntimeCodeScope       = "scope_not_covered"
	SeqRuntimeCodeObservation = "missing_observation"
	SeqRuntimeCodeQuery       = "query_not_proven"
	SeqRuntimeCodeAmbiguous   = "ambiguous_observations"
)

// SeqRuntimeBoundary 运行时事实的缺口类别/代码/所需事实。
type SeqRuntimeBoundary struct {
	Kind  string   `json:"kind"`
	Code  string   `json:"code"`
	Needs []string `json:"needs,omitempty"`
}

// seqRuntimeBoundaryFor 由原因/缺口代码给出边界（needs 只列真正需要的事实）。
func seqRuntimeBoundaryFor(code string) *SeqRuntimeBoundary {
	switch code {
	case SeqRuntimeCodeSnapshot:
		return &SeqRuntimeBoundary{Kind: SeqRuntimeKindMissingFact, Code: code, Needs: []string{"snapshot"}}
	case SeqRuntimeCodeBinding:
		return &SeqRuntimeBoundary{Kind: SeqRuntimeKindMissingFact, Code: code, Needs: []string{"resource_binding"}}
	case SeqRuntimeCodeRows:
		return &SeqRuntimeBoundary{Kind: SeqRuntimeKindMissingFact, Code: code, Needs: []string{"complete_rows"}}
	case SeqRuntimeCodeColumns:
		return &SeqRuntimeBoundary{Kind: SeqRuntimeKindMissingFact, Code: code, Needs: []string{"columns"}}
	case SeqRuntimeCodeScope:
		return &SeqRuntimeBoundary{Kind: SeqRuntimeKindMissingFact, Code: code, Needs: []string{"scope"}}
	case SeqRuntimeCodeObservation:
		return &SeqRuntimeBoundary{Kind: SeqRuntimeKindMissingFact, Code: code, Needs: []string{"observation"}}
	case SeqRuntimeCodeAmbiguous:
		return &SeqRuntimeBoundary{Kind: SeqRuntimeKindAmbiguous, Code: code}
	case SeqRuntimeCodeQuery:
		return &SeqRuntimeBoundary{Kind: SeqRuntimeKindAnalysis, Code: code}
	}
	return nil
}

// SeqRuntimeEvidence 一次快照读取的可核对来源。
type SeqRuntimeEvidence struct {
	SnapshotHash string              `json:"snapshot_hash"`
	SnapshotPath string              `json:"snapshot_path,omitempty"`
	Version      int                 `json:"version"`
	SourceID     string              `json:"source_id"`
	Symbol       string              `json:"symbol,omitempty"` // 源码已证明的资源符号（T19）
	Table        string              `json:"table"`
	Columns      []string            `json:"columns,omitempty"`
	Query        []string            `json:"query,omitempty"` // 实际查询谓词（field op value）
	Args         []string            `json:"args,omitempty"`
	Scope        []string            `json:"scope,omitempty"`
	Rows         int                 `json:"rows"`
	Result       string              `json:"result,omitempty"` // row|rows|empty|unknown
	Reason       string              `json:"reason,omitempty"`
	ObservedAt   string              `json:"observed_at,omitempty"`
	Revision     string              `json:"revision,omitempty"`
	ScanTarget   string              `json:"scan_target,omitempty"`
	ReturnSlot   string              `json:"return_slot,omitempty"`
	Compare      string              `json:"compare,omitempty"`  // 运行时比较位置/条件
	Boundary     *SeqRuntimeBoundary `json:"boundary,omitempty"` // T19：缺口类别/代码/所需事实
}

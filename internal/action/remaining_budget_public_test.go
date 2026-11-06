package action

import (
	"fmt"
	"strings"
	"testing"
)

func TestRemainingWorkflowBudgets(t *testing.T) {
	parameter := func(entry string) *SeqStorageOrigin {
		i := 0
		return &SeqStorageOrigin{Kind: SeqOriginParameter, ScalarType: "string", Repo: "repo", Symbol: entry, Parameter: &i}
	}
	write := &SeqStorage{Operation: "insert", Table: "nodes", ResourceDeclaration: "DB", Writes: []SeqStorageWrite{{Column: "id", Value: SeqStorageValue{Kind: SeqStorageValueArgument, Origin: parameter("submit")}}, {Column: "kind", Value: SeqStorageValue{Kind: SeqStorageValueArgument, Origin: &SeqStorageOrigin{Kind: SeqOriginLiteral, ScalarType: "string", Literal: `"profile"`}}}}}
	read := &SeqStorageRead{Table: "nodes", Columns: []string{"kind"}, ResourceDeclaration: "DB", Result: &SeqSQLReadResult{ID: "read-1", Entry: "review", Repo: "repo", Type: "Row"}, Predicates: []SeqStoragePredicate{{Column: "id", Value: SeqStorageValue{Kind: SeqStorageValueArgument, Origin: parameter("review")}}}}
	root := &CodeSeqNode{Kind: "call", Nodes: []*CodeSeqNode{{Kind: "call", Evidence: &SeqEvidence{Source: SeqEvidenceAST, CallFile: "flow.go", CallLine: 1, Storage: write}}}}
	target := &CodeSeqNode{Kind: "call", Nodes: []*CodeSeqNode{{Kind: "call", Evidence: &SeqEvidence{Source: SeqEvidenceAST, CallFile: "flow.go", CallLine: 2, StorageRead: read}}}}
	entries := []SeqWorkflowEntry{{Symbol: "submit", Repo: "repo", Sequence: root}, {Symbol: "review", Repo: "repo", Sequence: target}}
	for len(root.Nodes) <= 65536 {
		root.Nodes = append(root.Nodes, &CodeSeqNode{Kind: "call"})
	}
	conns, _ := seqWorkflowConnections(entries, 4096, 256)
	instances, partial, reasons, err := seqBuildWorkflowInstances(entries, conns, nil, nil, nil, nil)
	fmt.Printf("node: instances=%d partial=%t boundaries=%v error=%v\n", len(instances), partial, reasons, err)
	root.Nodes = root.Nodes[:1]
	write.Writes[1].Value.Origin.Literal = strings.Repeat("x", 65537)
	instances, partial, reasons, err = seqBuildWorkflowInstances(entries[:1], nil, nil, nil, nil, nil)
	fmt.Printf("value: instances=%d partial=%t boundaries=%v error=%v\n", len(instances), partial, reasons, err)
}

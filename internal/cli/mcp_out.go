package cli

// #235 MCP 输出 schema 自描述：handler 返回类型化输出（Out 非 any）——
// go-sdk 自动生成 OutputSchema（tools/list 可见）+ 校验 + 写入
// StructuredContent。结构体 snake_case tag 即契约（docs/json-contract.md）。
// 能复用领域/action 契约类型的直接复用（CodeContext/TablePathResult/
// TraceRow/Fact 等）；此处定义 MCP 组装层的输出结构。

import (
	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
)

// FactBrief 边的端点摘要（symbol 工具的 callers/callees）。
type FactBrief struct {
	ID         string  `json:"id"`
	Tool       string  `json:"tool,omitempty"`
	Confidence float64 `json:"confidence"`
}

// NodeBrief 节点摘要（roots/impact/file_symbols 等列表项）。
type NodeBrief struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
	File string `json:"file,omitempty"`
	Line int    `json:"line,omitempty"`
}

// SymbolOut symbol 工具输出。
type SymbolOut struct {
	ID        string      `json:"id"`
	Name      string      `json:"name"`
	Kind      string      `json:"kind"`
	File      string      `json:"file,omitempty"`
	Line      int         `json:"line,omitempty"`
	Signature string      `json:"signature,omitempty"`
	Doc       string      `json:"doc,omitempty"`
	Callers   []FactBrief `json:"callers,omitempty"`
	Callees   []FactBrief `json:"callees,omitempty"`
}

// FieldsOut fields 工具输出。
type FieldsOut struct {
	Func string                         `json:"func"`
	Rows []*domain.FunctionFieldSummary `json:"rows"`
}

// GraphOut callers/callees 工具输出。
type GraphOut struct {
	Target string         `json:"target"`
	Rows   []*domain.Fact `json:"rows"`
}

// ImpactOut impact 工具输出。
type ImpactOut struct {
	Target string      `json:"target"`
	Nodes  []NodeBrief `json:"nodes"`
}

// TraceOut trace 工具输出。
type TraceOut struct {
	Steps []*domain.TraceRow `json:"steps"`
}

// ValueTraceOut value_trace 工具输出。
type ValueTraceOut struct {
	Flows []*domain.TraceRow `json:"flows"`
}

// SummaryOut summary 工具输出（顶层数组，保持原契约）。
type SummaryOut []action.SummaryStep

// ModuleCallsOut module_calls 工具输出。
type ModuleCallsOut struct {
	Calls []action.ModuleCall `json:"calls"`
}

// BatchOut batch_symbols 工具输出。
type BatchOut struct {
	Results []*action.BatchResult `json:"results"`
}

// TableOut table 工具输出（顶层数组，保持原契约）。
type TableOut []*domain.TableColumn

// RelationsOut relations 工具输出（顶层数组，保持原契约）。
type RelationsOut []*domain.TableRelation

// RootsOut roots 工具输出。
type RootsOut struct {
	Roots []NodeBrief `json:"roots"`
}

// RepoSummaryOut repo_summary 工具输出。
type RepoSummaryOut struct {
	Nodes  int               `json:"nodes"`
	Edges  int               `json:"edges"`
	Tables int               `json:"tables"`
	Build  *domain.BuildMeta `json:"build,omitempty"`
}

// FileSymbolsOut file_symbols 工具输出。
type FileSymbolsOut struct {
	Symbols []NodeBrief `json:"symbols"`
}

// factBriefs 边端点转 FactBrief（#235 输出类型化；factIDs 保留给 CLI）。
func factBriefs(facts []*domain.Fact, endpoint string) []FactBrief {
	out := make([]FactBrief, 0, len(facts))
	for _, f := range facts {
		id := f.SourceID
		if endpoint == "target" {
			id = f.TargetID
		}
		out = append(out, FactBrief{ID: string(id), Tool: f.ToolSource, Confidence: f.Confidence})
	}
	return out
}

// nodeBriefList 节点转 NodeBrief（#235 输出类型化；nodeBriefs 保留给 CLI）。
func nodeBriefList(nodes []*domain.CodeEntity) []NodeBrief {
	out := make([]NodeBrief, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, NodeBrief{ID: string(n.ID), Name: n.Name, Kind: string(n.Kind), File: n.FilePath, Line: n.LineStart})
	}
	return out
}

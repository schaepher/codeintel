package cli

// 工具参数结构体（json tag = inputSchema 字段名，snake_case）。
type symbolParams struct {
	Repo string `json:"repo,omitempty"` // #232 多仓库：空=默认仓库；否则短名/路径/module（Q238）
	ID   string `json:"id"`             // 符号名或 canonical ID
}
type fieldsParams struct {
	Repo string `json:"repo,omitempty"` // #232 多仓库：空=默认仓库；否则短名/路径/module（Q238）
	Func string `json:"func"`           // 函数名或 canonical ID
}
type graphParams struct {
	Repo   string `json:"repo,omitempty"` // #232 多仓库：空=默认仓库；否则短名/路径/module（Q238）
	Symbol string `json:"symbol"`         // 符号名或 canonical ID
	Depth  int    `json:"depth"`          // 深度（默认 1/3）
}
type traceParams struct {
	Repo     string `json:"repo,omitempty"` // #232 多仓库：空=默认仓库；否则短名/路径/module（Q238）
	Field    string `json:"field"`          // 类型限定字段路径（如 example.com/m.T.A）
	Func     string `json:"func"`           // 函数名
	Dir      string `json:"dir"`            // backward（产生点）或 forward（使用点）
	MaxDepth int    `json:"max_depth"`      // 深度（默认 8）
}
type valueTraceParams struct {
	Repo     string  `json:"repo,omitempty"` // #232 多仓库：空=默认仓库；否则短名/路径/module（Q238）
	Node     string  `json:"node"`           // 节点 ID（如 symbol:go:...:main#t.A.read@5）
	MaxDepth int     `json:"max_depth"`      // 深度（默认 8）
	MinConf  float64 `json:"min_conf"`       // 候选边置信度剪枝（默认 1.0）
}
type contextParams struct {
	Repo string `json:"repo,omitempty"` // #232 多仓库：空=默认仓库；否则短名/路径/module（Q238）
	Node string `json:"node"`           // 符号/字段路径
}
type tableParams struct {
	Repo string `json:"repo,omitempty"` // #232 多仓库：空=默认仓库；否则短名/路径/module（Q238）
	Name string `json:"name"`           // 表名
}
type relationsParams struct {
	Repo       string `json:"repo,omitempty"` // #232 多仓库：空=默认仓库；否则短名/路径/module（Q238）
	Table      string `json:"table"`          // 表名
	Type       string `json:"type"`           // 关联类型（逗号分隔，默认 query,write）
	MaxHops    int    `json:"max_hops"`       // 跳数上限
	MaxResults int    `json:"max_results"`    // 条数上限
}
type tablePathParams struct {
	Repo    string `json:"repo,omitempty"` // #232 多仓库：空=默认仓库；否则短名/路径/module（Q238）
	From    string `json:"from"`           // 起始表名
	To      string `json:"to"`             // 目标表名
	MaxHops int    `json:"max_hops"`       // 跳数上限（默认 6）
}
type summaryParams struct {
	Repo string `json:"repo,omitempty"` // #232 多仓库：空=默认仓库；否则短名/路径/module（Q238）
	Node string `json:"node"`           // 锚点（符号/字段路径）
}
type moduleCallsParams struct {
	Repo   string `json:"repo,omitempty"` // #232 多仓库：空=默认仓库；否则短名/路径/module（Q238）
	Module string `json:"module"`         // module 名过滤（可选）
}

// #228 写操作工具参数：batch_symbols 批量概览；update/init 重建索引
// （stale 自愈——Agent 一条消息从「过期」到「可用」）。
type batchParams struct {
	Repo    string   `json:"repo,omitempty"` // #232 多仓库：空=默认仓库；否则短名/路径/module（Q238）
	Symbols []string `json:"symbols"`        // 符号名列表（单输入失败跳过，部分成功）
}
type updateParams struct {
	Repo string `json:"repo,omitempty"` // #232 多仓库：空=默认仓库；否则短名/路径/module（Q238）
}

// #229 file:line 定位参数。
type fileSymbolsParams struct {
	Repo string `json:"repo,omitempty"` // #232 多仓库：空=默认仓库；否则短名/路径/module（Q238）
	File string `json:"file"`           // 文件路径（精确或相对/省略前缀）
	Line int    `json:"line"`           // 行号（1 起）
}

// buildResult 重建结果摘要（update/init 工具输出，snake_case 契约）。
type buildResult struct {
	Status       string `json:"status"`                  // success/degraded/failed/up_to_date/needs_full_build
	ChangedFiles int    `json:"changed_files,omitempty"` // 变更文件数（init 为 0）
	Nodes        int    `json:"nodes,omitempty"`
	Edges        int    `json:"edges,omitempty"`
	SkippedEdges int    `json:"skipped_edges,omitempty"`
	DurationMs   int64  `json:"duration_ms,omitempty"`
	CommitSHA    string `json:"commit_sha,omitempty"`
	Message      string `json:"message,omitempty"` // 提示信息（无变更/需全量重建等）
}

// grpcRoutesParams gRPC 路由清单查询参数（R29）。
type grpcRoutesParams struct {
	Repo string `json:"repo"` // 目标仓库（缺省用默认仓库）
}

func (p grpcRoutesParams) getRepo() string { return p.Repo }

// enumsParams 枚举查询参数（R6：include_untyped 控制是否含无类型常量）。
type enumsParams struct {
	Repo           string `json:"repo"` // 目标仓库（缺省用默认仓库）
	IncludeUntyped bool   `json:"include_untyped,omitempty"`
}

func (p enumsParams) getRepo() string { return p.Repo }

// entitiesParams 实体协作图查询参数（R9）。
type entitiesParams struct {
	Repo string `json:"repo"` // 目标仓库（缺省用默认仓库）
}

func (p entitiesParams) getRepo() string { return p.Repo }

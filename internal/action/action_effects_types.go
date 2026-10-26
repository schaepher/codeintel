package action

// EffectsRequest query effects 参数。
type EffectsRequest struct {
	Target  string // 函数/方法（canonical ID 或名称）
	RepoAbs string
	Depth   int // 展开层级（1 = 只本函数；默认 1）
	Scene   Scene
	// todo12-1：目标筛选（互斥；空 = 不筛选，保持既有契约）
	TargetField  string // 类型限定字段路径（pkg.Type.Field）
	TargetReturn string // 返回槽名字或 #序号
	TargetSet    bool   // 是否显式指定了目标（用于区分"未指定"与"空值"）
	PathBudget   int    // 路径枚举预算（0 = 默认 24）
}

// DBEffect 一次表列写入。
type DBEffect struct {
	Table       string       `json:"table,omitempty"`
	Column      string       `json:"column,omitempty"`
	Field       string       `json:"field"`                  // SSA 摘要的字段路径
	Function    string       `json:"function"`               // 写入函数（canonical ID）
	File        string       `json:"file,omitempty"`         // 写入函数所在文件（被调写入定位用）
	Line        int          `json:"line,omitempty"`         // 写入行
	Access      string       `json:"access"`                 // direct_write / indirect_write
	Origin      string       `json:"origin,omitempty"`       // todo12-8：ast|callee|summary（写点来源）
	Kind        string       `json:"kind,omitempty"`         // todo12-8 R2：field_assign|db_write|callee_write
	CommitScope string       `json:"commit_scope,omitempty"` // begin_commit|outside_or_unknown|none
	Evidence    *SeqEvidence `json:"evidence,omitempty"`
}

// RemoteEffect 一次远程写（grpc/http 出站）。
type RemoteEffect struct {
	Kind     string       `json:"kind"` // grpc | http
	Service  string       `json:"service"`
	Method   string       `json:"method,omitempty"`
	Path     string       `json:"path,omitempty"`
	Function string       `json:"function"`
	Line     int          `json:"line,omitempty"`
	Evidence *SeqEvidence `json:"evidence,omitempty"`
}

// TxScope 事务范围（观察到的事实，不含"是否回滚"的推断）。
type TxScope struct {
	Kind         string `json:"kind"` // none|begin_commit|tx_wrapper
	BeginLine    int    `json:"begin_line,omitempty"`
	CommitLine   int    `json:"commit_line,omitempty"`
	RollbackLine int    `json:"rollback_line,omitempty"`
	CoversWrites bool   `json:"covers_writes"` // 写入行是否落在 [begin, commit) 之间
	Verdict      string `json:"verdict"`
	Detail       string `json:"detail,omitempty"`
}

// ResultEffect 返回结果与可疑覆盖。
// ErrorSource 一条返回槽的错误来源判定（todo12-8 R3）。
type ErrorSource struct {
	Slot          int    `json:"slot"`
	Expr          string `json:"expr"`
	Var           string `json:"var,omitempty"`
	Line          int    `json:"line,omitempty"`
	Status        string `json:"status"` // returned|wrapped|swallowed|overwritten|stale|nil|unknown
	OverwrittenAt int    `json:"overwritten_at,omitempty"`
	Detail        string `json:"detail,omitempty"`
}
type ResultEffect struct {
	Returns    []string `json:"returns,omitempty"`    // 返回表达式文本
	Slots      []string `json:"slots,omitempty"`      // 结果槽
	Line       int      `json:"line,omitempty"`       // return 行
	ErrorVars  []string `json:"error_vars,omitempty"` // 被当作错误的变量
	Suspicious []string `json:"suspicious"`           // "错误被后续赋值覆盖"的可疑点（file:line；空数组也输出）
	// todo12-8：每条返回槽的错误来源（区分返回/覆盖/吞掉/旧错误）
	ErrorSources []ErrorSource `json:"error_sources,omitempty"`
	Verdict      string        `json:"verdict"`
	Detail       string        `json:"detail,omitempty"`
}

// EffectsResult query effects 输出契约。
type EffectsResult struct {
	Target   string `json:"target"`
	Function string `json:"function"`
	File     string `json:"file,omitempty"`
	// todo12-1：目标筛选（不指定目标时省略，保持既有契约）
	TargetInfo   *EffectsTarget   `json:"target_info,omitempty"`
	Analysis     *EffectsAnalysis `json:"analysis,omitempty"`
	DBWrites     []DBEffect       `json:"db_writes"`
	RemoteWrites []RemoteEffect   `json:"remote_writes"`
	Paths        []EffectPath     `json:"paths,omitempty"` // todo11-4：按执行路径
	Tx           TxScope          `json:"tx"`
	Result       ResultEffect     `json:"result"`
	Diagnostics  []SeqDiagCount   `json:"diagnostics,omitempty"`
}

// tx 调用名（扫描 AST 用）。
var txBeginNames = map[string]bool{"begin": true, "begintx": true, "beginx": true}
var txCommitNames = map[string]bool{"commit": true, "committx": true}
var txRollbackNames = map[string]bool{"rollback": true, "rollbacktx": true}
var txWrapperNames = map[string]bool{"transaction": true, "tx": true, "withtransaction": true}

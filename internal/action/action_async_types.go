package action

import "github.com/schaepher/codeintel/internal/domain"

// AsyncNode 一个生命周期入口（人工动作/内部审核/定时任务/消息消费等）。
type AsyncNode struct {
	ID       string `json:"id"`   // canonical ID
	Name     string `json:"name"` // 短名
	Role     string `json:"role"` // human|audit|cron|consumer|auto|other
	RoleRule string `json:"role_rule,omitempty"`
	// todo12-6：入口注册证据（如 registration:ScheduleAdvance）——名称启发式
	// 只保留低等级标记，注册证据才是 corroborated。
	RoleEvidence string `json:"role_evidence,omitempty"`
	File         string `json:"file,omitempty"`
	Line         int    `json:"line,omitempty"`
	Verdict      string `json:"verdict"` // 角色判定的支持程度
}

// AsyncLink 两个入口之间的关联（带因果证据）。
type AsyncLink struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Reason  string `json:"reason"`  // call_path|value_origin|field_shared|name_only|shared_scope_only
	Verdict string `json:"verdict"` // direct|corroborated|unverified
	// EvidenceKind 证据种类（todo11-3）：把"启发式候选"与"经调用路径/值来源
	// 验证的因果"分开——field_shared/name_only/shared_scope_only 都是启发式
	EvidenceKind string `json:"evidence_kind"`
	Detail       string `json:"detail,omitempty"`
}

// AsyncLifecycleRequest query async 参数。
type AsyncLifecycleRequest struct {
	Seed    string // 入口符号或表/类型名
	RepoAbs string
}

// AsyncLifecycleResult 异步生命周期关联结果。
type AsyncLifecycleResult struct {
	Seed        string         `json:"seed"`
	Scope       string         `json:"scope,omitempty"` // 作用域（字段路径前缀——类型/表近似）
	Nodes       []AsyncNode    `json:"nodes"`
	Links       []AsyncLink    `json:"links"`
	Diagnostics []SeqDiagCount `json:"diagnostics,omitempty"`
	// todo12-5：候选预算与完整性（节点/关联/排序使用同一集合）
	CandidateTotal  int    `json:"candidate_total,omitempty"`
	CandidateBudget int    `json:"candidate_budget,omitempty"`
	CandidateBasis  string `json:"candidate_basis,omitempty"` // todo13-5：field_identity|call_identity
	Partial         bool   `json:"partial,omitempty"`
	// todo12-6：跨入口身份链（写 → 持久化/队列边界 → 读/过滤）与完整性
	Chains          []AsyncChain `json:"chains,omitempty"`
	IdentityPartial bool         `json:"identity_partial,omitempty"`
}

// AsyncChain 跨入口值身份链（逐跳可核对；边界未建模 → partial）。
type AsyncChain struct {
	From    string     `json:"from"`
	To      string     `json:"to"`
	Field   string     `json:"field"`
	Hops    []AsyncHop `json:"hops"`
	Verdict string     `json:"verdict"`
	Reason  string     `json:"reason,omitempty"`
}

// AsyncHop 身份链的一跳（写入/边界/读取/过滤）。
type AsyncHop struct {
	Kind     string `json:"kind"` // write|persistence|persistence_or_queue|read|filter
	Function string `json:"function,omitempty"`
	Field    string `json:"field,omitempty"`
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
	Verdict  string `json:"verdict"`
	Detail   string `json:"detail,omitempty"`
}

// asyncCandidateBudget 候选入口预算（防止把全仓字段摘要函数当相关入口）。
const asyncCandidateBudget = 200

// 角色常量。
const (
	AsyncRoleHuman    = "human"
	AsyncRoleAudit    = "audit"
	AsyncRoleCron     = "cron"
	AsyncRoleConsumer = "consumer"
	AsyncRoleAuto     = "auto"
	AsyncRoleOther    = "other"
)

// asyncFnFacts 一个函数的异步关联素材（来自 SSA 摘要 + 调用链）。
type asyncFnFacts struct {
	node   *domain.CodeEntity
	writes map[string]bool
	reads  map[string]bool
	// todo12-6：字段读写行（身份链逐跳位置）
	wlines map[string]int
	rlines map[string]int
}

package action

// todo10-6：关联异步生命周期并保留因果证据（`query async`）。
//
// 判据（用户确认的 Q3）——"仅共享作用域（类型/表）不算因果"：
//   (a) 字段链路：一个入口写状态字段 F、另一个入口读/写同一 F → corroborated
//       reason=field:<F>（值流可达性未逐条验证，故为 corroborated 而非 direct）
//   (b) 调用链：两个入口在同一 calls 链上（seed 的链路集合）→ corroborated
//       reason=call_chain
//   (c) 标识同源：两个入口都触碰同名 ID 类字段（*_id/*_no/*_code/Id/No/Code）
//       → corroborated reason=id_field:<col>
// 仅同作用域（字段前缀相同）而无上述证据 → **unverified** +
// reason=shared_scope_only（明确标注"不构成因果"，但不隐藏）。
//
// 角色（人工动作/内部审核/定时任务/消息消费者）按符号名启发式判定，标注为
// corroborated 并给出 rule；无法判定为 unknown（不猜）。

import (
	"fmt"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// AsyncNode 一个生命周期入口（人工动作/内部审核/定时任务/消息消费等）。
type AsyncNode struct {
	ID       string `json:"id"`   // canonical ID
	Name     string `json:"name"` // 短名
	Role     string `json:"role"` // human|audit|cron|consumer|auto|other
	RoleRule string `json:"role_rule,omitempty"`
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
	Verdict  string `json:"verdict"` // 角色判定的支持程度
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
	CandidateTotal  int  `json:"candidate_total,omitempty"`
	CandidateBudget int  `json:"candidate_budget,omitempty"`
	Partial         bool `json:"partial,omitempty"`
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

// asyncRoleRule 角色判定规则（先匹配先返回；只按名字，必须标注为启发式）。
var asyncRoleRules = []struct {
	role string
	keys []string
}{
	{AsyncRoleConsumer, []string{"consume", "listener", "subscribe", "onmessage", "handlemsg"}},
	{AsyncRoleCron, []string{"cron", "timer", "tick", "sweep", "schedule", "job"}},
	{AsyncRoleAudit, []string{"audit", "review", "check", "verify"}},
	{AsyncRoleHuman, []string{"approve", "manual", "human", "confirm", "byhuman"}},
	{AsyncRoleAuto, []string{"auto", "system", "bysystem", "advance", "apply"}},
}

// asyncClassifyRole 按名字判定角色（返回 role + 命中的规则词）。
func asyncClassifyRole(name string) (role, rule string) {
	lower := strings.ToLower(name)
	for _, r := range asyncRoleRules {
		for _, k := range r.keys {
			if strings.Contains(lower, k) {
				return r.role, "name:" + k
			}
		}
	}
	return AsyncRoleOther, "name:none"
}

// asyncFnFacts 一个函数的异步关联素材（来自 SSA 摘要 + 调用链）。
type asyncFnFacts struct {
	node   *domain.CodeEntity
	writes map[string]bool
	reads  map[string]bool
}

// AsyncLifecycle 关联入口相关的生命周期事件（人工/审核/定时/消费）与因果。
func (a *Actions) AsyncLifecycle(req AsyncLifecycleRequest) (*AsyncLifecycleResult, error) {
	logger := zap.L()
	logger.Info("enter (Actions).AsyncLifecycle", zap.String("seed", req.Seed))
	defer logger.Info("exit (Actions).AsyncLifecycle")

	seed, err := a.ResolveSymbol(req.Seed)
	if err != nil {
		// 表/类型名搜索保留；canonical ID 不存在必须明确报错（todo12-5 R2）
		if strings.HasPrefix(req.Seed, "symbol:") || strings.HasPrefix(req.Seed, "file:") {
			return nil, err
		}
		seed = nil
	}
	sums, err := a.repo.AllSummaries()
	if err != nil {
		return nil, err
	}
	facts := map[string]*asyncFnFacts{} // functionID → 素材
	for _, s := range sums {
		id := string(s.FunctionID)
		f := facts[id]
		if f == nil {
			f = &asyncFnFacts{writes: map[string]bool{}, reads: map[string]bool{}}
			facts[id] = f
		}
		switch s.AccessKind {
		case domain.SummaryDirectWrite, domain.SummaryIndirectWrite:
			f.writes[s.FieldPath] = true
		default:
			f.reads[s.FieldPath] = true
		}
	}
	// seed 的字段集合（决定作用域）
	seedFields := map[string]bool{}
	scope := ""
	if seed != nil {
		if f := facts[string(seed.ID)]; f != nil {
			for p := range f.writes {
				seedFields[p] = true
			}
			for p := range f.reads {
				seedFields[p] = true
			}
		}
	}
	if len(seedFields) == 0 && seed != nil {
		// 无字段摘要：作用域退回**包前缀**（同包函数进候选——这正是
		// "只共享作用域"的典型场景，关联会标 unverified 而不是被隐藏）
		scope = asyncPkgPrefix(string(seed.ID))
	}
	// todo12-5：候选入口只取**与 seed 有具体关系**的函数——字段身份精确
	// 相同、同一结构体类型前缀（共享作用域，关联标 unverified）、
	// ID 类列同源；不再把"公共包前缀"扩成全仓函数。
	seedTypePrefixes := map[string]bool{}
	for p := range seedFields {
		if i := strings.LastIndex(p, "."); i > 0 {
			seedTypePrefixes[p[:i]] = true
		}
	}
	idColumns := asyncIDColumns(seedFields)
	candidates := map[string]bool{}
	for id, f := range facts {
		if seed != nil && id == string(seed.ID) {
			continue
		}
		fields := map[string]bool{}
		for p := range f.writes {
			fields[p] = true
		}
		for p := range f.reads {
			fields[p] = true
		}
		for p := range fields {
			if seedFields[p] || asyncSharesType(p, seedTypePrefixes) || asyncSharesIDColumn(p, idColumns) {
				candidates[id] = true
				break
			}
		}
		// 无字段摘要的 seed：同包函数只作为"共享作用域"候选（预算内）
		if len(seedFields) == 0 && strings.HasPrefix(scope, "symbol:") && strings.HasPrefix(id, scope) {
			candidates[id] = true
		}
	}
	// 调用链上的函数也算候选（人工动作 → 内部审核这类同步段）
	chain := map[string]bool{}
	if seed != nil {
		chain = chainSymbols(a, string(seed.ID))
		for id := range chain {
			if id != string(seed.ID) {
				candidates[id] = true
			}
		}
	}
	// todo12-5 R3：候选预算 + 稳定排序（seed 必保留；实际数量/预算可审计）
	candIDs := make([]string, 0, len(candidates))
	for id := range candidates {
		candIDs = append(candIDs, id)
	}
	sortStrings(candIDs)
	total := len(candIDs)
	if total > asyncCandidateBudget {
		candIDs = candIDs[:asyncCandidateBudget]
	}
	// todo11-3：真实可达索引（calls 边）——"双方都在展开集合"不再算可达
	reach := a.callsReachIndex()
	// todo11-3：值来源指纹（summary_origins：callee + 调用行）。AllSummaries
	// 不带 origins，故按候选函数逐个取（节点数 ≤20，成本可控）。
	origins := a.asyncOriginIndex(funcsForOrigins(seed, candidates))

	res := &AsyncLifecycleResult{Seed: req.Seed, Nodes: []AsyncNode{}, Links: []AsyncLink{},
		CandidateTotal: total, CandidateBudget: asyncCandidateBudget}
	if scope == "" {
		if len(seedTypePrefixes) > 0 {
			ps := make([]string, 0, len(seedTypePrefixes))
			for p := range seedTypePrefixes {
				ps = append(ps, p)
			}
			sortStrings(ps)
			scope = strings.Join(ps, ",")
		} else {
			scope = asyncScopeOf(seedFields)
		}
	}
	res.Scope = scope
	if total > asyncCandidateBudget {
		res.Partial = true
		res.Diagnostics = append(res.Diagnostics, SeqDiagCount{Code: "candidates_truncated",
			Count: total - asyncCandidateBudget,
			Detail: fmt.Sprintf("候选入口 %d 个超过预算 %d，按 canonical ID 稳定排序保留前 %d 个",
				total, asyncCandidateBudget, asyncCandidateBudget)})
	}
	// 节点：seed + 候选（有索引节点的才有位置）
	addNode := func(id string) *AsyncNode {
		n, err := a.repo.GetSymbol(domain.CanonicalID(id))
		if err != nil || n == nil {
			return nil
		}
		role, rule := asyncClassifyRole(n.Name)
		verdict := SeqVerdictCorroborated
		if rule == "name:none" {
			verdict = SeqVerdictUnverified
		}
		nd := AsyncNode{ID: id, Name: n.Name, Role: role, RoleRule: rule,
			File: n.FilePath, Line: n.LineStart, Verdict: verdict}
		res.Nodes = append(res.Nodes, nd)
		return &res.Nodes[len(res.Nodes)-1]
	}
	if seed != nil {
		addNode(string(seed.ID))
	}
	for _, id := range candIDs {
		addNode(id)
	}
	sort.Slice(res.Nodes, func(i, j int) bool { return res.Nodes[i].ID < res.Nodes[j].ID })
	if len(res.Nodes) <= 1 {
		res.Diagnostics = append(res.Diagnostics, SeqDiagCount{Code: "no_lifecycle_entries", Count: 1})
	}

	// 关联：节点/关系使用同一 scoped 集合（不再用"前 20 个"另开窗口）
	limit := len(res.Nodes)
	for i := 0; i < limit; i++ {
		for j := i + 1; j < limit; j++ {
			if link, ok := asyncLink(res.Nodes[i], res.Nodes[j], facts, reach, origins); ok {
				res.Links = append(res.Links, link)
			}
		}
	}
	sort.Slice(res.Links, func(i, j int) bool {
		if res.Links[i].From != res.Links[j].From {
			return res.Links[i].From < res.Links[j].From
		}
		return res.Links[i].To < res.Links[j].To
	})
	if len(res.Links) == 0 {
		res.Diagnostics = append(res.Diagnostics, SeqDiagCount{Code: "no_related_links", Count: 1,
			Detail: "未发现与 seed 有证据的关联（共享作用域不算因果）——不是空成功，也不填充无关关系"})
	}
	return res, nil
}

// asyncSharesType 字段的类型前缀是否属于 seed 的类型集合（共享作用域）。
func asyncSharesType(field string, seedTypes map[string]bool) bool {
	for t := range seedTypes {
		if strings.HasPrefix(field, t+".") {
			return true
		}
	}
	return false
}

// asyncIDColumns seed 字段里的 ID 类列（*_id/*_no/*_code/Id/No/Code）。
func asyncIDColumns(fields map[string]bool) map[string]bool {
	out := map[string]bool{}
	for p := range fields {
		leaf := p
		if i := strings.LastIndex(p, "."); i >= 0 {
			leaf = p[i+1:]
		}
		l := strings.ToLower(leaf)
		if strings.HasSuffix(l, "id") || strings.HasSuffix(l, "no") ||
			strings.HasSuffix(l, "code") || strings.HasSuffix(l, "_id") ||
			strings.HasSuffix(l, "_no") || strings.HasSuffix(l, "_code") {
			out[l] = true
		}
	}
	return out
}

// asyncSharesIDColumn 字段是否为同源 ID 列（同名 ID 类字段——仍需链路验证）。
func asyncSharesIDColumn(field string, cols map[string]bool) bool {
	if len(cols) == 0 {
		return false
	}
	leaf := field
	if i := strings.LastIndex(field, "."); i >= 0 {
		leaf = field[i+1:]
	}
	return cols[strings.ToLower(leaf)]
}

// asyncInScope 字段是否落在同一作用域（seed 字段集合，或名的近似匹配）。
func asyncInScope(field string, seedFields map[string]bool, scope string) bool {
	if scope != "" {
		return strings.Contains(strings.ToLower(field), strings.ToLower(scope))
	}
	typePrefix := asyncScopeOf(seedFields)
	if typePrefix == "" {
		return false
	}
	return strings.HasPrefix(field, typePrefix)
}

// asyncScopeOf 字段路径的公共类型前缀（去掉末段字段名）——表/类型近似。
func asyncScopeOf(fields map[string]bool) string {
	prefix := ""
	for p := range fields {
		i := strings.LastIndex(p, ".")
		if i <= 0 {
			continue
		}
		pre := p[:i]
		if prefix == "" {
			prefix = pre
			continue
		}
		for !strings.HasPrefix(pre, prefix) && prefix != "" {
			j := strings.LastIndex(prefix, ".")
			if j < 0 {
				prefix = ""
				break
			}
			prefix = prefix[:j]
		}
	}
	return prefix
}

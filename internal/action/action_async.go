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

// AsyncLink 两个入口之间的关联（带因果证据）。

// AsyncLifecycleRequest query async 参数。

// AsyncLifecycleResult 异步生命周期关联结果。

// AsyncChain 跨入口值身份链（逐跳可核对；边界未建模 → partial）。

// AsyncHop 身份链的一跳（写入/边界/读取/过滤）。

// asyncCandidateBudget 候选入口预算（防止把全仓字段摘要函数当相关入口）。

// 角色常量。

// asyncRoleRule 角色判定规则（先匹配先返回；只按名字，必须标注为启发式）。

// asyncClassifyRole 按名字判定角色（返回 role + 命中的规则词）。

// asyncFnFacts 一个函数的异步关联素材（来自 SSA 摘要 + 调用链）。

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
			f = &asyncFnFacts{writes: map[string]bool{}, reads: map[string]bool{},
				wlines: map[string]int{}, rlines: map[string]int{}}
			facts[id] = f
		}
		switch s.AccessKind {
		case domain.SummaryDirectWrite, domain.SummaryIndirectWrite:
			f.writes[s.FieldPath] = true
			if s.LineStart > 0 {
				f.wlines[s.FieldPath] = s.LineStart
			}
		default:
			f.reads[s.FieldPath] = true
			if s.LineStart > 0 {
				f.rlines[s.FieldPath] = s.LineStart
			}
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
	// todo13-5：seed 自身无摘要时，用**直接被调者**的字段摘要推导调用/
	// 值身份（Review→AdvanceBySystem 的 Step.Count）；仍不把同包全量
	// 函数当候选。推导仅一层，避免把无关下游整图拉进来。
	seedHasOwnFields := len(seedFields) > 0
	if !seedHasOwnFields && seed != nil {
		for id := range a.directCalleesOf(string(seed.ID)) {
			if f := facts[id]; f != nil {
				for p := range f.writes {
					seedFields[p] = true
				}
				for p := range f.reads {
					seedFields[p] = true
				}
			}
		}
	}
	if len(seedFields) == 0 && seed != nil {
		// 完全无摘要：作用域退回**包前缀**（仅用于展示；候选仍只取调用身份）
		scope = asyncPkgPrefix(string(seed.ID))
	}
	// todo12-5：候选入口只取**与 seed 有具体关系**的函数——字段身份精确
	// 相同、同一结构体类型前缀（共享作用域，关联标 unverified）、
	// ID 类列同源；不再把"公共包前缀"扩成全仓函数。
	// todo13-5：无字段摘要时**不得**把同包函数当候选（大仓实测会成为
	// 2000 个无关类型/19900 条无关关系）——仅取 seed 的调用/值身份。
	seedHasFields := len(seedFields) > 0
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
		if !seedHasFields {
			continue // 下方按调用/值身份收集
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
		// todo13-5：反向直接调用者同样属于“调用身份”（谁触发 seed）
		for id := range a.directCallersOf(string(seed.ID)) {
			candidates[id] = true
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
	if seedHasOwnFields {
		res.CandidateBasis = "field_identity"
	} else {
		res.CandidateBasis = "call_identity"
		if seed != nil {
			detail := "seed 无字段摘要：候选仅取调用/值身份关联，不以同包全量函数代替"
			if seedHasFields {
				detail += "（已用直接被调者的字段摘要推导身份）"
			}
			res.Diagnostics = append(res.Diagnostics, SeqDiagCount{Code: "seed_fields_missing", Count: 1, Detail: detail})
		}
	}
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
	// todo12-6：入口注册证据（calls 边来源名）——名称启发式只作低等级
	reg := a.asyncRegistrationIndex()
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
		verdict := SeqVerdictUnverified // 名称启发式：低等级标记（todo12-6 R3）
		evidence := ""
		if ev, ok := reg[id]; ok {
			role, rule = ev.role, "registration:"+ev.api
			verdict, evidence = SeqVerdictCorroborated, ev.evidence
		}
		nd := AsyncNode{ID: id, Name: n.Name, Role: role, RoleRule: rule,
			RoleEvidence: evidence, File: n.FilePath, Line: n.LineStart, Verdict: verdict}
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
	// todo12-6：跨入口身份链（写 → 边界 → 读/过滤），边界未建模独立标 partial
	res.Chains = a.asyncIdentityChains(res.Links, res.Nodes, facts)
	boundaryMiss := 0
	for _, c := range res.Chains {
		for _, h := range c.Hops {
			if h.Kind == "persistence_or_queue" {
				boundaryMiss++
			}
		}
		if c.Verdict != SeqVerdictCorroborated {
			res.IdentityPartial = true
		}
	}
	if boundaryMiss > 0 {
		res.Diagnostics = append(res.Diagnostics, SeqDiagCount{Code: "persistence_boundary_unverified",
			Count:  boundaryMiss,
			Detail: "写入与读取之间的持久化/消息边界未建模（静态仅见字段身份；同一列不代表同一条记录）"})
	}
	return res, nil
}

// asyncSharesType 字段的类型前缀是否属于 seed 的类型集合（共享作用域）。

// asyncIDColumns seed 字段里的 ID 类列（*_id/*_no/*_code/Id/No/Code）。

// asyncSharesIDColumn 字段是否为同源 ID 列（同名 ID 类字段——仍需链路验证）。

// asyncInScope 字段是否落在同一作用域（seed 字段集合，或名的近似匹配）。

// asyncScopeOf 字段路径的公共类型前缀（去掉末段字段名）——表/类型近似。

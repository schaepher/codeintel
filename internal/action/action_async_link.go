package action

// todo10-6：异步关联的因果判据（从 action_async.go 拆出——行数治理）。
//
// 三条判据 + "仅同作用域不算因果"：
//   (a) field:<F>   一方写、另一方读/写同名字段 → corroborated
//   (b) call_chain  两者都在 seed 的调用链上 → corroborated
//   (c) id_field:<col> 同名 ID 类字段 → corroborated
//   (none) shared_scope_only → unverified（明确不构成因果，但不隐藏）

import (
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// asyncLink 两个入口的关联判定（Q3 的三条判据 + 仅同作用域的 unverified）。
func asyncLink(x, y AsyncNode, facts map[string]*asyncFnFacts, reach reachIndex,
	origins map[string]map[string]string) (AsyncLink, bool) {
	link := AsyncLink{From: x.ID, To: y.ID}

	// (1) 调用路径可达（todo11-3 收紧）：必须是**真实可达**——x 能沿 calls 边
	// 走到 y（或反向），而不是"双方都在 seed 的展开集合里"（同入口下互不
	// 调用的两个分支会被误判）。
	if reach.reaches(x.ID, y.ID) || reach.reaches(y.ID, x.ID) {
		link.Reason, link.Verdict, link.EvidenceKind = "call_path", SeqVerdictCorroborated, "call_path"
		link.Detail = "调用路径可达（calls 边 BFS）"
		return link, true
	}

	fx, fy := facts[x.ID], facts[y.ID]
	if fx == nil || fy == nil {
		return link, false
	}
	// (2) 身份字段（*_id/*Id/_no/_code）优先——异步生命周期靠身份传递串起来：
	// 值来源指纹一致 → value_origin（corroborated）；都非空但不同 →
	// name_only + 明确"不构成同一生命周期"；来源未知 → name_only（启发式）。
	for p := range allFields(fx) {
		leaf := fieldLeaf(p)
		if !asyncIDLike(leaf) {
			continue
		}
		for q := range allFields(fy) {
			if fieldLeaf(q) != leaf {
				continue
			}
			ox, oy, ok := identityOrigins(origins, x.ID, y.ID, p)
			switch {
			case ok && ox == oy:
				link.Reason, link.Verdict, link.EvidenceKind = "value_origin:"+leaf, SeqVerdictCorroborated, "value_origin"
				link.Detail = "同名标识值来源一致（" + leaf + "：" + ox + "）"
				return link, true
			case ok && ox != oy:
				link.Reason, link.Verdict, link.EvidenceKind = "name_only:"+leaf, SeqVerdictUnverified, "name_only"
				link.Detail = "同名标识但值来源不同（" + ox + " vs " + oy + "）——不构成同一生命周期"
				return link, true
			default:
				link.Reason, link.Verdict, link.EvidenceKind = "name_only:"+leaf, SeqVerdictUnverified, "name_only"
				link.Detail = "仅同名标识字段（" + leaf + "），值来源未知——启发式候选"
				return link, true
			}
		}
	}
	// (3) 字段链路：一方写、另一方读/写同名字段——只是**启发式**（读写同一
	// 字段不等于同一次异步因果；身份字段已在 (2) 处理）。
	for p := range fx.writes {
		if fy.reads[p] || fy.writes[p] {
			ox, oy, ok := identityOrigins(origins, x.ID, y.ID, p)
			if ok && ox == oy {
				link.Reason, link.Verdict, link.EvidenceKind = "value_origin:"+p, SeqVerdictCorroborated, "value_origin"
				link.Detail = "字段值来源指纹一致（" + p + "：" + ox + "）"
				return link, true
			}
			link.Reason, link.Verdict, link.EvidenceKind = "field_shared:"+p, SeqVerdictUnverified, "field_shared"
			link.Detail = "共享字段读写（" + p + "）——启发式，未验证值流可达"
			return link, true
		}
	}
	// (4) 仅同作用域：明确标注不构成因果
	sx, sy := asyncScopeOf(allFields(fx)), asyncScopeOf(allFields(fy))
	if sx != "" && sx == sy {
		link.Reason, link.Verdict, link.EvidenceKind = "shared_scope_only", SeqVerdictUnverified, "field_shared"
		link.Detail = "只共享作用域（类型/表），不构成因果证据"
		return link, true
	}
	// 同一包（无字段摘要时）：同样只是"作用域"，标 unverified
	if asyncPkgPrefix(x.ID) == asyncPkgPrefix(y.ID) {
		link.Reason, link.Verdict, link.EvidenceKind = "shared_scope_only", SeqVerdictUnverified, "field_shared"
		link.Detail = "只共享包作用域，不构成因果证据"
		return link, true
	}
	return link, false
}

// identityOrigins 两个函数对同一字段的"值来源指纹"（summary_origins 的
// callee + 调用行；空表示索引没给来源）。指纹相同 → 值来源相同（身份传递
// 可核验）；都非空且不同 → 同名不同源（不得认定同一生命周期）。
func identityOrigins(origins map[string]map[string]string, x, y, field string) (ox, oy string, ok bool) {
	ox = origins[x][field]
	oy = origins[y][field]
	if ox == "" || oy == "" {
		return ox, oy, false
	}
	return ox, oy, true
}

// reachIndex 调用路径可达（calls 边邻接表 + 按起点缓存 BFS）。
type reachIndex struct {
	adj   map[string][]string
	cache map[string]map[string]bool
}

// reaches from 是否能沿 calls 边走��� to。
func (r reachIndex) reaches(from, to string) bool {
	if from == "" || to == "" || from == to {
		return false
	}
	if r.cache == nil {
		return false
	}
	seen, ok := r.cache[from]
	if !ok {
		seen = map[string]bool{}
		queue := []string{from}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			for _, nxt := range r.adj[cur] {
				if seen[nxt] {
					continue
				}
				seen[nxt] = true
				queue = append(queue, nxt)
			}
		}
		r.cache[from] = seen
	}
	return seen[to]
}

// allFields 函数涉及的字段集合（读 + 写）。
func allFields(f *asyncFnFacts) map[string]bool {
	out := map[string]bool{}
	if f == nil {
		return out
	}
	for p := range f.writes {
		out[p] = true
	}
	for p := range f.reads {
		out[p] = true
	}
	return out
}

// fieldLeaf 字段路径末段。
func fieldLeaf(p string) string {
	if i := strings.LastIndex(p, "."); i >= 0 {
		return p[i+1:]
	}
	return p
}

// asyncIDLike 末段是否 ID 类字段（*_id / *Id / id / *_no / *_code）。
func asyncIDLike(leaf string) bool {
	l := strings.ToLower(leaf)
	return l == "id" || strings.HasSuffix(l, "_id") || strings.HasSuffix(l, "id") ||
		strings.HasSuffix(l, "_no") || strings.HasSuffix(l, "_code")
}

// asyncPkgPrefix 从 canonical ID 取"同包"前缀（symbol:go:<pkg>:）。
func asyncPkgPrefix(id string) string {
	i := strings.LastIndex(id, ":")
	if i < 0 {
		return ""
	}
	return id[:i+1]
}

// callsReachIndex 构建 calls 边邻接表（一次加载；按起点惰性 BFS）。
func (a *Actions) callsReachIndex() reachIndex {
	idx := reachIndex{adj: map[string][]string{}, cache: map[string]map[string]bool{}}
	facts, err := a.repo.GetFactsByKinds(string(domain.FactCalls))
	if err != nil {
		return idx
	}
	for _, f := range facts {
		src, dst := string(f.SourceID), string(f.TargetID)
		idx.adj[src] = append(idx.adj[src], dst)
	}
	return idx
}

// funcsForOrigins 取指纹的函数集合（seed + 候选；去重）。
func funcsForOrigins(seed *domain.CodeEntity, candidates map[string]bool) []domain.CanonicalID {
	var out []domain.CanonicalID
	if seed != nil {
		out = append(out, seed.ID)
	}
	for id := range candidates {
		out = append(out, domain.CanonicalID(id))
	}
	return out
}

// asyncOriginIndex 值来源指纹：function → field → "callee@line,callee@line"。
// 只取写类摘要的 origins（间接写来源）；同函数同字段多来源按排序拼接。
func (a *Actions) asyncOriginIndex(funcs []domain.CanonicalID) map[string]map[string]string {
	var sums []*domain.FunctionFieldSummary
	for _, id := range funcs {
		rows, err := a.repo.GetFunctionFields(id)
		if err != nil {
			continue
		}
		sums = append(sums, rows...)
	}
	out := map[string]map[string]string{}
	agg := map[string]map[string][]string{}
	for _, s := range sums {
		if len(s.Origins) == 0 {
			continue
		}
		if s.AccessKind == domain.SummaryDirectRead {
			continue
		}
		fn := string(s.FunctionID)
		if agg[fn] == nil {
			agg[fn] = map[string][]string{}
		}
		for _, o := range s.Origins {
			agg[fn][s.FieldPath] = append(agg[fn][s.FieldPath], string(o.CalleeID)+"@"+itoaSeq(o.CallLine))
		}
	}
	for fn, byField := range agg {
		out[fn] = map[string]string{}
		for field, items := range byField {
			sortStrings(items)
			out[fn][field] = strings.Join(items, ",")
		}
	}
	return out
}

// ---- todo12-6：跨入口值身份链与入口注册证据 ----

// asyncRegistrationIndex 入口注册证据：calls 边的调用方名含注册/调度/消费
// 关键词 → 目标函数视为已注册入口（名称启发式只作低等级）。
func (a *Actions) asyncRegistrationIndex() map[string]string {
	out := map[string]string{}
	facts, err := a.repo.GetFactsByKinds(string(domain.FactCalls))
	if err != nil {
		return out
	}
	for _, f := range facts {
		caller := shortNameOf(string(f.SourceID))
		role, _ := asyncClassifyRole(caller)
		if role == AsyncRoleOther {
			continue
		}
		target := string(f.TargetID)
		if prev, ok := out[target]; !ok || caller < prev {
			out[target] = caller
		}
	}
	return out
}

// asyncRoleFromRegistration 注册调用方 → 角色（caller 名含关键词）。
func asyncRoleFromRegistration(caller string) (role, rule string) {
	role, _ = asyncClassifyRole(caller)
	return role, "registration:" + caller
}

// asyncIdentityChains 由关联证据构建逐跳身份链（写 → 边界 → 读/过滤）。
// 绑定关系本身来自 asyncLink（值来源/字段共享），这里只把"值从哪里写、
// 经过什么边界、被谁读/过滤"展开成可核对的行。
func (a *Actions) asyncIdentityChains(links []AsyncLink, nodes []AsyncNode,
	facts map[string]*asyncFnFacts) []AsyncChain {
	if len(links) == 0 {
		return nil
	}
	fileOf := map[string]string{}
	lineOf := map[string]int{}
	for _, n := range nodes {
		fileOf[n.ID] = n.File
		lineOf[n.ID] = n.Line
	}
	colIdx := a.tableColumnIndex()
	var out []AsyncChain
	seen := map[string]bool{}
	for _, l := range links {
		kind, field := splitAsyncReason(l.Reason)
		if field == "" {
			continue
		}
		key := l.From + "\x00" + l.To + "\x00" + field
		if seen[key] {
			continue
		}
		seen[key] = true
		wfn, wline, wfield := asyncFieldSide(facts, l.From, l.To, field, true)
		rfn, rline, rfield := asyncFieldSide(facts, l.From, l.To, field, false)
		if wfn == "" || rfn == "" {
			continue
		}
		hops := []AsyncHop{{
			Kind: "write", Function: wfn, Field: wfield,
			File: fileOf[wfn], Line: wline, Verdict: SeqVerdictCorroborated,
			Detail: "字段写入（SSA 摘要 direct/indirect write）",
		}}
		leaf := fieldLeaf(wfield)
		if table, column, ok := colIdx.lookup(leaf); ok {
			hops = append(hops, AsyncHop{Kind: "persistence", Field: table + "." + column,
				Verdict: SeqVerdictUnverified,
				Detail:  "字段→表列映射（列名匹配）；同一列不代表同一条记录"})
		} else {
			hops = append(hops, AsyncHop{Kind: "persistence_or_queue", Field: leaf,
				Verdict: SeqVerdictUnverified,
				Detail:  "未建模持久化/消息边界（静态仅见字段身份）"})
		}
		hops = append(hops, AsyncHop{
			Kind: "read", Function: rfn, Field: rfield,
			File: fileOf[rfn], Line: rline, Verdict: SeqVerdictCorroborated,
			Detail: "字段读取（SSA 摘要）",
		})
		hops = append(hops, AsyncHop{Kind: "filter", Function: rfn, Field: rfield,
			Verdict: SeqVerdictUnverified,
			Detail:  "过滤/查询条件未建模（无法证明读到同一条记录）"})
		verdict := SeqVerdictUnverified
		reason := l.EvidenceKind
		if l.Verdict == SeqVerdictCorroborated && (kind == "value_origin" || kind == "call_path") {
			verdict = SeqVerdictCorroborated
		}
		out = append(out, AsyncChain{From: l.From, To: l.To, Field: wfield,
			Hops: hops, Verdict: verdict, Reason: reason})
	}
	return out
}

// splitAsyncReason "value_origin:field" → (kind, field)。
func splitAsyncReason(reason string) (string, string) {
	kind := reason
	field := ""
	if i := strings.Index(reason, ":"); i >= 0 {
		kind, field = reason[:i], reason[i+1:]
	}
	return kind, field
}

// asyncFieldSide 在 from/to 两侧找字段的写入方（write=true）或读取方。
func asyncFieldSide(facts map[string]*asyncFnFacts, from, to, field string, write bool) (fn string, line int, full string) {
	for _, id := range []string{from, to} {
		f := facts[id]
		if f == nil {
			continue
		}
		set, lines := f.reads, f.rlines
		if write {
			set, lines = f.writes, f.wlines
		}
		for p := range set {
			if p == field || fieldLeaf(p) == field {
				return id, lines[p], p
			}
		}
	}
	return "", 0, ""
}

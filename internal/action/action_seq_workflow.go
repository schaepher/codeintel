package action

// U04a：关联入口集合（`--related-entry`）——先校验全部入口，再分别分析；
// 入口集合与顺序不代表请求已经发生。
//
// 语义：
//   - 只接受准确 canonical 函数/方法 ID（当前仓库/workspace 解析，精确查询）；
//   - 重复入口只分析一次；根入口外按 canonical ID 稳定排序；最多 32 个不同入口；
//   - 每个入口使用独立的参数/局部状态/来源环境，共享请求取消信号与追加预算；
//   - `workflow.entries` 提供 symbol/repo/sequence，`ordering=unproven`；
//     `workflow.continuation` 统计整个集合的追加次数（不为每个入口重置预算）；
//   - 没有 `--related-entry` 时输出与原来完全一致（不产生 workflow 字段）。

import (
	"fmt"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// SeqRelatedEntriesMax 一次查询最多支持的不同入口数（含根入口）。
const SeqRelatedEntriesMax = 32

// SeqOrderingUnproven 入口/连接顺序未证明（不是"已经按顺序执行"）。
const SeqOrderingUnproven = "unproven"

// SeqWorkflow 关联入口的分析集合（挂在根节点的 workflow 字段）。
type SeqWorkflow struct {
	Entries []SeqWorkflowEntry `json:"entries"`
	// Ordering 固定为 unproven：入口集合不表示请求顺序。
	Ordering string `json:"ordering"`
	// Continuation 整个入口集合共享的追加预算统计。
	Continuation *SeqWorkflowContinuation `json:"continuation,omitempty"`
}

// SeqWorkflowEntry 单个入口的分析树。
type SeqWorkflowEntry struct {
	Symbol string `json:"symbol"`
	Repo   string `json:"repo"`
	// Sequence 该入口的代码级时序树（根入口的树是文档根的分析副本，
	// 不含 document 级 workflow 字段——避免 JSON 自引用）。
	Sequence *CodeSeqNode `json:"sequence,omitempty"`
}

// SeqWorkflowContinuation 集合级追加预算统计（与 meta.continuation 同口径，
// 但不是每个入口各自一份）。
type SeqWorkflowContinuation struct {
	SegmentsBudget int `json:"segments_budget"`
	SegmentsUsed   int `json:"segments_used"`
	Expanded       int `json:"expanded"`
	BudgetStops    int `json:"budget_stops"`
}

// seqValidateRelatedEntries 校验全部关联入口（准确 canonical 函数 ID +
// 当前仓库精确存在）。返回去重后的 ID（不含根入口）并按 ID 稳定排序；
// 任何入口不合法都直接报错（先验证全部入口，再执行分析）。
func (a *Actions) seqValidateRelatedEntries(rootID string, related []string) ([]string, error) {
	if len(related) == 0 {
		return nil, nil
	}
	seen := map[string]bool{rootID: true}
	out := make([]string, 0, len(related))
	for _, raw := range related {
		id := strings.TrimSpace(raw)
		if id == "" {
			return nil, fmt.Errorf("--related-entry 不接受空值（只接受准确 canonical 函数 ID）")
		}
		if !seqIsCanonicalFuncID(id) {
			return nil, fmt.Errorf("--related-entry %q 不是准确 canonical 函数 ID（需要 symbol:go:<import_path>:<name>）", id)
		}
		if seen[id] {
			continue
		}
		n, err := a.repo.GetSymbol(domain.CanonicalID(id))
		if err != nil {
			return nil, fmt.Errorf("--related-entry %s 不存在于当前索引（精确查询未命中）: %w", id, err)
		}
		switch n.Kind {
		case domain.KindFunction, domain.KindMethod:
		default:
			return nil, fmt.Errorf("--related-entry %s 不是函数/方法（kind=%s）", id, n.Kind)
		}
		seen[id] = true
		out = append(out, id)
		if len(seen) > SeqRelatedEntriesMax {
			return nil, fmt.Errorf("关联入口过多：最多 %d 个不同入口（含根入口）", SeqRelatedEntriesMax)
		}
	}
	sort.Strings(out)
	return out, nil
}

// seqIsCanonicalFuncID 形如 symbol:go:<import_path>:<name>（至少 3 个冒号后的
// 非空段）。不按名称/短名做任何猜测。
func seqIsCanonicalFuncID(id string) bool {
	if !strings.HasPrefix(id, "symbol:go:") {
		return false
	}
	rest := strings.TrimPrefix(id, "symbol:go:")
	parts := strings.Split(rest, ":")
	if len(parts) < 2 {
		return false
	}
	for _, p := range parts {
		if strings.TrimSpace(p) == "" {
			return false
		}
	}
	return true
}

// seqAnalyzeRelatedEntries 逐个分析关联入口（每个入口独立环境、共享预算与
// 取消信号）。root 用于生成根入口条目；返回 workflow 集合。
func (a *Actions) seqAnalyzeRelatedEntries(base CodeSequenceRequest, root *CodeSeqNode,
	rootID string, related []string) (*SeqWorkflow, error) {
	wf := &SeqWorkflow{Ordering: SeqOrderingUnproven}
	rootCopy := *root
	rootCopy.Workflow = nil
	wf.Entries = append(wf.Entries, SeqWorkflowEntry{
		Symbol: rootID, Repo: base.RepoAbs, Sequence: &rootCopy})
	for _, id := range related {
		sub := base
		sub.Target = id
		sub.RelatedEntries = nil
		sub.acc = base.acc // 共享：追加预算/取消/耗时账本
		sub.pkgSet = base.pkgSet
		sub.visited = map[string]bool{} // 每个入口独立的防环路径
		sub.entryID = ""
		sub.runtime = nil // 每个入口按自己的参数重新分析运行时快照
		// 共享 acc 的三个映射按 AST/节点对象键控——跨入口复用同一辅助函数时
		// 会互相覆盖绑定，进入新入口前清空由 seqInitRequestCaches 重建。
		if base.acc != nil {
			base.acc.callNodes = nil
			base.acc.pendingVarsByObj = nil
			base.acc.writeIDs = nil
		}
		tree, err := a.CodeSequence(sub)
		if err != nil {
			return nil, fmt.Errorf("关联入口 %s 分析失败: %w", id, err)
		}
		wf.Entries = append(wf.Entries, SeqWorkflowEntry{
			Symbol: id, Repo: base.RepoAbs, Sequence: tree})
	}
	if base.acc != nil {
		wf.Continuation = &SeqWorkflowContinuation{
			SegmentsBudget: base.ContinueSegments,
			SegmentsUsed:   base.acc.continueUsed,
			Expanded:       base.acc.continueExpansions,
			BudgetStops:    base.acc.continueBudgetStops,
		}
	}
	return wf, nil
}

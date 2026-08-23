package action

import (
	"fmt"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// Q244 意图命令（before/trace）：普通程序员入口——不问「用哪个子命令」，
// 只问「改这个会炸谁」（before）与「数据从哪来到哪去」（trace）。
// 目标形态分派：含 '.' 的字段路径优先字段、纯名优先表、回退符号。

// BeforeTarget 目标解析结果（形态分派）。
type BeforeTarget struct {
	Kind string `json:"kind"`          // symbol / field / table
	Name string `json:"name"`          // 显示名（符号名/字段路径/表名）
	ID   string `json:"id,omitempty"`  // symbol: canonical ID；field: 读节点 ID
}

// BeforeSummary 改动影响预判（Q244）——按目标形态聚合（缺省组省略）。
type BeforeSummary struct {
	Target BeforeTarget `json:"target"`
	// symbol 目标
	Callers []*domain.Fact       `json:"callers,omitempty"`
	Impact  []*domain.CodeEntity `json:"impact,omitempty"`
	// field 目标
	Writers []*domain.FunctionFieldSummary `json:"writers,omitempty"`
	Reads   []*domain.FunctionFieldSummary `json:"reads,omitempty"`
	// table 目标
	Relations []*domain.TableRelation `json:"relations,omitempty"`
	Columns   []*domain.TableColumn    `json:"columns,omitempty"`
}

// TraceFlow 数据来龙去脉（Q244）：值流全链 + 生命周期主链。
type TraceFlow struct {
	Target BeforeTarget       `json:"target"`
	Flows  []*domain.TraceRow `json:"flows,omitempty"` // 值流全链（跨函数双向）
	Chain  []SummaryStep      `json:"chain,omitempty"` // 生命周期主链
}

// ResolveBeforeTarget 目标形态分派（Q244）：
//   - 含 '.' 的输入先按字段路径解析（ResolveAnchor 支持字段回退），
//     失败再试符号/表
//   - 纯名输入先按表名（大小写不敏感），失败回退符号
func (a *Actions) ResolveBeforeTarget(input string) (*BeforeTarget, error) {
	logger := zap.L()
	logger.Info("enter (Actions).ResolveBeforeTarget", zap.String("input", input))
	defer logger.Info("exit (Actions).ResolveBeforeTarget")
	hasDot := strings.Contains(input, ".")
	if hasDot {
		if id, err := a.ResolveAnchor(input); err == nil {
			return &BeforeTarget{Kind: "field", Name: input, ID: string(id)}, nil
		}
	} else {
		if t, _, err := a.ResolveTableName(input); err == nil {
			return &BeforeTarget{Kind: "table", Name: t}, nil
		}
	}
	if n, err := a.ResolveSymbol(input); err == nil {
		return &BeforeTarget{Kind: "symbol", Name: n.Name, ID: string(n.ID)}, nil
	}
	if hasDot {
		if t, _, err := a.ResolveTableName(input); err == nil {
			return &BeforeTarget{Kind: "table", Name: t}, nil
		}
	}
	return nil, fmt.Errorf("目标 %q 未命中符号/字段/表（请检查名称）", input)
}

// Before 改动影响预判（Q244）：按目标形态聚合影响面。
func (a *Actions) Before(input string) (*BeforeSummary, error) {
	logger := zap.L()
	logger.Info("enter (Actions).Before", zap.String("input", input))
	defer logger.Info("exit (Actions).Before")
	tgt, err := a.ResolveBeforeTarget(input)
	if err != nil {
		return nil, err
	}
	sum := &BeforeSummary{Target: *tgt}
	switch tgt.Kind {
	case "symbol":
		if callers, err := a.Callers(domain.CanonicalID(tgt.ID), 2); err == nil {
			sum.Callers = callers
		}
		if impact, err := a.Impact(domain.CanonicalID(tgt.ID), 3); err == nil {
			sum.Impact = impact
		}
	case "field":
		// 全量摘要按 field_path 过滤（写/读行）
		all, err := a.repo.AllSummaries()
		if err != nil {
			return nil, err
		}
		for _, s := range all {
			if s.FieldPath != tgt.Name {
				continue
			}
			if strings.HasSuffix(s.AccessKind, "write") {
				sum.Writers = append(sum.Writers, s)
			} else {
				sum.Reads = append(sum.Reads, s)
			}
		}
	case "table":
		if rels, err := a.Relations(tgt.Name, ""); err == nil {
			sum.Relations = rels
		}
		if cols, err := a.Table(tgt.Name); err == nil {
			sum.Columns = cols
		}
	}
	return sum, nil
}

// TraceFlow 数据来龙去脉（Q244）：值流全链 + 生命周期主链。
func (a *Actions) TraceFlow(input string, maxDepth int) (*TraceFlow, error) {
	logger := zap.L()
	logger.Info("enter (Actions).TraceFlow", zap.String("input", input), zap.Int("max_depth", maxDepth))
	defer logger.Info("exit (Actions).TraceFlow")
	tgt, err := a.ResolveBeforeTarget(input)
	if err != nil {
		return nil, err
	}
	if maxDepth <= 0 {
		maxDepth = 8
	}
	flow := &TraceFlow{Target: *tgt}
	id := domain.CanonicalID(tgt.ID)
	switch tgt.Kind {
	case "field", "symbol":
		if rows, err := a.ValueTrace(id, maxDepth, 1.0, false); err == nil {
			flow.Flows = rows
		}
		if steps, err := a.SummaryChain(id); err == nil {
			flow.Chain = steps
		}
	case "table":
		if rels, err := a.Relations(tgt.Name, ""); err == nil {
			// 表目标的 flows 语义 = 关联链（relRows 展平为 steps 风格）
			flow.Chain = relSteps(rels)
		}
	}
	return flow, nil
}

// relSteps 表关联 → 生命周期链风格步骤（trace 表目标用）。
func relSteps(rels []*domain.TableRelation) []SummaryStep {
	steps := make([]SummaryStep, 0, len(rels))
	for _, r := range rels {
		steps = append(steps, SummaryStep{
			Kind: r.Type, Name: r.FromTable + "." + r.FromCol + " → " + r.ToTable + "." + r.ToCol,
		})
	}
	return steps
}

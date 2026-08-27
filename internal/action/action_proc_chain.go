package action

// R92 迁移：流程调用链数据（原 cli/wiki_processes.go 的 queryChain/
// procChain、wiki_keyflows.go 的 wikiKeyFlows、wiki_entities_order.go
// 的 sortChainByCallLine）——grpcProcMethods/httpProcEntries 的链数据
// 依赖随迁（数据查询属 action；渲染留 cli）。

import (
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// WikiKeyFlow 一个核心符号的字段读写摘要。
type WikiKeyFlow struct {
	Symbol string   `json:"symbol"`
	Reads  []string `json:"reads"`  // 字段路径（类型限定，去重）
	Writes []string `json:"writes"` // direct_write + indirect_write（去重）
}

// ValueLink value-trace 串联标注（R100 待办14-①）：入口写入字段 → 下游
// 被调者读取的交集——跨符号连接自动标注（流程页直接显示"谁写谁读"，
// 不必手动 query trace-backward/forward）。
type ValueLink struct {
	Field      string `json:"field"`       // 字段路径（类型限定）
	ProducedBy string `json:"produced_by"` // 写入符号（入口）
	ReadBy     string `json:"read_by"`     // 读取符号（下游被调者）
}

// ProcChain 一条流程的调用链（入口 + 边 + 涉及包）。
type ProcChain struct {
	Entry      string
	Steps      []domain.WikiSeqStep
	Pkgs       []string
	Miss       string        // R50：无调用链的原因（区分索引问题 vs 仅调用外部库）
	KeyFlows   []WikiKeyFlow // R78：链上符号关键数据流（字段读写——value-trace 串联）
	ValueLinks []ValueLink   // R100：入口写入 → 下游读取交集（跨符号串联标注）
}

// QueryChain 查询入口符号的深度 2 调用链 + 涉及包（短名展示）。
// R13：steps 按源码调用行号排序（SortChainByCallLine）——顺序与
// 实际代码一一对应（此前 SQL 遍历序与源码序不一致）。
// R50：无链原因区分——符号不存在（索引问题）vs 符号存在但无项目内
// 出边（仅调用外部库——go2o 实测 ParseFlags 只调 flag 标准库）。
func (a *Actions) QueryChain(entryName string) *ProcChain {
	logger := zap.L()
	logger.Info("enter (Actions).QueryChain", zap.String("entry", entryName))
	defer logger.Info("exit (Actions).QueryChain")
	entry, err := a.ResolveSymbol(entryName)
	if err != nil {
		return &ProcChain{Miss: "索引中无此符号——可能未重建索引"}
	}
	facts, err := a.Callees(entry.ID, 2)
	if err != nil {
		return &ProcChain{Entry: seqShort(string(entry.ID)), Miss: "查询调用链失败"}
	}
	// R75/R76：接口具体化——CalleesConcrete（action 层通用——wiki 与
	// query sequence 共用）：接口调用经 implements 落到实现，时序图
	// 反映实际执行逻辑而非接口列表
	facts, err = a.CalleesConcrete(entry.ID, 2)
	if err != nil {
		return &ProcChain{Entry: seqShort(string(entry.ID)), Miss: "查询调用链失败"}
	}
	chain := &ProcChain{Entry: seqShort(string(entry.ID))}
	chain.Steps = SortChainByCallLine(string(entry.ID), facts)
	// R78：流程页深度——链上符号（入口 + 被调者）关键数据流（字段
	// 读写，value-trace 串联入口；失败跳过——链可能含外部库符号）
	var flowIDs []string
	flowIDs = append(flowIDs, string(entry.ID))
	for _, f := range facts {
		flowIDs = append(flowIDs, string(f.TargetID))
	}
	if flows := a.WikiKeyFlows("", flowIDs); len(flows) > 0 {
		chain.KeyFlows = flows
		// R100 待办14-①：value-trace 串联——入口写入字段与下游被调者
		// 读取的交集自动标注（入口级：主链路方向；反向不标注）
		entryWrites := map[string]bool{}
		for _, fl := range flows {
			if fl.Symbol == chain.Entry {
				for _, w := range fl.Writes {
					entryWrites[w] = true
				}
			}
		}
		for _, fl := range flows {
			if fl.Symbol == chain.Entry {
				continue
			}
			for _, rd := range fl.Reads {
				if entryWrites[rd] {
					chain.ValueLinks = append(chain.ValueLinks, ValueLink{
						Field: rd, ProducedBy: chain.Entry, ReadBy: fl.Symbol})
				}
			}
		}
	}
	pkgs := map[string]bool{}
	pkgs[pkgPathOf(string(entry.ID))] = true
	for _, f := range facts {
		pkgs[pkgPathOf(string(f.SourceID))] = true
		pkgs[pkgPathOf(string(f.TargetID))] = true
	}
	for p := range pkgs {
		if p != "" {
			chain.Pkgs = append(chain.Pkgs, p)
		}
	}
	sort.Strings(chain.Pkgs)
	if len(chain.Steps) == 0 {
		chain.Miss = "该函数未调用项目内其他函数（可能仅调用外部库）"
	}
	return chain
}

// WikiKeyFlows 批量计算核心符号的字段读写数据流（R17）：每符号
// FunctionFields 分组——direct_read 归读、direct_write/indirect_write
// 归写。过滤：只保留本模块类型限定字段（排除第三方 x/tools/ssa 等
// 与 map 访问噪音）；[key] 变体归一后去重。无字段访问的符号跳过。
// R78：单符号解析失败跳过（不整批丢弃——流程页链上可能含外部库符号）。
func (a *Actions) WikiKeyFlows(modulePrefix string, symbolNames []string) []WikiKeyFlow {
	logger := zap.L()
	logger.Info("enter (Actions).WikiKeyFlows", zap.Int("symbols", len(symbolNames)))
	defer logger.Info("exit (Actions).WikiKeyFlows")
	var out []WikiKeyFlow
	for _, name := range symbolNames {
		n, rows, err := a.FunctionFields(name)
		if err != nil || len(rows) == 0 {
			continue
		}
		f := WikiKeyFlow{Symbol: n.Name}
		add := func(dst *[]string, path string) {
			// 只保留本模块类型限定字段（前缀 module；无包前缀的
			// map 访问 n["x"]/slots[key] 是噪音）
			if !strings.HasPrefix(path, modulePrefix) {
				return
			}
			// [key] 变体归一（fields[key] → fields）
			path = strings.ReplaceAll(path, "[key]", "")
			if !containsStr(*dst, path) {
				*dst = append(*dst, path)
			}
		}
		for _, r := range rows {
			switch r.AccessKind {
			case domain.SummaryDirectRead:
				add(&f.Reads, r.FieldPath)
			case domain.SummaryDirectWrite, domain.SummaryIndirectWrite:
				add(&f.Writes, r.FieldPath)
			}
		}
		if len(f.Reads) > 0 || len(f.Writes) > 0 {
			out = append(out, f)
		}
	}
	return out
}

// SortChainByCallLine 调用链按源码调用行号排序（R13）：深度 1（入口
// 直接调用）按入口内行号；深度 2（被调者内部）按被调者在入口中的
// 调用位置 + 内部行号——还原源码书写顺序（"顺序与代码一一对应"）。
// 无行号边 fallback 排最后。
func SortChainByCallLine(entryID string, facts []*domain.Fact) []domain.WikiSeqStep {
	pos := map[string]int{}
	for _, f := range facts {
		if string(f.SourceID) == entryID {
			if ln := factLineNum(f); ln > 0 {
				pos[string(f.TargetID)] = ln
			}
		}
	}
	const noLine = 1 << 30
	key := func(f *domain.Fact) (int, int) {
		if string(f.SourceID) == entryID {
			if ln := factLineNum(f); ln > 0 {
				return ln, 0
			}
			return noLine, 0
		}
		p, ok := pos[string(f.SourceID)]
		if !ok {
			return noLine, noLine
		}
		if ln := factLineNum(f); ln > 0 {
			return p, ln
		}
		return p, noLine
	}
	sorted := append([]*domain.Fact(nil), facts...)
	sort.SliceStable(sorted, func(i, j int) bool {
		ki1, ki2 := key(sorted[i])
		kj1, kj2 := key(sorted[j])
		if ki1 != kj1 {
			return ki1 < kj1
		}
		return ki2 < kj2
	})
	var out []domain.WikiSeqStep
	seen := map[[2]string]bool{}
	for _, f := range sorted {
		caller, callee := seqShort(string(f.SourceID)), seqShort(string(f.TargetID))
		if seen[[2]string{caller, callee}] {
			continue
		}
		seen[[2]string{caller, callee}] = true
		out = append(out, domain.WikiSeqStep{Caller: caller, Callee: callee})
	}
	return out
}

package action

// #238 wiki 数据聚合：六区块（职责/入口/核心符号/模块间调用/相关表）。

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// WikiData 聚合全仓库 wiki 数据（按 go.mod module 组织；mods 由调用方
// 从 buildRepo 传入——repo 层不存模块信息）。
func (a *Actions) WikiData(mods []string) ([]*domain.WikiModule, error) {
	logger := zap.L()
	logger.Info("enter (Actions).WikiData", zap.Int("mods", len(mods)))
	defer logger.Info("exit (Actions).WikiData")
	if len(mods) == 0 {
		return nil, nil
	}
	roots, err := a.repo.TopLevelEntries()
	if err != nil {
		return nil, err
	}
	calls, err := a.ModuleCalls("")
	if err != nil {
		return nil, err
	}
	allCalls, err := a.repo.GetAllCalls()
	if err != nil {
		return nil, err
	}
	out := make([]*domain.WikiModule, 0, len(mods))
	for _, mod := range mods {
		wm := &domain.WikiModule{Name: mod, ShortName: shortModName(mod)}
		wm.Desc = a.moduleDesc(mod)
		wm.Entries = moduleEntries(roots, mod)
		syms, err := a.repo.TopCallersInModule("symbol:go:"+mod, 5)
		if err != nil {
			return nil, err
		}
		wm.CoreSymbols = syms
		// 自动时序：入口 main 优先（系统流程），无入口用核心符号第一名；
		// 一律用 canonical ID（名称多匹配——本仓库 12 个 main 实测）
		anchorID := moduleEntryID(roots, mod)
		if anchorID == "" && len(syms) > 0 {
			anchorID = domain.CanonicalID(syms[0].ID)
		}
		if anchorID != "" {
			seq, err := a.wikiSequenceByID(anchorID)
			if err != nil {
				return nil, err
			}
			wm.Flows = seq
		}
		for _, c := range calls {
			if c.FromModule == mod && !containsStr(wm.OutCalls, c.ToModule) {
				wm.OutCalls = append(wm.OutCalls, c.ToModule)
			}
			if c.ToModule == mod && !containsStr(wm.InCalls, c.FromModule) {
				wm.InCalls = append(wm.InCalls, c.FromModule)
			}
		}
		tables, err := a.repo.TablesWrittenByModule("symbol:go:" + mod)
		if err != nil {
			return nil, err
		}
		wm.Tables = tableNames(tables)
		wm.PkgCalls = a.pkgCallsForModule(mod, allCalls)
		out = append(out, wm)
	}
	return out, nil
}

// pkgCallsForModule 模块内包间调用聚合（Q251-A）：calls 边两端包都
// 属于该模块且不同包 → (fromPkg, toPkg) 计数（次数降序 + 键序确定性；
// 同包调用不画——图语义 = 包间）。
func (a *Actions) pkgCallsForModule(mod string, calls []*domain.Fact) []*domain.WikiPkgCall {
	prefix := "symbol:go:" + mod
	counts := map[string]int{}
	for _, c := range calls {
		if c.Kind != domain.FactCalls {
			continue
		}
		src, dst := string(c.SourceID), string(c.TargetID)
		if !strings.HasPrefix(src, prefix) || !strings.HasPrefix(dst, prefix) {
			continue
		}
		from, to := pkgOfID(c.SourceID), pkgOfID(c.TargetID)
		if from == "" || to == "" || from == to {
			continue
		}
		counts[from+"|"+to]++
	}
	if len(counts) == 0 {
		return nil
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if counts[keys[i]] != counts[keys[j]] {
			return counts[keys[i]] > counts[keys[j]]
		}
		return keys[i] < keys[j]
	})
	out := make([]*domain.WikiPkgCall, 0, len(keys))
	for _, k := range keys {
		p := strings.SplitN(k, "|", 2)
		out = append(out, &domain.WikiPkgCall{From: shortModName(p[0]), To: shortModName(p[1]), Count: counts[k]})
	}
	return out
}

// shortModName module 路径末段。
func shortModName(mod string) string {
	if i := strings.LastIndex(mod, "/"); i >= 0 {
		return mod[i+1:]
	}
	return mod
}

// moduleDesc 模块根包的 doc_comment（symbol:go:<mod>:<短名> 包节点）。
func (a *Actions) moduleDesc(mod string) string {
	n, err := a.repo.GetSymbol(domain.CanonicalID("symbol:go:" + mod + ":" + shortModName(mod)))
	if err == nil {
		if d := n.DocComment(); d != "" {
			return d
		}
	}
	// scip 不写包 doc：读模块根目录 package 注释（doc.go 或首个 .go）
	return readPackageDoc(filepath.Join(a.repo.RepoPath(), modDir(mod)))
}

// modDir module 名 → 模块根目录（最后一段）。
func modDir(mod string) string {
	if i := strings.LastIndex(mod, "/"); i >= 0 {
		return mod[i+1:]
	}
	return mod
}

// readPackageDoc 读目录下 doc.go（若有）或首个 .go 的 package 注释。

// extractPackageDoc 提取 "package X" 前的注释块（Go 惯例）。
func extractPackageDoc(src string) string {
	idx := strings.Index(src, "package ")
	if idx <= 0 {
		return ""
	}
	head := strings.TrimSpace(src[:idx])
	if !strings.HasPrefix(head, "//") && !strings.HasPrefix(head, "/*") {
		return ""
	}
	return strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(head, "\\r", ""), "\\n", " "))
}

// moduleEntries 模块内入口（roots 节点 ID 前缀匹配）。
// moduleEntries 模块入口：只取 main（去重）——启动入口语义清晰；
// serves_http/grpc 标记过宽（辅助函数也被标），不适合 wiki 入口展示。

// tableNames 列名（表.列）→ 表名去重排序。

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// moduleEntryID 模块入口（main）的 canonical ID（roots 中第一个 main）。

// wikiSequenceByID 锚点调用链分组（#242 每个流程单独画）：入口的每条
// 一级调用 = 一条流程（一个命令/服务），各自一张图；组内 = 锚点 →
// 一级被调者 + 其后续链（深度 2）。
func (a *Actions) wikiSequenceByID(id domain.CanonicalID) ([]domain.WikiFlow, error) {
	depth1, err := a.Callees(id, 1)
	if err != nil {
		return nil, err
	}
	var out []domain.WikiFlow
	for _, f := range depth1 {
		calleeID := f.TargetID
		title := seqShort(string(calleeID))
		steps := []domain.WikiSeqStep{{Caller: seqShort(string(id)), Callee: title}}
		// 后续链（一级被调者出发深度 2）
		sub, err := a.Callees(calleeID, 2)
		if err != nil {
			return nil, err
		}
		// R13：按源码调用行号排序（入口边 = 自身行号；子链 = 入口
		// 调用行号 + 内部行号）——替换 R4 的字典序近似（行号更精确，
		// 顺序与实际代码一一对应）
		subSteps := sortWikiSubByCallLine(string(id), title, sub)
		steps = append(steps, subSteps...)
		out = append(out, domain.WikiFlow{Title: title, Steps: steps})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Title < out[j].Title })
	return out, nil
}

// sortWikiSubByCallLine 子链按源码调用行号排序（R13，与 cli 包
// sortChainByCallLine 同逻辑）：入口边（source=入口）按入口内行号；
// 子链边按 source 在入口中的调用行号 + 内部行号——链连续、顺序与
// 代码一一对应。
func sortWikiSubByCallLine(entryID, title string, facts []*domain.Fact) []domain.WikiSeqStep {
	pos := map[string]int{} // 被调者 → 入口内调用行号
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
	seen := map[[2]string]bool{{seqShort(entryID), title}: true}
	for _, f := range sorted {
		pair := [2]string{seqShort(string(f.SourceID)), seqShort(string(f.TargetID))}
		if seen[pair] {
			continue
		}
		seen[pair] = true
		out = append(out, domain.WikiSeqStep{Caller: pair[0], Callee: pair[1]})
	}
	return out
}

// factLineNum 调用边行号（metadata.line_num）。
func factLineNum(f *domain.Fact) int {
	if v, ok := f.Metadata["line_num"]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		case int64:
			return int(n)
		}
	}
	return -1
}

// seqShort canonical ID → 短名（保留方法形态 (T).m）。

// TableSchemas 表 schema 事实源（R19）：sqlite_master CREATE TABLE
// 全量——列类型/默认值权威（不借助 AI 填类型）。
func (a *Actions) TableSchemas() (map[string]string, error) {
	logger := zap.L()
	logger.Info("enter (Actions).TableSchemas")
	defer logger.Info("exit (Actions).TableSchemas")
	return a.repo.GetTableSchemas()
}

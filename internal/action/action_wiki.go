package action

// #238 wiki 数据聚合：六区块（职责/入口/核心符号/模块间调用/相关表）。

import (
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
	roots, err := a.Roots()
	if err != nil {
		return nil, err
	}
	calls, err := a.ModuleCalls("")
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
		out = append(out, wm)
	}
	return out, nil
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
	if err != nil {
		return ""
	}
	return n.DocComment()
}

// moduleEntries 模块内入口（roots 节点 ID 前缀匹配）。
func moduleEntries(roots []*domain.CodeEntity, mod string) []string {
	p1 := "symbol:go:" + mod + ":"
	p2 := "symbol:go:" + mod + "/"
	var out []string
	for _, r := range roots {
		if strings.HasPrefix(string(r.ID), p1) || strings.HasPrefix(string(r.ID), p2) {
			out = append(out, r.Name)
		}
	}
	return out
}

// tableNames 列名（表.列）→ 表名去重排序。
func tableNames(cols []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range cols {
		table := c
		if i := strings.Index(table, "."); i >= 0 {
			table = table[:i]
		}
		if !seen[table] {
			seen[table] = true
			out = append(out, table)
		}
	}
	sort.Strings(out)
	return out
}

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

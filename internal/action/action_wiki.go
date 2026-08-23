package action

// #238 wiki 数据聚合：六区块（职责/入口/核心符号/模块间调用/相关表）。

import (
	"os"
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
func readPackageDoc(dir string) string {
	for _, name := range []string{"doc.go"} {
		if b, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
			if d := extractPackageDoc(string(b)); d != "" {
				return d
			}
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		if b, err := os.ReadFile(filepath.Join(dir, e.Name())); err == nil {
			if d := extractPackageDoc(string(b)); d != "" {
				return d
			}
		}
	}
	return ""
}

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
func moduleEntries(roots []*domain.CodeEntity, mod string) []string {
	p1 := "symbol:go:" + mod + ":"
	p2 := "symbol:go:" + mod + "/"
	seen := map[string]bool{}
	var out []string
	for _, r := range roots {
		if r.Name != "main" || seen[r.Name] {
			continue
		}
		if strings.HasPrefix(string(r.ID), p1) || strings.HasPrefix(string(r.ID), p2) {
			seen[r.Name] = true
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

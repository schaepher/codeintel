package action

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

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

// moduleEntryID 模块入口（main）的 canonical ID（roots 中第一个 main）。
func moduleEntryID(roots []*domain.CodeEntity, mod string) domain.CanonicalID {
	p1 := "symbol:go:" + mod + ":"
	p2 := "symbol:go:" + mod + "/"
	for _, r := range roots {
		if r.Name != "main" {
			continue
		}
		if strings.HasPrefix(string(r.ID), p1) || strings.HasPrefix(string(r.ID), p2) {
			return r.ID
		}
	}
	return ""
}

// seqShort canonical ID → 短名（保留方法形态 (T).m）。
func seqShort(id string) string {
	if i := strings.LastIndex(id, ":"); i >= 0 {
		return id[i+1:]
	}
	return id
}

// SymbolPkg canonical ID → 包路径（symbol:go:<pkg>:<name>）。批次 C：
// 自 cli/wiki_processes.go 迁入（domains 事实包与 wiki 渲染共用）。
func SymbolPkg(id string) string {
	rest := strings.TrimPrefix(id, "symbol:go:")
	if i := strings.LastIndex(rest, ":"); i >= 0 {
		return rest[:i]
	}
	return rest
}

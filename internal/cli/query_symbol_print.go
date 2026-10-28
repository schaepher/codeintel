package cli

// query symbol 的输出辅助（事实行/节点/短 ID；从 query_symbol.go 拆出）。

import (
	"fmt"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// factRow 调用者/被调用者 JSON 行：端点节点信息（名称/类型/文件:行/签名）
// + 完整 source/target 边——VS Code 扩展一次查询即可在内存中组装调用树
// （不必逐层再查 CLI）。snake_case（Q243 契约）。
type factRow struct {
	ID         string  `json:"id"`
	Name       string  `json:"name,omitempty"`
	Kind       string  `json:"kind,omitempty"`
	File       string  `json:"file,omitempty"`
	Line       int     `json:"line,omitempty"`
	Signature  string  `json:"signature,omitempty"`
	Source     string  `json:"source"`
	Target     string  `json:"target"`
	EdgeKind   string  `json:"edge_kind"`
	Tool       string  `json:"tool,omitempty"`
	Confidence float64 `json:"confidence"`
	LineNum    int     `json:"line_num,omitempty"`
}

// factIDs 提取边的端点 JSON 行（endpoint=source/target）：附带端点节点
// 名称/类型/位置与完整 source/target 边（扩展据此组装调用链）。
func factIDs(acts *action.Actions, facts []*domain.Fact, endpoint string) []factRow {
	ids := make([]domain.CanonicalID, 0, len(facts))
	seen := map[domain.CanonicalID]bool{}
	for _, f := range facts {
		id := f.SourceID
		if endpoint == "target" {
			id = f.TargetID
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	byID := map[domain.CanonicalID]*domain.CodeEntity{}
	if acts != nil {
		if m, err := acts.SymbolsByIDs(ids); err == nil {
			byID = m
		}
	}
	out := make([]factRow, 0, len(facts))
	for _, f := range facts {
		id := f.SourceID
		if endpoint == "target" {
			id = f.TargetID
		}
		r := factRow{
			ID: string(id), Source: string(f.SourceID), Target: string(f.TargetID),
			EdgeKind: string(f.Kind), Tool: string(f.ToolSource), Confidence: f.Confidence,
			LineNum: metaLine(f.Metadata),
		}
		if n := byID[id]; n != nil {
			r.Name, r.Kind, r.File, r.Line = n.Name, string(n.Kind), n.FilePath, n.LineStart
			r.Signature = n.Signature()
		}
		out = append(out, r)
	}
	return out
}

// metaLine 取边元数据的调用点行号（int/float64——SQLite 回读类型差异）。
func metaLine(md map[string]any) int {
	switch v := md["line_num"].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return 0
}

// nodeBriefs 提取节点摘要（JSON 输出用）。
func nodeBriefs(nodes []*domain.CodeEntity) []map[string]any {
	out := make([]map[string]any, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, map[string]any{
			"id":   string(n.ID),
			"name": n.Name,
			"kind": string(n.Kind),
			"file": n.FilePath,
			"line": n.LineStart,
		})
	}
	return out
}

// printFacts 打印边列表；endpoint 为 "source" 时显示边左端（调用者场景），
// 否则显示右端（被调用者场景）。
func printFacts(facts []*domain.Fact, endpoint string, limit int, marks map[string]string) {
	logger := zap.L()
	logger.Debug("enter printFacts")
	defer logger.Debug("exit printFacts")
	truncated := len(facts) > limit
	if truncated {
		facts = facts[:limit]
	}
	for _, f := range facts {
		id := f.SourceID
		if endpoint == "target" {
			id = f.TargetID
		}
		flag := ""
		if m, ok := marks[string(id)]; ok {
			flag = " [" + m + "]"
		}
		fmt.Printf("  %s%s  (%s, conf=%.2f)\n", shortID(id), flag, f.ToolSource, f.Confidence)
	}
	if truncated {
		fmt.Printf("  ...（已截断，共 %d 条）\n", len(facts)+1)
	}
}

// shortID 压缩 canonical ID 显示：保留 pkg 末段与符号名。
func shortID(id domain.CanonicalID) string {
	logger := zap.L()
	logger.Debug("enter shortID")
	defer logger.Debug("exit shortID")
	s := string(id)
	prefix := "symbol:go:"
	if !strings.HasPrefix(s, prefix) {
		return s
	}
	rest := strings.TrimPrefix(s, prefix)
	if i := strings.LastIndex(rest, ":"); i >= 0 {
		pkg := rest[:i]
		name := rest[i+1:]
		if j := strings.LastIndex(pkg, "/"); j >= 0 {
			pkg = pkg[j+1:]
		}
		return pkg + ":" + name
	}
	return rest
}

// printNodes 打印节点列表。
func printNodes(nodes []*domain.CodeEntity) {
	logger := zap.L()
	logger.Debug("enter printNodes")
	defer logger.Debug("exit printNodes")
	sorted := make([]*domain.CodeEntity, len(nodes))
	copy(sorted, nodes)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Kind != sorted[j].Kind {
			return sorted[i].Kind < sorted[j].Kind
		}
		return sorted[i].Name < sorted[j].Name
	})
	for _, n := range sorted {
		loc := ""
		if n.FilePath != "" {
			loc = " " + n.FilePath
			if n.LineStart > 0 {
				loc += fmt.Sprintf(":%d", n.LineStart)
			}
		}
		fmt.Printf("  %s %s%s\n", n.Kind, n.Name, loc)
	}
}

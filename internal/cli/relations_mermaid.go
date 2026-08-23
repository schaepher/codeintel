package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// printRelationsAllMermaid 全库列级 mermaid 图（query relations --all --mermaid）：
// 表为子图（列节点），关联为列到列的边（query 类型粗线）。
func printRelationsAllMermaid(rels []*domain.TableRelation) int {
	var sb strings.Builder
	sb.WriteString("flowchart LR\n")

	byTable := map[string]map[string]bool{}
	for _, r := range rels {
		if byTable[r.FromTable] == nil {
			byTable[r.FromTable] = map[string]bool{}
		}
		byTable[r.FromTable][r.FromCol] = true
		if byTable[r.ToTable] == nil {
			byTable[r.ToTable] = map[string]bool{}
		}
		byTable[r.ToTable][r.ToCol] = true
	}
	var tableNames []string
	for t := range byTable {
		tableNames = append(tableNames, t)
	}
	sort.Strings(tableNames)
	for _, t := range tableNames {
		sb.WriteString(fmt.Sprintf("  subgraph %s[\"%s\"]\n", t, t))
		cols := make([]string, 0, len(byTable[t]))
		for c := range byTable[t] {
			cols = append(cols, c)
		}
		sort.Strings(cols)
		for _, c := range cols {
			sb.WriteString(fmt.Sprintf("    %s[\"%s.%s\"]\n", colID(t, c), t, c))
		}
		sb.WriteString("  end\n")
	}

	for _, r := range rels {
		style := " --> "
		if r.Type == domain.RelationQuery {
			style = " ==> "
		}
		sb.WriteString(fmt.Sprintf("  %s%s%s\n",
			colID(r.FromTable, r.FromCol), style, colID(r.ToTable, r.ToCol)))
	}
	fmt.Println(sb.String())
	return 0
}

// printRelationsMermaid 输出列级 mermaid 图：表为子图（列节点），
// 关联为列到列的边（query 类型粗线）。
func printRelationsMermaid(fromTable string, rels []*domain.TableRelation) int {
	var sb strings.Builder
	sb.WriteString("flowchart LR\n")

	fromCols := map[string]bool{}
	for _, r := range rels {
		fromCols[r.FromCol] = true
	}
	var fromColList []string
	for c := range fromCols {
		fromColList = append(fromColList, c)
	}
	sort.Strings(fromColList)
	for _, c := range fromColList {
		sb.WriteString(fmt.Sprintf("  %s[\"%s.%s\"]\n", colID(fromTable, c), fromTable, c))
	}

	byTable := map[string][]*domain.TableRelation{}
	for _, r := range rels {
		byTable[r.ToTable] = append(byTable[r.ToTable], r)
	}
	var ttList []string
	for tt := range byTable {
		ttList = append(ttList, tt)
	}
	sort.Strings(ttList)
	for _, tt := range ttList {
		list := byTable[tt]
		sb.WriteString(fmt.Sprintf("  subgraph %s[\"%s\"]\n", tt, tt))
		for _, r := range list {
			sb.WriteString(fmt.Sprintf("    %s[\"%s.%s\"]\n", colID(tt, r.ToCol), tt, r.ToCol))
		}
		sb.WriteString("  end\n")
	}

	seen := map[string]bool{}
	for _, r := range rels {
		key := r.FromCol + "|" + r.ToTable + "|" + r.ToCol
		if seen[key] {
			continue
		}
		seen[key] = true
		style := " --> "
		if r.Type == domain.RelationQuery {
			style = " ==> "
		}
		sb.WriteString(fmt.Sprintf("  %s%s%s[\"%s.%s\"]\n",
			colID(fromTable, r.FromCol), style, colID(r.ToTable, r.ToCol), r.ToTable, r.ToCol))
	}
	fmt.Println(sb.String())
	return 0
}

// colID mermaid 节点 ID（表名.列名 转义为合法标识符）。
func colID(table, col string) string {
	return strings.ReplaceAll(table+"_"+col, ".", "_")
}

package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
)

// queryRelations 实现 `codeintel query relations <表名> [--mermaid]`：
// 表间关联分析——本表列的值沿数据流链流入其他表列（A.x 读出 → B.y
// 过滤/写入，代码层推断，无外键依赖）。--mermaid 输出列级 mermaid 图；
// --type/--max-hops/--max-results 过滤输出；--memory full|sql 选择实现
// 路径（默认 auto 按规模）。
func queryRelations(acts *action.Actions, table, format string, opts outputOpts, f *queryFlags) int {
	acts.SetRelationHops(relationHopsFromFlags(f))
	rels, err := acts.Relations(table, f.memory)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	rels = relationsFilter(f)(rels)
	if format == "mermaid" {
		return printRelationsMermaid(table, rels)
	}
	if opts.json {
		if rels == nil {
			rels = []*domain.TableRelation{}
		}
		data, err := json.MarshalIndent(rels, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Println(string(data))
		return 0
	}
	if len(rels) == 0 {
		fmt.Printf("表 %s：无关联表（数据流链上未命中其他表的列）\n", table)
		return 0
	}

	sort.SliceStable(rels, func(i, j int) bool {
		if rels[i].Type != rels[j].Type {
			return rels[i].Type == domain.RelationQuery
		}
		return rels[i].Hops < rels[j].Hops
	})
	fmt.Printf("表 %s 关联（数据流链推断，%d 条）:\n", table, len(rels))
	var q, w int
	for _, r := range rels {
		tag := ""
		switch r.Type {
		case domain.RelationFK:
			tag = " [外键关联]"
			q++
		case domain.RelationQuery:
			tag = " [查询关联]"
			q++
		case domain.RelationWrite:
			tag = " [同源写入]"
			w++
		}
		fmt.Printf("  %s.%s → %s.%s  [%d 跳]%s\n", r.FromTable, r.FromCol, r.ToTable, r.ToCol, r.Hops, tag)
	}
	fmt.Printf("  （query=%d 键关联 / write=%d 同源 / read=%d 间接）\n", q, w, len(rels)-q-w)
	return 0
}

// queryRelationsAll 实现 `codeintel query relations --all`（Q160）：
// 一次遍历全部表返回所有表对关联（合并去重），AGENT 单次调用拿全库。
// --json 输出数组（与单表同构）；文本模式按表分组展示。
func queryRelationsAll(acts *action.Actions, format string, opts outputOpts, f *queryFlags) int {
	acts.SetRelationHops(relationHopsFromFlags(f))
	rels, err := acts.RelationsAll(f.memory)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	rels = relationsFilter(f)(rels)
	if format == "mermaid" {
		return printRelationsAllMermaid(rels)
	}
	if opts.json {
		if rels == nil {
			rels = []*domain.TableRelation{}
		}
		data, err := json.MarshalIndent(rels, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Println(string(data))
		return 0
	}
	if len(rels) == 0 {
		fmt.Println("全库无表间关联（数据流链上未命中其他表的列）")
		return 0
	}

	byFrom := map[string][]*domain.TableRelation{}
	for _, r := range rels {
		byFrom[r.FromTable] = append(byFrom[r.FromTable], r)
	}
	var fromTables []string
	for t := range byFrom {
		fromTables = append(fromTables, t)
	}
	sort.Strings(fromTables)
	fmt.Printf("全库表间关联（数据流链推断，共 %d 条 / %d 张表）:\n", len(rels), len(fromTables))
	var q, w int
	for _, ft := range fromTables {
		list := byFrom[ft]
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].Type != list[j].Type {
				return list[i].Type == domain.RelationQuery
			}
			return list[i].Hops < list[j].Hops
		})
		fmt.Printf("  [%s]\n", ft)
		for _, r := range list {
			tag := ""
			switch r.Type {
			case domain.RelationFK:
				tag = " [外键关联]"
				q++
			case domain.RelationQuery:
				tag = " [查询关联]"
				q++
			case domain.RelationWrite:
				tag = " [同源写入]"
				w++
			}
			fmt.Printf("    %s.%s → %s.%s  [%d 跳]%s\n", r.FromTable, r.FromCol, r.ToTable, r.ToCol, r.Hops, tag)
		}
	}
	fmt.Printf("  （query=%d 键关联 / write=%d 同源 / read=%d 间接）\n", q, w, len(rels)-q-w)
	return 0
}

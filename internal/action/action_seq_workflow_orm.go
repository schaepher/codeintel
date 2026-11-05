package action

// B03：ORM 模型/表/列 → 工作流写入/读取（从 action_seq_workflow_conn.go 拆出）。

import "strings"

// B03：ORM（xorm 风格）模型 → 表列映射（显式 Table + 准确列标签；无标签
// 用 snake_case 默认策略，动态命名/链对象重绑不接入）。
func seqORMColumnName(tag, field string) string {
	if tag != "" {
		if i := strings.Index(tag, ":"); i >= 0 {
			tag = tag[i+1:]
		}
		tag = strings.Trim(tag, `"' `)
		if i := strings.Index(tag, ","); i >= 0 {
			tag = tag[:i]
		}
		if tag != "" && tag != "-" {
			return tag
		}
	}
	return snakeCase(field)
}

// seqORMColumns 模型字段 → 列名（只保留有明确声明的字段）。
func seqORMColumns(m SeqORMModel) map[string]string {
	out := map[string]string{}
	for _, f := range m.Fields {
		if f.Embedded || f.Name == "" {
			continue
		}
		out[f.Name] = seqORMColumnName(f.Tag, f.Name)
	}
	return out
}

// seqORMWritesFromEvidence ORM 写入证据 → 工作流写入。
func seqORMWritesFromEvidence(node *CodeSeqNode, entry, repo string) []seqWFWrite {
	orm := node.Evidence.ORM
	if orm == nil || (orm.Operation != SeqStorageInsert && orm.Operation != SeqStorageUpdate) ||
		orm.Table == "" || len(orm.Arguments) == 0 {
		return nil
	}
	var out []seqWFWrite
	w := seqWFWrite{Entry: entry, Repo: repo, File: node.Evidence.CallFile,
		Line: node.Evidence.CallLine, Table: orm.Table, Op: orm.Operation, Guard: orm.Guard,
		Resource: seqORMSessionResource(node)}
	w.ID = "orm-write@" + node.Evidence.CallFile + ":" + itoaSeq(node.Evidence.CallLine)
	for _, m := range orm.Models {
		if m.Argument < 0 || m.Argument >= len(orm.Arguments) {
			continue
		}
		sl := orm.Arguments[m.Argument].Origin
		if sl == nil || sl.Kind != SeqOriginStructLiteral || sl.StructLiteral == nil {
			continue
		}
		cols := seqORMColumns(m)
		for _, f := range sl.StructLiteral.Fields {
			col, ok := cols[f.Name]
			if !ok || f.Origin == nil {
				continue
			}
			w.Values = append(w.Values, SeqStorageWrite{Column: col,
				Value: SeqStorageValue{Kind: SeqStorageValueArgument, Origin: f.Origin}})
		}
	}
	if len(w.Values) == 0 {
		return nil
	}
	out = append(out, w)
	return out
}

// seqORMReadsFromEvidence ORM 读取证据 → 工作流读取。
func seqORMReadsFromEvidence(node *CodeSeqNode, entry, repo string) []seqWFRead {
	orm := node.Evidence.ORM
	if orm == nil || orm.Operation != "select" || orm.Table == "" || len(orm.Predicates) == 0 {
		return nil
	}
	var cols []string
	for _, m := range orm.Models {
		for _, f := range m.Fields {
			if f.Embedded || f.Name == "" {
				continue
			}
			cols = append(cols, seqORMColumnName(f.Tag, f.Name))
		}
	}
	if len(cols) == 0 {
		return nil
	}
	return []seqWFRead{{Entry: entry, Repo: repo, File: node.Evidence.CallFile,
		Line: node.Evidence.CallLine, ReadID: "orm-read@" + node.Evidence.CallFile + ":" +
			itoaSeq(node.Evidence.CallLine), Table: orm.Table, Columns: cols,
		Preds: orm.Predicates, Resource: seqORMSessionResource(node), Guard: orm.Guard}}
}

// seqORMSessionResource ORM 调用的资源声明（框架证据不提供句柄符号 → 空；
// 两侧都空时连接带 resource_binding 缺失证明）。
func seqORMSessionResource(*CodeSeqNode) string { return "" }

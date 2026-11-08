package action

// B03e.4：ORM 字段 → 列映射的有效性（忽略字段/重复列歧义）。

// seqORMColumnBinding 一个有效字段 → 列映射。
type seqORMColumnBinding struct {
	Field  string
	Column string
}

// seqORMValidColumnBindings 模型的有效列映射：忽略字段（xorm:"-"）与
// 多字段映射同一列（歧义）都不参与来源证明。
func seqORMValidColumnBindings(m SeqORMModel) []seqORMColumnBinding {
	raw := make([]seqORMColumnBinding, 0, len(m.Fields))
	counts := map[string]int{}
	for _, f := range m.Fields {
		if f.Embedded || f.Name == "" {
			continue
		}
		col := seqORMColumnName(f.Tag, f.Name)
		if col == "" {
			continue
		}
		raw = append(raw, seqORMColumnBinding{Field: f.Name, Column: col})
		counts[col]++
	}
	out := make([]seqORMColumnBinding, 0, len(raw))
	for _, b := range raw {
		if counts[b.Column] == 1 {
			out = append(out, b)
		}
	}
	return out
}

// seqORMColumns 模型字段 → 列名（只保留唯一有效映射）。
func seqORMColumns(m SeqORMModel) map[string]string {
	out := map[string]string{}
	for _, b := range seqORMValidColumnBindings(m) {
		out[b.Field] = b.Column
	}
	return out
}

package action

// 读取证据的可核对文本（从 action_seq_storage_read.go 拆出）。

import "strings"

// SeqStorageReadDebugText 渲染用：按读取证据还原可核对的 SELECT 文本。
func SeqStorageReadDebugText(r *SeqStorageRead) string {
	if r == nil || r.Table == "" || len(r.Columns) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("SELECT ")
	b.WriteString(strings.Join(r.Columns, ","))
	b.WriteString(" FROM ")
	b.WriteString(r.Table)
	if len(r.Predicates) > 0 {
		b.WriteString(" WHERE ")
		for i, p := range r.Predicates {
			if i > 0 {
				b.WriteString(" AND ")
			}
			b.WriteString(p.Column)
			b.WriteString("=")
			switch p.Value.Kind {
			case SeqStorageValueLiteral:
				b.WriteString(p.Value.SQL)
			default:
				b.WriteString(p.Value.Expression)
			}
		}
	}
	return b.String()
}

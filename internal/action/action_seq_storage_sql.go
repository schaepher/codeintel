package action

// U04b：SQL 写入的 AST 解析（INSERT 单行 / UPDATE SET+WHERE AND 等式）。
// 形态未证明时返回边界代码，调用方丢弃任何部分结构（不保留看似确定的表列）。

import (
	"strings"

	"vitess.io/vitess/go/vt/sqlparser"
)

// seqSQLNamedBind SQL 文本里是否用了命名绑定（`:name`）——vitess 会把位置
// 占位符 `?` 也归一成内部名字（v1/v2），不能用 Argument.Name 判断。
func seqSQLNamedBind(sql string) bool {
	quote := byte(0)
	for i := 0; i < len(sql); i++ {
		c := sql[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"', '`':
			quote = c
		case ':':
			if i+1 < len(sql) {
				n := sql[i+1]
				if n == '_' || (n >= 'a' && n <= 'z') || (n >= 'A' && n <= 'Z') || (n >= '0' && n <= '9') {
					return true
				}
			}
		}
	}
	return false
}

// seqStorageParse 解析 SQL → 结构化写入。失败返回边界代码（调用方丢弃
// 任何部分结构，不保留看似确定的表列）。
func seqStorageParse(sql string, binder *seqSQLBinder) (*SeqStorage, string) {
	if seqSQLNamedBind(sql) {
		return nil, SeqStorageBoundaryShape
	}
	stmt, err := runtimeSQLParser.Parse(sql)
	if err != nil {
		return nil, SeqStorageBoundaryQuery
	}
	st := &SeqStorage{Outcome: "unproven"}
	switch s := stmt.(type) {
	case *sqlparser.Insert:
		return seqStorageInsert(s, binder, st)
	case *sqlparser.Update:
		return seqStorageUpdate(s, binder, st)
	}
	return nil, SeqStorageBoundaryShape
}

func seqStorageInsert(s *sqlparser.Insert, b *seqSQLBinder, st *SeqStorage) (*SeqStorage, string) {
	if s.Action != sqlparser.InsertAct || s.Ignore || len(s.OnDup) > 0 ||
		s.RowAlias != nil || len(s.Partitions) > 0 ||
		(s.Comments != nil && s.Comments.Length() > 0) {
		return nil, SeqStorageBoundaryShape
	}
	table, ok := seqStorageTable(s.Table)
	if !ok {
		return nil, SeqStorageBoundaryShape
	}
	rows, ok := s.Rows.(sqlparser.Values)
	if !ok || len(rows) != 1 {
		return nil, SeqStorageBoundaryShape
	}
	cols, ok := seqStorageColumns(s.Columns)
	if !ok || len(cols) != len(rows[0]) {
		return nil, SeqStorageBoundaryShape
	}
	writes := make([]SeqStorageWrite, 0, len(cols))
	for i, expr := range rows[0] {
		val, code := b.bind(expr)
		if code != "" {
			return nil, code
		}
		writes = append(writes, SeqStorageWrite{Column: cols[i], Value: val})
	}
	if code := b.done(); code != "" {
		return nil, code
	}
	st.Operation, st.Table, st.Writes = SeqStorageInsert, table, writes
	return st, ""
}

func seqStorageUpdate(s *sqlparser.Update, b *seqSQLBinder, st *SeqStorage) (*SeqStorage, string) {
	if s.With != nil || s.Ignore || s.Limit != nil || len(s.OrderBy) > 0 ||
		len(s.TableExprs) != 1 ||
		(s.Comments != nil && s.Comments.Length() > 0) {
		return nil, SeqStorageBoundaryShape
	}
	ate, ok := s.TableExprs[0].(*sqlparser.AliasedTableExpr)
	if !ok {
		return nil, SeqStorageBoundaryShape
	}
	table, ok := seqStorageTable(ate)
	if !ok {
		return nil, SeqStorageBoundaryShape
	}
	if len(s.Exprs) == 0 {
		return nil, SeqStorageBoundaryShape
	}
	writes := make([]SeqStorageWrite, 0, len(s.Exprs))
	seen := map[string]bool{}
	for _, ue := range s.Exprs {
		if ue == nil || ue.Name == nil || ue.Name.Qualifier.Name.String() != "" {
			return nil, SeqStorageBoundaryShape
		}
		col := ue.Name.Name.String()
		if col == "" || seen[strings.ToLower(col)] {
			return nil, SeqStorageBoundaryShape
		}
		seen[strings.ToLower(col)] = true
		val, code := b.bind(ue.Expr)
		if code != "" {
			return nil, code
		}
		writes = append(writes, SeqStorageWrite{Column: col, Value: val})
	}
	preds, code := seqStorageWhere(s.Where, b)
	if code != "" {
		return nil, code
	}
	if code := b.done(); code != "" {
		return nil, code
	}
	st.Operation, st.Table, st.Writes, st.Predicates = SeqStorageUpdate, table, writes, preds
	return st, ""
}

// seqStorageWhere WHERE 的 AND 等式子集（OR/IN/非等式/限定名未证明）。
func seqStorageWhere(where *sqlparser.Where, b *seqSQLBinder) ([]SeqStoragePredicate, string) {
	if where == nil {
		return nil, ""
	}
	var exprs []sqlparser.Expr
	seqFlattenAnd(where.Expr, &exprs)
	out := make([]SeqStoragePredicate, 0, len(exprs))
	for _, e := range exprs {
		cmp, ok := e.(*sqlparser.ComparisonExpr)
		if !ok || cmp.Operator != sqlparser.EqualOp {
			return nil, SeqStorageBoundaryShape
		}
		cn, ok := cmp.Left.(*sqlparser.ColName)
		if !ok || cn.Qualifier.Name.String() != "" || cn.Name.String() == "" {
			return nil, SeqStorageBoundaryShape
		}
		val, code := b.bind(cmp.Right)
		if code != "" {
			return nil, code
		}
		out = append(out, SeqStoragePredicate{Column: cn.Name.String(), Value: val})
	}
	return out, ""
}

func seqFlattenAnd(e sqlparser.Expr, out *[]sqlparser.Expr) {
	if a, ok := e.(*sqlparser.AndExpr); ok {
		seqFlattenAnd(a.Left, out)
		seqFlattenAnd(a.Right, out)
		return
	}
	*out = append(*out, e)
}

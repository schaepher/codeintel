package action

// U04e：SQL 读取结构证据（storage_read）——选择器与符号键来源。
//
// 与写入 storage 分开：storage_read 只描述**读取语句结构**与实参绑定，
// outcome 恒为 unproven（不证明唯一记录、读取成功或跨入口顺序）。

import (
	"go/ast"
	"strconv"
	"strings"

	"vitess.io/vitess/go/vt/sqlparser"
)

// 标准库读取方法的 SQL 实参位置。
var seqSQLReadMethods = map[string]int{
	"symbol:go:database/sql:(DB).Query":             0,
	"symbol:go:database/sql:(DB).QueryContext":      1,
	"symbol:go:database/sql:(DB).QueryRow":          0,
	"symbol:go:database/sql:(DB).QueryRowContext":   1,
	"symbol:go:database/sql:(Tx).Query":             0,
	"symbol:go:database/sql:(Tx).QueryContext":      1,
	"symbol:go:database/sql:(Tx).QueryRow":          0,
	"symbol:go:database/sql:(Tx).QueryRowContext":   1,
	"symbol:go:database/sql:(Conn).QueryContext":    1,
	"symbol:go:database/sql:(Conn).QueryRowContext": 1,
}

// SeqStorageRead 读取证据（SeqEvidence.StorageRead）。
type SeqStorageRead struct {
	Table               string                `json:"table,omitempty"`
	Columns             []string              `json:"columns,omitempty"`
	Predicates          []SeqStoragePredicate `json:"predicates,omitempty"`
	ResourceDeclaration string                `json:"resource_declaration,omitempty"`
	Guard               string                `json:"guard,omitempty"`
	Outcome             string                `json:"outcome"`
	Boundary            string                `json:"boundary,omitempty"`
	Resource            *SeqStorageResource   `json:"resource,omitempty"`
}

// seqStorageReadSQLArg 已证明的 database/sql 读取调用 → SQL 实参索引（-1 = 不是）。
func seqStorageReadSQLArg(node *CodeSeqNode) int {
	if node == nil || node.Evidence == nil {
		return -1
	}
	if arg, ok := seqSQLReadMethods[node.Evidence.SymbolID]; ok {
		return arg
	}
	return -1
}

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

// seqFoldSQLText 标量来源 → SQL 文本（只有全字面量可折叠；未知/参数 → 空）。
func seqFoldSQLText(o *SeqStorageOrigin) string {
	if o == nil {
		return ""
	}
	switch o.Kind {
	case SeqOriginLiteral:
		if o.ScalarType != "string" {
			return ""
		}
		v, err := strconv.Unquote(o.Literal)
		if err != nil {
			return ""
		}
		return v
	case SeqOriginConcat:
		var b strings.Builder
		for i := range o.Parts {
			part := seqFoldSQLText(&o.Parts[i])
			if part == "" {
				return ""
			}
			b.WriteString(part)
			if b.Len() > seqScalarOriginMaxBytes {
				return ""
			}
		}
		return b.String()
	}
	return ""
}

// seqStorageReadParse 解析 SELECT（明确列投影 + AND 等式）；失败返回边界码，
// 不保留部分表列。
func seqStorageReadParse(sql string, b *seqSQLBinder) (*SeqStorageRead, string) {
	if seqSQLNamedBind(sql) {
		return nil, SeqStorageBoundaryShape
	}
	stmt, err := runtimeSQLParser.Parse(sql)
	if err != nil {
		return nil, SeqStorageBoundaryQuery
	}
	sel, ok := stmt.(*sqlparser.Select)
	if !ok {
		return nil, SeqStorageBoundaryShape
	}
	if strings.Contains(sql, "/*+") {
		return nil, SeqStorageBoundaryShape
	}
	if sel.Distinct || sel.Into != nil || sel.GroupBy != nil || sel.Having != nil ||
		sel.OrderBy != nil || sel.Limit != nil || sel.Lock != 0 || sel.With != nil ||
		len(sel.Windows) > 0 || len(sel.From) != 1 ||
		(sel.Comments != nil && sel.Comments.Length() > 0) {
		return nil, SeqStorageBoundaryShape
	}
	ate, ok := sel.From[0].(*sqlparser.AliasedTableExpr)
	if !ok {
		return nil, SeqStorageBoundaryShape
	}
	table, ok := seqStorageTable(ate)
	if !ok {
		return nil, SeqStorageBoundaryShape
	}
	var cols []string
	seen := map[string]bool{}
	for _, e := range sel.SelectExprs.Exprs {
		ae, ok := e.(*sqlparser.AliasedExpr)
		if !ok || ae.As.String() != "" {
			return nil, SeqStorageBoundaryShape
		}
		cn, ok := ae.Expr.(*sqlparser.ColName)
		if !ok || cn.Name.String() == "" || cn.Qualifier.Name.String() != "" {
			return nil, SeqStorageBoundaryShape
		}
		name := cn.Name.String()
		if seen[strings.ToLower(name)] {
			return nil, SeqStorageBoundaryShape
		}
		seen[strings.ToLower(name)] = true
		cols = append(cols, name)
	}
	if len(cols) == 0 {
		return nil, SeqStorageBoundaryShape
	}
	preds, code := seqStorageWhere(sel.Where, b)
	if code != "" {
		return nil, code
	}
	if code := b.done(); code != "" {
		return nil, code
	}
	return &SeqStorageRead{Table: table, Columns: cols, Predicates: preds,
		Outcome: "unproven"}, ""
}

// seqStorageReadEvidence 已证明的读取调用 → 结构化读取证据。
func seqStorageReadEvidence(node *CodeSeqNode, call *ast.CallExpr, c *seqPersistCtx) *SeqStorageRead {
	sqlArg := seqStorageReadSQLArg(node)
	if sqlArg < 0 || call == nil {
		return nil
	}
	if sqlArg >= len(call.Args) {
		return &SeqStorageRead{Outcome: "unproven", Boundary: SeqStorageBoundaryQuery}
	}
	sql := seqPersistenceSQLText(call, sqlArg)
	if sql == "" && c != nil && sqlArg < len(c.argOrigins) {
		// U04e：已证明的字符串拼接（全部段落可折叠为字面量）沿标量来源折叠。
		sql = seqFoldSQLText(c.argOrigins[sqlArg])
	}
	if sql == "" {
		return &SeqStorageRead{Outcome: "unproven", Boundary: SeqStorageBoundaryQuery}
	}
	valueArgs := call.Args[sqlArg+1:]
	if call.Ellipsis.IsValid() && len(valueArgs) > 0 {
		return &SeqStorageRead{Outcome: "unproven", Boundary: SeqStorageBoundaryArgs}
	}
	binder := &seqSQLBinder{args: valueArgs, ctx: c}
	if c != nil && len(c.argOrigins) > sqlArg+1 {
		binder.origins = c.argOrigins[sqlArg+1:]
	}
	rd, code := seqStorageReadParse(sql, binder)
	if rd == nil {
		return &SeqStorageRead{Outcome: "unproven", Boundary: code}
	}
	if resource := seqStorageResourceDecl(call, c); resource != "" {
		rd.ResourceDeclaration = resource
		rd.Boundary = SeqStorageBoundaryRecord
	} else {
		rd.Boundary = SeqStorageBoundaryResource
	}
	if c != nil && c.guard != "" {
		rd.Guard = c.guard
	}
	return rd
}

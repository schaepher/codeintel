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
	// Result U04d：本次读取调用的结果身份（QueryRow→Row / Query→Rows，
	// 槽 0）。身份按调用求值区分，不证明读取成功或列值。
	Result *SeqSQLReadResult `json:"result,omitempty"`
}

// 读取结果类型（槽 0）。
const (
	SeqReadResultRow  = "Row"
	SeqReadResultRows = "Rows"
)

// SeqSQLReadResult 标准 SQL 读取调用的结果身份（U04d）。
type SeqSQLReadResult struct {
	// ID 本次请求内的读取身份（read-N；同一调用源码被求值两次也有两个）。
	ID     string `json:"id"`
	Repo   string `json:"repo,omitempty"`
	Entry  string `json:"entry,omitempty"`
	Symbol string `json:"symbol,omitempty"`
	File   string `json:"file,omitempty"`
	Offset int    `json:"offset,omitempty"`
	// Slot 结果槽（QueryRow/Query 的结果都是槽 0；错误槽保持未知）。
	Slot int `json:"slot"`
	// Type Row | Rows（槽 0 的静态类型短名）。
	Type string `json:"type"`
	// columns U04d/U04e：读取的 SELECT 列（内部快照；Scan 绑定按真实列序）。
	// 不进入 JSON 契约——列结构已由 storage_read.columns 输出。
	columns []string
	fields  []string // B03d：模型 Go 字段名（与 columns 对齐）
	// errSlot B03e：该读取方法的**错误**返回槽（Find=0 / Get=1）；
	// -1/0 缺省表示标准库 Query/QueryRow 的错误槽未知（不借用结果槽）。
	errSlot int
}

// errSlotOf 错误返回槽（未建模时返回 -1）。
func (r *SeqSQLReadResult) errSlotOf() int {
	if r == nil {
		return -1
	}
	return r.errSlot
}

// seqReadResultType 已证明读取方法的槽 0 类型（未知 → 空）。
func seqReadResultType(symbol string) string {
	switch symbol {
	case "symbol:go:database/sql:(DB).QueryRow", "symbol:go:database/sql:(DB).QueryRowContext",
		"symbol:go:database/sql:(Tx).QueryRow", "symbol:go:database/sql:(Tx).QueryRowContext",
		"symbol:go:database/sql:(Conn).QueryRowContext":
		return SeqReadResultRow
	case "symbol:go:database/sql:(DB).Query", "symbol:go:database/sql:(DB).QueryContext",
		"symbol:go:database/sql:(Tx).Query", "symbol:go:database/sql:(Tx).QueryContext",
		"symbol:go:database/sql:(Conn).QueryContext":
		return SeqReadResultRows
	}
	return ""
}

// seqAttachReadResult 给已证明的读取证据补结果身份（U04d）。
func seqAttachReadResult(req *CodeSequenceRequest, node *CodeSeqNode, call *ast.CallExpr,
	rd *SeqStorageRead) {
	if req == nil || req.acc == nil || node == nil || node.Evidence == nil || rd == nil ||
		len(rd.Columns) == 0 || call == nil {
		return
	}
	typ := seqReadResultType(node.Evidence.SymbolID)
	if typ == "" {
		return
	}
	req.acc.readSeq++
	r := &SeqSQLReadResult{ID: "read-" + itoaSeq(req.acc.readSeq), Repo: req.RepoAbs,
		Entry: req.entryID, Symbol: node.Evidence.SymbolID, File: node.Evidence.CallFile,
		Slot: 0, Type: typ}
	if req.acc.fset != nil && call.Lparen.IsValid() {
		r.Offset = req.acc.fset.Position(call.Lparen).Offset
	}
	rd.Result = r
}

// seqReadResultOriginAt 调用表达式是已证明读取时，返回结果身份来源。
func seqReadResultOriginAt(req CodeSequenceRequest, call *ast.CallExpr) *SeqStorageOrigin {
	node := seqCallNodeOf(req, call)
	if node == nil || node.Evidence == nil {
		return nil
	}
	if orm := node.Evidence.ORM; orm != nil && orm.ReadResult != nil {
		// B03e：ORM 的结果槽与错误槽分开建模——Find 的槽 0 是 error，
		// Get 的槽 0 是 bool；两者都不能当成"读取结果"。
		if orm.ReadResult.errSlotOf() >= 0 {
			return nil
		}
		return seqORMReadResultOriginAt(req, call)
	}
	if node.Evidence.StorageRead == nil {
		return nil
	}
	r := node.Evidence.StorageRead.Result
	if r == nil {
		return nil
	}
	cp := *r
	cp.columns = append([]string{}, node.Evidence.StorageRead.Columns...)
	return &SeqStorageOrigin{Kind: SeqOriginReadResult, ScalarType: "sql." + cp.Type, ReadResult: &cp}
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

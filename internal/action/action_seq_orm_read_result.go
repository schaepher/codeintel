package action

// B03d：ORM Find/Get 的读取结果身份与集合元素字段来源。

import (
	"go/ast"
	"go/token"
)

// seqBindORMReadResult 已证明 ORM select → 读取结果身份，并绑定到目标集合/行变量。
func seqBindORMReadResult(req CodeSequenceRequest, node *CodeSeqNode, call *ast.CallExpr,
	orm *SeqORM, c *seqPersistCtx) {
	if req.acc == nil || node == nil || node.Evidence == nil || orm == nil || call == nil ||
		orm.Operation != "select" || len(orm.Models) == 0 {
		return
	}
	m := orm.Models[0]
	if m.Argument < 0 || m.Argument >= len(call.Args) {
		return
	}
	method := ""
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
		method = sel.Sel.Name
	}
	typ := SeqReadResultRows
	if method == "Get" {
		typ = SeqReadResultRow
	}
	// 列顺序与模型字段声明一致（只取直接字段）。
	cols := make([]string, 0, len(m.Fields))
	fields := make([]string, 0, len(m.Fields))
	for _, f := range m.Fields {
		if f.Embedded || f.Name == "" {
			continue
		}
		cols = append(cols, seqORMColumnName(f.Tag, f.Name))
		fields = append(fields, f.Name)
	}
	if len(cols) == 0 {
		return
	}
	req.acc.readSeq++
	id := "orm-read-" + itoaSeq(req.acc.readSeq)
	rr := &SeqSQLReadResult{ID: id, Repo: req.RepoAbs, Entry: req.entryID,
		Symbol: node.Evidence.SymbolID, File: node.Evidence.CallFile, Slot: 0, Type: typ,
		columns: append([]string{}, cols...), fields: append([]string{}, fields...)}
	if c != nil && c.fset != nil && call.Lparen.IsValid() {
		rr.Offset = c.fset.Position(call.Lparen).Offset
	}
	orm.ReadResult = rr
	// 目标变量：Find(&records) / Get(record)。
	arg := runtimeUnparen(call.Args[m.Argument])
	if u, ok := arg.(*ast.UnaryExpr); ok && u.Op == token.AND {
		arg = runtimeUnparen(u.X)
	}
	idIdent, ok := arg.(*ast.Ident)
	if !ok || idIdent.Obj == nil {
		return
	}
	// B03d：本次调用的 `&records` 地址传递不保留指针——可按调用点覆盖
	// 同语句内的逃逸（更早的逃逸仍有效）。
	req.env.setScalarScanTarget(idIdent.Obj, &SeqStorageOrigin{Kind: SeqOriginReadResult,
		ScalarType: "sql." + typ, ReadResult: rr}, call.Pos())
}

// seqBindRangeReadElement range 元素 ← 集合读取结果（字段槽 = read_column）。
func seqBindRangeReadElement(req CodeSequenceRequest, s *ast.RangeStmt) {
	if req.env == nil || s == nil || s.Value == nil {
		return
	}
	origin := seqScalarOriginOf(req, s.X)
	if origin == nil || origin.Kind != SeqOriginReadResult || origin.ReadResult == nil ||
		origin.ReadResult.Type != SeqReadResultRows || len(origin.ReadResult.columns) == 0 {
		return
	}
	elem, ok := runtimeUnparen(s.Value).(*ast.Ident)
	if !ok || elem.Obj == nil {
		return
	}
	rr := cloneSeqScalarOrigin(origin).ReadResult
	scan := &SeqScan{ID: "orm-scan-" + rr.ID, Read: rr,
		SuccessConditions: []string{"query_success", SeqScanWhenSuccess},
		Outcome:           "unproven"}
	// 字段槽按 **Go 字段名** 绑定（条件 `r.Kind`），同时保留列名别名。
	names := append([]string{}, rr.fields...)
	for i, col := range rr.columns {
		origin := &SeqStorageOrigin{Kind: SeqOriginReadColumn, ScalarType: "string",
			ReadColumn: &SeqReadColumn{Result: scan, Column: col, Index: i, When: "query_success"}}
		field := col
		if i < len(names) {
			field = names[i]
		}
		seqFieldSlotSet(req, seqFieldSlotKey{obj: elem.Obj, field: field}, origin)
		if field != col {
			seqFieldSlotSet(req, seqFieldSlotKey{obj: elem.Obj, field: col}, origin)
		}
	}
}

// seqORMReadResultOriginAt 已证明 ORM select 调用的结果身份来源。
func seqORMReadResultOriginAt(req CodeSequenceRequest, call *ast.CallExpr) *SeqStorageOrigin {
	node := seqCallNodeOf(req, call)
	if node == nil || node.Evidence == nil || node.Evidence.ORM == nil {
		return nil
	}
	rr := node.Evidence.ORM.ReadResult
	if rr == nil {
		return nil
	}
	cp := *rr
	cp.columns = append([]string{}, rr.columns...)
	cp.fields = append([]string{}, rr.fields...)
	return &SeqStorageOrigin{Kind: SeqOriginReadResult, ScalarType: "sql." + cp.Type, ReadResult: &cp}
}

// seqORMFirstModelForRead 从读取结果身份反查 ORM 模型字段（列顺序对齐）。
func seqORMFirstModelForRead(req CodeSequenceRequest, e ast.Expr) SeqORMModel {
	var out SeqORMModel
	ast.Inspect(e, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if node := seqCallNodeOf(req, call); node != nil && node.Evidence != nil &&
			node.Evidence.ORM != nil && len(node.Evidence.ORM.Models) > 0 {
			out = node.Evidence.ORM.Models[0]
			return false
		}
		return true
	})
	return out
}

package action

// B03d：ORM Find/Get 的读取结果身份与集合元素字段来源。

import (
	"go/ast"
	"go/token"
)

// SeqReadError 读取错误槽来源（B03e）。
//
// ORM 读取方法的真实签名：Find(target) error → 槽 0 是 error；Get(target)
// (bool, error) → 槽 0 bool、槽 1 error。集合/行本身来自目标实参，不是返回值。
type SeqReadError struct {
	// Result 本次读取身份（同一次 ORM 读取）。
	Result *SeqSQLReadResult `json:"result,omitempty"`
	// Slot 实际返回槽（Find=0，Get=1）。
	Slot int `json:"slot"`
}

// seqORMReadErrorSlot 已证明 ORM 读取方法的错误返回槽（未知 → -1）。
func seqORMReadErrorSlot(method string) int {
	switch method {
	case "Find":
		return 0
	case "Get":
		return 1
	}
	return -1
}

// seqORMReadErrorOrigin 读取错误槽来源（Kind=read_error）。
func seqORMReadErrorOrigin(rr *SeqSQLReadResult) *SeqStorageOrigin {
	if rr == nil {
		return nil
	}
	cp := *rr
	cp.columns = append([]string{}, rr.columns...)
	cp.fields = append([]string{}, rr.fields...)
	return &SeqStorageOrigin{Kind: SeqOriginReadError, ScalarType: "error",
		ReadError: &SeqReadError{Result: &cp, Slot: rr.errSlot}}
}

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
	errSlot := seqORMReadErrorSlot(method)
	if errSlot < 0 {
		return
	}
	// 列顺序与模型字段声明一致；忽略字段/重复列歧义不进入读取结果。
	bindings := seqORMValidColumnBindings(m)
	cols := make([]string, 0, len(bindings))
	fields := make([]string, 0, len(bindings))
	for _, b := range bindings {
		cols = append(cols, b.Column)
		fields = append(fields, b.Field)
	}
	// B03e.4：即使全部字段被忽略（没有任何列来源），读取身份仍要建立，
	// 以便保留 Find/Get 的错误槽隔离；元素字段绑定由空列集合自然跳过。
	req.acc.readSeq++
	id := "orm-read-" + itoaSeq(req.acc.readSeq)
	rr := &SeqSQLReadResult{ID: id, Repo: req.RepoAbs, Entry: req.entryID,
		Symbol: node.Evidence.SymbolID, File: node.Evidence.CallFile, Slot: 0, Type: typ,
		errSlot: errSlot,
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

// seqORMReadSlotOriginAt 调用表达式第 slot 个返回槽的 ORM 读取来源。
//
// Find(槽 0=error) / Get(槽 1=error)；Get 的槽 0 是 bool（此处不给来源）。
// 其它（未知方法）返回 nil，不按"槽 0 一律是行/集合"推断。
func seqORMReadSlotOriginAt(req CodeSequenceRequest, call *ast.CallExpr, slot int) *SeqStorageOrigin {
	node := seqCallNodeOf(req, call)
	if node == nil || node.Evidence == nil || node.Evidence.ORM == nil {
		return nil
	}
	rr := node.Evidence.ORM.ReadResult
	if rr == nil {
		return nil
	}
	if rr.errSlotOf() != slot {
		return nil
	}
	return seqORMReadErrorOrigin(rr)
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

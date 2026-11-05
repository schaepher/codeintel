package action

// U04e：标准库 Row.Scan / Rows.Scan 的列来源、错误身份与成功前提。
//
// 边界（刻意保守）：
//   - 只认已证明的 `database/sql:(Row).Scan` / `(Rows).Scan` 调用，且接收者必须
//     来自 U04d 的读取结果身份（QueryRow→Row / Query→Rows）；
//   - 按 SELECT 的**真实列顺序**绑定目标，不按变量名猜列；
//   - 目标只支持同步 Scan 中直接取址的内建标量变量（指针/结构体字段/自定义
//     Scanner 保持不支持并给出原因）；
//   - 列来源 kind=read_column、when=scan_success、result.outcome=unproven：
//     不证明读取成功、记录存在或数据库返回值；
//   - 错误返回槽 kind=scan_error，引用同一 Scan 身份；此前发生的取址逃逸仍
//     持续有效（只有本次 Scan 的地址传递可专门处理）。

import (
	"go/ast"
	"go/token"
)

// Scan 相关常量。
const (
	SeqScanWhenSuccess = "scan_success"
	SeqScanWhenGuard   = "scan_success_guarded"

	seqScanSymbolRow  = "symbol:go:database/sql:(Row).Scan"
	seqScanSymbolRows = "symbol:go:database/sql:(Rows).Scan"
)

// SeqScan 一次 Scan 的身份与目标绑定（evidence.scan）。
type SeqScan struct {
	ID string `json:"id"`
	// Read 本次 Scan 对应的读取结果身份（U04d）。
	Read *SeqSQLReadResult `json:"read,omitempty"`
	// SuccessConditions 该 Scan 成立所需前提（Row: scan_success；
	// Rows: query_success/rows_next_true/scan_success）。
	SuccessConditions []string        `json:"success_conditions,omitempty"`
	Targets           []SeqScanTarget `json:"targets,omitempty"`
	// Unsupported 无法证明的来源（列数不符/类型不支持/目标形态不支持）。
	Unsupported string `json:"unsupported,omitempty"`
	Outcome     string `json:"outcome"`
}

// SeqScanTarget 单个 Scan 目标的列绑定。
type SeqScanTarget struct {
	Index  int    `json:"index"`
	Column string `json:"column,omitempty"`
	// Target 源码目标（变量名；字段路径在本项不支持）。
	Target string `json:"target,omitempty"`
	When   string `json:"when,omitempty"`
	// Unsupported 该目标不支持的原因（保留目标名，不补来源）。
	Unsupported string `json:"unsupported,omitempty"`
}

// SeqReadColumn 列来源（条件/赋值的标量来源）。
type SeqReadColumn struct {
	// Result 该列所属的 Scan（id=scan-N，read=read-N）。
	Result *SeqScan `json:"result"`
	Column string   `json:"column"`
	Index  int      `json:"index"`
	When   string   `json:"when"`
}

// seqScanSymbol 已证明的标准 Scan 方法身份（空 = 不是）。
func seqScanSymbol(symbol string) string {
	switch symbol {
	case seqScanSymbolRow, seqScanSymbolRows:
		return symbol
	}
	return ""
}

// seqAnnotateScans 语句节点生成后补齐 Scan 证据（接收者调用节点此时已登记）。
func seqAnnotateScans(req CodeSequenceRequest, nodes []*CodeSeqNode) {
	if req.acc == nil || len(nodes) == 0 {
		return
	}
	for _, n := range nodes {
		if n == nil || n.Evidence == nil || n.Evidence.Scan != nil || n.call == nil {
			continue
		}
		if seqScanSymbol(n.Evidence.SymbolID) == "" {
			// 变量接收者的 `row.Scan(...)` 可能没有索引调用边——方法身份由
			// 已证明的读取结果类型确定（在 seqAttachScan 内核对）。
			sel, ok := n.call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Scan" {
				continue
			}
		}
		seqAttachScan(req, n, n.call)
	}
}

// seqScanReceiverRead Scan 接收者对应的读取结果（chained 调用或已绑定变量）。
func seqScanReceiverRead(req CodeSequenceRequest, call *ast.CallExpr) *SeqSQLReadResult {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	switch x := runtimeUnparen(sel.X).(type) {
	case *ast.CallExpr:
		node := seqCallNodeOf(req, x)
		if node == nil || node.Evidence == nil || node.Evidence.StorageRead == nil {
			return nil
		}
		r := node.Evidence.StorageRead.Result
		if r == nil {
			return nil
		}
		cp := *r
		cp.columns = append([]string{}, node.Evidence.StorageRead.Columns...)
		return &cp
	case *ast.Ident:
		if x.Obj == nil || req.env == nil {
			return nil
		}
		origin, found := req.env.scalarOriginAt(x.Obj, x.Pos())
		if !found || origin == nil || origin.Kind != SeqOriginReadResult || origin.ReadResult == nil {
			return nil
		}
		cp := *origin.ReadResult
		cp.columns = append([]string{}, origin.ReadResult.columns...)
		return &cp
	}
	return nil
}

// seqAttachScan 生成 Scan 身份 + 目标列绑定（失败原因保留）。
func seqAttachScan(req CodeSequenceRequest, node *CodeSeqNode, call *ast.CallExpr) {
	if req.acc == nil || node == nil || node.Evidence == nil || call == nil {
		return
	}
	if node.Exec != "" {
		// 延迟/异步调度体不提供当前同步调用的列值（defer/go 的实参虽然立即
		// 求值，但 Scan 本身在之后执行）。
		return
	}
	symbol := seqScanSymbol(node.Evidence.SymbolID)
	if node.Evidence.SymbolID != "" && symbol == "" {
		// 明确解析到其它方法（自定义同名 Scan）——不借用标准库身份。
		return
	}
	read := seqScanReceiverRead(req, call)
	if read == nil {
		// 查询来源未知：不产生列来源（保留原有保守处理）。
		return
	}
	typ := read.Type
	if symbol == "" {
		// 方法身份由接收者的读取结果类型证明（*sql.Row / *sql.Rows）。
		switch typ {
		case SeqReadResultRow:
			symbol = seqScanSymbolRow
		case SeqReadResultRows:
			symbol = seqScanSymbolRows
		default:
			return
		}
		node.Evidence.SymbolID = symbol
	}
	if (symbol == seqScanSymbolRow && typ != SeqReadResultRow) ||
		(symbol == seqScanSymbolRows && typ != SeqReadResultRows) {
		return
	}
	req.acc.scanSeq++
	scan := &SeqScan{ID: "scan-" + itoaSeq(req.acc.scanSeq), Read: read, Outcome: "unproven"}
	if typ == SeqReadResultRows {
		scan.SuccessConditions = []string{"query_success", "rows_next_true", SeqScanWhenSuccess}
	} else {
		scan.SuccessConditions = []string{SeqScanWhenSuccess}
	}
	bind := len(call.Args) == len(read.columns) && len(read.columns) > 0
	if len(read.columns) == 0 {
		scan.Unsupported = SeqStorageBoundaryQuery
	} else if !bind {
		scan.Unsupported = SeqStorageBoundaryArgs
	}
	for i, arg := range call.Args {
		t := SeqScanTarget{Index: i}
		if i < len(read.columns) {
			t.Column = read.columns[i]
		}
		id := seqScanArgIdent(arg)
		scalarType := ""
		if id == nil || id.Name == "_" {
			t.Unsupported = "target_unsupported"
		} else {
			t.Target = id.Name
			if typ, ok := seqScanTargetType(id); ok {
				scalarType = typ
			} else {
				t.Unsupported = "target_type_unsupported"
			}
		}
		scan.Targets = append(scan.Targets, t)
		if id == nil || id.Obj == nil {
			continue
		}
		if t.Unsupported != "" || !bind {
			// 列数不符/目标不支持：不能沿用 Scan 前的旧值。
			req.env.setScalar(id.Obj, nil)
			continue
		}
		col, ok := seqReadColumnOrigin(scan, t, scalarType)
		if !ok {
			continue
		}
		// 标准 Scan 不保留指针：本次地址传递可以专门绑定；之前已发生的取址
		// 逃逸仍持续有效（escapedAt 早于本次调用 → 不绑定）。
		req.env.setScalarScanTarget(id.Obj, col, call.Pos())
	}
	node.Evidence.Scan = scan
}

// seqScanArgIdent `&x` → x（其它形态不支持）。
func seqScanArgIdent(arg ast.Expr) *ast.Ident {
	u, ok := runtimeUnparen(arg).(*ast.UnaryExpr)
	if !ok || u.Op != token.AND {
		return nil
	}
	id, ok := runtimeUnparen(u.X).(*ast.Ident)
	if !ok {
		return nil
	}
	return id
}

// seqScanTargetType 目标的声明类型（只接受内建标量；无显式类型/指针/复合
// 类型一律不支持）。
func seqScanTargetType(id *ast.Ident) (string, bool) {
	if id == nil || id.Obj == nil {
		return "", false
	}
	var typ ast.Expr
	switch d := id.Obj.Decl.(type) {
	case *ast.Field:
		typ = d.Type
	case *ast.ValueSpec:
		typ = d.Type
	}
	if typ == nil {
		return "", false
	}
	t := runtimeUnparen(typ)
	if _, ok := t.(*ast.StarExpr); ok {
		return "", false
	}
	name, ok := t.(*ast.Ident)
	if !ok || !seqBuiltinTypeName(name.Name) {
		return "", false
	}
	switch name.Name {
	case "error", "any", "complex64", "complex128":
		return "", false
	}
	return name.Name, true
}

// seqReadColumnOrigin 目标 → read_column 来源（同一 Scan 身份）。
func seqReadColumnOrigin(scan *SeqScan, t SeqScanTarget, scalarType string) (*SeqStorageOrigin, bool) {
	if scan == nil || t.Column == "" {
		return nil, false
	}
	col := &SeqReadColumn{Result: scan, Column: t.Column, Index: t.Index, When: SeqScanWhenSuccess}
	return &SeqStorageOrigin{Kind: SeqOriginReadColumn, ScalarType: scalarType, ReadColumn: col}, true
}

// seqBindReadResultOrigin 多槽赋值里槽 0 = 读取结果身份（`rows, err :=
// DB.Query(...)`；错误槽保持未知）。
func seqBindReadResultOrigin(req CodeSequenceRequest, call *ast.CallExpr, lhs []ast.Expr) {
	if req.env == nil || call == nil || len(lhs) == 0 {
		return
	}
	id, ok := lhs[0].(*ast.Ident)
	if !ok || id.Obj == nil || id.Name == "_" {
		return
	}
	if o := seqReadResultOriginAt(req, call); o != nil {
		req.env.setScalar(id.Obj, o)
	}
}

// seqScanErrorOriginAt 调用是已证明的 Scan → 错误返回槽来源（槽 0 = error）。
func seqScanErrorOriginAt(req CodeSequenceRequest, call *ast.CallExpr) *SeqStorageOrigin {
	node := seqCallNodeOf(req, call)
	if node == nil || node.Evidence == nil || node.Evidence.Scan == nil {
		return nil
	}
	cp := *node.Evidence.Scan
	return &SeqStorageOrigin{Kind: SeqOriginScanError, ScalarType: "error", ScanResult: &cp}
}

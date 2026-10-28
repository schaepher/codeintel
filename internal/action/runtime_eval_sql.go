package action

// todo25 §13：SQL 读取点的运行时求值（从 runtime_eval_call.go 拆出）。
import (
	"fmt"
	"go/ast"
	"strings"
)

func (e *rtEval) evalQueryCall(frame *rtFrame, call *ast.CallExpr, s *rtState, depth int) (rtValue, []*rtState, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || !isRuntimeQueryMethod(sel.Sel.Name) {
		return rtUnknownValue(), nil, false
	}
	e.an.fact(call) // 保证 map 初始化
	sym := ""
	if recv, ok := sel.X.(*ast.Ident); ok {
		// 包级 DB 变量：rebind（DB=nil 等）后不再使用旧绑定。
		if recv.Obj != nil {
			g := e.an.globalByNameOf(recv)
			if g.mutated {
				ev := &SeqRuntimeEvidence{SnapshotPath: e.an.snap.Path, SnapshotHash: e.an.snap.Hash,
					Version: e.an.snap.Version, Reason: "binding-mutated", Result: "unknown"}
				e.an.facts[call] = ev
				return rtUnknownValue(), []*rtState{s}, true
			}
		}
		// T14：只有**准确声明为 *sql.DB** 的包级/局部变量才绑定数据库身份
		// （局部同名、自定义类型、未证明类型不借用绑定）。
		if !runtimeIsSQLDBDecl(recv, frame.ctx.file) {
			ev := &SeqRuntimeEvidence{SnapshotPath: e.an.snap.Path, SnapshotHash: e.an.snap.Hash,
				Version: e.an.snap.Version, Reason: "binding-type-unproven", Result: "unknown"}
			e.an.facts[call] = ev
			return rtUnknownValue(), []*rtState{s}, true
		}
		sym = "symbol:go:" + frame.pkgPath + ":" + recv.Name
	} else {
		// 结构体字段/工厂返回值里的 db 句柄（`d.db.QueryContext`）。
		rv, variants := e.evalExpr(frame, sel.X, s, depth)
		if len(variants) > 0 {
			s = variants[0]
		}
		if rv.kind == rtDB {
			sym = rv.s
		}
	}
	ev := &SeqRuntimeEvidence{SnapshotPath: e.an.snap.Path, SnapshotHash: e.an.snap.Hash,
		Version: e.an.snap.Version, Reason: "runtime-query", Result: "unknown"}
	e.an.facts[call] = ev
	source := runtimeResolveSource(e.an.snap, sym)
	if source == "" {
		ev.Reason = "binding-missing"
		return rtUnknownValue(), []*rtState{s}, true
	}
	ev.SourceID = source
	if len(call.Args) < 2 {
		ev.Reason = "missing-sql"
		return rtUnknownValue(), []*rtState{s}, true
	}
	sqlVal, variants := e.evalExpr(frame, call.Args[1], s, depth)
	if len(variants) > 0 {
		s = variants[0]
	}
	if sqlVal.kind != rtString {
		ev.Reason = "dynamic-sql"
		return rtUnknownValue(), []*rtState{s}, true
	}
	args := make([]rtValue, 0, len(call.Args)-2)
	rawArgs := call.Args[2:]
	for i, arg := range rawArgs {
		v, variants2 := e.evalExpr(frame, arg, s, depth)
		if len(variants2) > 0 {
			s = variants2[0]
		}
		if call.Ellipsis.IsValid() && i == len(rawArgs)-1 && v.kind == rtSlice {
			args = append(args, v.elems...)
			continue
		}
		args = append(args, v)
		ev.Args = append(ev.Args, v.rtDisplay())
	}
	q, err := parseRuntimeSelect(sqlVal.s, args)
	if err != nil {
		ev.Reason = err.Error()
		return rtUnknownValue(), []*rtState{s}, true
	}
	q.SourceID = source
	q.Args = append([]rtValue(nil), args...)
	e.an.queries[call] = q
	ev.Table = q.Table
	ev.Columns = append([]string(nil), q.Columns...)
	ev.Query = runtimePredicateDisplays(q.Predicates)
	obs, err := runtimeObservationFor(e.an.snap, q)
	if err != nil {
		ev.Reason = err.Error()
		return rtUnknownValue(), []*rtState{s}, true
	}
	fillRuntimeObservationEvidence(ev, obs)
	rows, err := runtimeFilterRows(obs, q)
	if err != nil {
		ev.Reason = err.Error()
		return rtUnknownValue(), []*rtState{s}, true
	}
	ev.Rows = len(rows)
	q.RowCount = len(rows)
	if sel.Sel.Name == "QueryRow" || sel.Sel.Name == "QueryRowContext" {
		if len(rows) == 1 {
			ev.Result = "row"
			return rtValue{kind: rtRows, elems: rows}, []*rtState{s}, true
		}
		if len(rows) == 0 {
			ev.Result = "empty"
			return rtValue{kind: rtRows, elems: nil}, []*rtState{s}, true
		}
		// T14：标量查询多行——相同重复行保持 unknown（幂等行集）；值冲突
		// 直接失败（stdout 为空、非零退出），不挑第一行。
		if runtimeRowsIdentical(rows) {
			ev.Reason = "query-row-duplicate-rows"
			return rtUnknownValue(), []*rtState{s}, true
		}
		e.err = fmt.Errorf("runtime-data: scalar query returned %d conflicting rows（标量查询多行冲突，无法确定值）", len(rows))
		ev.Reason = "query-row-conflict"
		return rtUnknownValue(), []*rtState{s}, true
	}
	ev.Result = "rows"
	return rtValue{kind: rtRows, elems: rows}, []*rtState{s}, true
}

func isRuntimeQueryMethod(name string) bool {
	switch name {
	case "QueryRowContext", "QueryContext", "QueryRow", "Query":
		return true
	}
	return false
}

// assignScanTarget &x / x（扫描目标只支持局部变量指针）。
func runtimePredicateDisplays(preds []runtimePredicate) []string {
	out := make([]string, 0, len(preds))
	for _, p := range preds {
		out = append(out, p.Field+" "+p.Op+" "+p.Value.rtDisplay())
	}
	return out
}

func fillRuntimeObservationEvidence(ev *SeqRuntimeEvidence, obs *runtimeObservation) {
	ev.ObservedAt = obs.ObservedAt
	ev.Revision = obs.Revision
	ev.Scope = runtimePredicateDisplays(obs.Scope)
}

func copyRuntimeEvidence(dst, src *SeqRuntimeEvidence) {
	*dst = *src
	dst.ScanTarget = ""
}

func isRuntimeQueryCall(call *ast.CallExpr) bool {
	if call == nil {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	return ok && isRuntimeQueryMethod(sel.Sel.Name)
}

// evalQueryOutcomes QueryContext/Query 的 rows + error 两个状态（成功/失败）。
func (e *rtEval) evalQueryOutcomes(frame *rtFrame, call *ast.CallExpr, s *rtState, depth int) []rtOutcome {
	val, variants, _ := e.evalQueryCall(frame, call, s, depth)
	if len(variants) > 0 {
		s = variants[0]
	}
	q := e.an.queries[call]
	if val.kind != rtRows || q == nil {
		return []rtOutcome{{state: s, val: val}}
	}
	success := cloneRuntimeState(s)
	failure := cloneRuntimeState(s)
	return []rtOutcome{
		{state: success, val: rtValue{kind: rtTuple, elems: []rtValue{val, rtNullValue()}}},
		{state: failure, val: rtValue{kind: rtTuple, elems: []rtValue{rtUnknownValue(), rtErrorValue()}}},
	}
}

// evalRowsScanOutcomes rows.Scan(&f1,&f2)（循环内）：按 query.Columns 顺序写入
// 目标变量/结构体字段。
func (e *rtEval) evalRowsScanOutcomes(frame *rtFrame, scan *ast.CallExpr, rowsID *ast.Ident,
	s *rtState, depth int) []rtOutcome {
	if rowsID.Obj == nil {
		return nil
	}
	row, ok := e.an.cursor[rowsID.Obj]
	q := e.an.rowQueries[rowsID.Obj]
	if !ok || q == nil || row.kind != rtRow {
		return nil
	}
	ev := &SeqRuntimeEvidence{SnapshotPath: e.an.snap.Path, SnapshotHash: e.an.snap.Hash,
		Version: e.an.snap.Version, Reason: "runtime-rows-scan", Result: "row",
		SourceID: q.SourceID, Table: q.Table, Columns: append([]string(nil), q.Columns...),
		Query: runtimePredicateDisplays(q.Predicates), Rows: q.RowCount}
	for _, a := range q.Args {
		ev.Args = append(ev.Args, a.rtDisplay())
	}
	e.an.facts[scan] = ev
	var targets []string
	for i, arg := range scan.Args {
		if i >= len(q.Columns) {
			break
		}
		v, ok := row.cols[q.Columns[i]]
		if !ok || !runtimeScanTargetAccepts(arg, v) {
			v = rtUnknownValue()
		}
		e.assignScanTarget(arg, v, s)
		targets = append(targets, runtimeScanTargetName(arg))
	}
	ev.ScanTarget = strings.Join(targets, ",")
	ev.ReturnSlot = ev.ScanTarget
	return []rtOutcome{{state: s, val: rtNullValue()}}
}

// runtimeRowsIdentical 多行是否完全相同（同一行重复出现 = 幂等行集）。
func runtimeRowsIdentical(rows []rtValue) bool {
	if len(rows) < 2 {
		return true
	}
	first := rows[0]
	for _, r := range rows[1:] {
		if r.kind != first.kind || len(r.cols) != len(first.cols) {
			return false
		}
		for k, v := range first.cols {
			ov, ok := r.cols[k]
			if !ok {
				return false
			}
			eq, known := rtEqual(v, ov)
			if !known || !eq {
				return false
			}
		}
	}
	return true
}

// runtimeIsSQLDBDecl 变量声明类型是否为 *sql.DB（源码精确声明；未知类型不算）。
func runtimeIsSQLDBDecl(id *ast.Ident, file *ast.File) bool {
	if id == nil || id.Obj == nil || file == nil {
		return false
	}
	var typ ast.Expr
	switch d := id.Obj.Decl.(type) {
	case *ast.ValueSpec:
		typ = d.Type
	case *ast.Field:
		typ = d.Type
	case *ast.AssignStmt:
		// `db := ...`：类型未显式声明 → 不证明（不猜）。
		return false
	}
	return runtimeIsSQLDBTypeExpr(typ, file)
}

func runtimeIsSQLDBTypeExpr(t ast.Expr, file *ast.File) bool {
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}
	sel, ok := t.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "DB" {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return seqImportPath(file, id.Name) == "database/sql"
}

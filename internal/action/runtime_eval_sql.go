package action

// todo25 §13：SQL 读取点的运行时求值（从 runtime_eval_call.go 拆出）。
import (
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
		ev.Reason = "query-row-multiple-rows"
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

// evalScanOutcomes Scan → 成功/失败两个变体（错误边界保留；目标变量按
// 成功行值或未知赋值）。
func (e *rtEval) evalScanOutcomes(frame *rtFrame, scan *ast.CallExpr, inner *ast.CallExpr,
	s *rtState, depth int) []rtOutcome {
	ev := &SeqRuntimeEvidence{SnapshotPath: e.an.snap.Path, SnapshotHash: e.an.snap.Hash,
		Version: e.an.snap.Version, Reason: "runtime-scan", Result: "unknown"}
	e.an.facts[scan] = ev
	val, variants, _ := e.evalQueryCall(frame, inner, s, depth)
	if len(variants) > 0 {
		s = variants[0]
	}
	q := e.an.queries[inner]
	if qe := e.an.facts[inner]; qe != nil {
		copyRuntimeEvidence(ev, qe)
	}
	if q != nil {
		ev.Table = q.Table
		ev.Columns = append([]string(nil), q.Columns...)
	}
	var targets []string
	for _, arg := range scan.Args {
		targets = append(targets, runtimeScanTargetName(arg))
	}
	ev.ScanTarget = strings.Join(targets, ",")
	ev.ReturnSlot = ev.ScanTarget
	if val.kind != rtRows || len(scan.Args) == 0 || q == nil {
		if ev.Reason == "" {
			ev.Reason = "scan-unknown"
		}
		for _, arg := range scan.Args {
			e.assignScanTarget(arg, rtUnknownValue(), s)
		}
		return []rtOutcome{{state: s, val: rtUnknownValue()}}
	}
	if len(val.elems) == 0 {
		ev.Reason = "query-row-no-rows"
		ev.Result = "empty"
		for _, arg := range scan.Args {
			e.assignScanTarget(arg, rtUnknownValue(), s)
		}
		return []rtOutcome{{state: s, val: rtErrorValue()}}
	}
	if len(val.elems) != 1 {
		ev.Reason = "query-row-multiple-rows"
		for _, arg := range scan.Args {
			e.assignScanTarget(arg, rtUnknownValue(), s)
		}
		return []rtOutcome{{state: s, val: rtUnknownValue()}}
	}
	row := val.elems[0]
	colVal, ok := row.cols[q.Columns[0]]
	if !ok {
		ev.Reason = "scan-column-missing"
		for _, arg := range scan.Args {
			e.assignScanTarget(arg, rtUnknownValue(), s)
		}
		return []rtOutcome{{state: s, val: rtUnknownValue()}}
	}
	_ = colVal
	success := cloneRuntimeState(s)
	failure := cloneRuntimeState(s)
	for i, arg := range scan.Args {
		if i >= len(q.Columns) {
			break
		}
		v, ok := row.cols[q.Columns[i]]
		if !ok {
			v = rtUnknownValue()
		}
		if !runtimeScanTargetAccepts(arg, v) {
			ev.Reason = "scan-type-incompatible"
			v = rtUnknownValue()
		}
		e.assignScanTarget(arg, v, success)
		e.assignScanTarget(arg, rtUnknownValue(), failure)
	}
	ev.Result = "row"
	return []rtOutcome{{state: success, val: rtNullValue()}, {state: failure, val: rtErrorValue()}}
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

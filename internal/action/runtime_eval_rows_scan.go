package action

// 运行时行扫描（rows.Scan 循环内）的证据与目标赋值。

import (
	"go/ast"
	"strings"
)

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

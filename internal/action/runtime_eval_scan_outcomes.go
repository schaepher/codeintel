package action

// T14：Scan 成功/失败变体与类型不兼容边界（从 runtime_eval_sql.go 拆出）。

import (
	"go/ast"
	"strings"
)

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
	mismatch := false
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
			mismatch = true
			v = rtUnknownValue()
		}
		e.assignScanTarget(arg, v, success)
		e.assignScanTarget(arg, rtUnknownValue(), failure)
	}
	if mismatch {
		// T14：类型不兼容 ≠ 确定失败——值未知且错误边界未知（保留失败分支与
		// 全部业务分支，不凭"类型不对"裁剪流程）。
		unknown := cloneRuntimeState(s)
		for _, arg := range scan.Args {
			e.assignScanTarget(arg, rtUnknownValue(), unknown)
		}
		return []rtOutcome{{state: unknown, val: rtUnknownValue()}}
	}
	ev.Result = "row"
	return []rtOutcome{{state: success, val: rtNullValue()}, {state: failure, val: rtErrorValue()}}
}

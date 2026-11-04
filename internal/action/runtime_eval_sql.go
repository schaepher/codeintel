package action

// todo25 §13：SQL 读取点的运行时求值（从 runtime_eval_call.go 拆出）。
import (
	"fmt"
	"go/ast"
)

func (e *rtEval) evalQueryCall(frame *rtFrame, call *ast.CallExpr, s *rtState, depth int) (rtValue, []*rtState, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || !isRuntimeQueryMethod(sel.Sel.Name) {
		return rtUnknownValue(), nil, false
	}
	// 接收者身份（Go 求值顺序：接收者先于实参）。
	sym := ""
	if recv, ok := sel.X.(*ast.Ident); ok {
		if sym2, ok2 := e.sqlResourceSymbols(frame, recv); ok2 {
			sym = sym2
		} else if recv.Obj != nil || e.an.pkgVarNames[recv.Name] {
			// 不是已证明的 `*sql.DB` 包级句柄（局部/参数/自定义类型/已初始化）：
			// 交回通用调用求值——自研 QueryRow/Scan 的副作用不能被读取快路径
			// 绕过（U04d）。
			return rtUnknownValue(), nil, false
		}
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
	// U04d：SQL 与值实参里的 helper 副作用先求值，再检查绑定（ResetArg 等
	// 改写句柄后不得用旧绑定返回确定行）。
	vals, states := e.evalArgs(frame, call.Args, s, depth)
	if len(states) > 0 {
		s = states[0]
	}
	e.an.fact(call) // 保证 map 初始化
	ev := &SeqRuntimeEvidence{SnapshotPath: e.an.snap.Path, SnapshotHash: e.an.snap.Hash,
		Version: e.an.snap.Version, Reason: "runtime-query", Result: "unknown"}
	e.an.facts[call] = ev
	// T19：先解析查询（即使缺绑定也要给出可核对的表/列/谓词身份），
	// 绑定缺失作为明确边界而不是丢失查询身份。
	mutated := false
	if sym != "" {
		mutated = e.an.globalByNameOf(&ast.Ident{Name: nameOfSymbol(sym)}).mutated
	}
	// T19c：标准库 SQL 实参位置按**方法身份**区分——`QueryRow`/`Query` 的
	// SQL 实参是 0（无 Context 参数），`*Context` 版本是 1。只按方法名猜
	// 会让无 Context 的调用被当成 query_not_proven。
	sqlArg := runtimeQuerySQLArg(sel.Sel.Name)
	if len(call.Args) <= sqlArg {
		ev.Reason = "missing-sql"
		if sym == "" {
			ev.Reason = "binding-type-unproven"
		}
		return rtUnknownValue(), []*rtState{s}, true
	}
	var sqlVal rtValue
	if len(vals) > sqlArg {
		sqlVal = vals[sqlArg]
	}
	if sqlVal.kind != rtString {
		ev.Reason = "dynamic-sql"
		ev.Boundary = seqRuntimeBoundaryFor(SeqRuntimeCodeQuery)
		if sym == "" {
			ev.Reason = "binding-type-unproven"
		}
		return rtUnknownValue(), []*rtState{s}, true
	}
	valueArg := sqlArg + 1
	args := make([]rtValue, 0, len(call.Args)-valueArg)
	rawArgs := call.Args[valueArg:]
	for i := range rawArgs {
		var v rtValue
		if len(vals) > valueArg+i {
			v = vals[valueArg+i]
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
		// T19：SQL 形态未证明 → analysis_boundary/query_not_proven（不索要配置）。
		ev.Boundary = seqRuntimeBoundaryFor(SeqRuntimeCodeQuery)
		return rtUnknownValue(), []*rtState{s}, true
	}
	q.SourceID = runtimeResolveSource(e.an.snap, sym)
	q.Args = append([]rtValue(nil), args...)
	e.an.queries[call] = q
	ev.Table = q.Table
	ev.Columns = append([]string(nil), q.Columns...)
	ev.Query = runtimePredicateDisplays(q.Predicates)
	// 绑定检查（查询身份已就绪后才能给出准确边界）。
	if sym == "" {
		ev.Reason = "binding-type-unproven"
		return rtUnknownValue(), []*rtState{s}, true
	}
	if mutated {
		ev.Reason = "binding-mutated"
		e.recordReadResource(call, SeqResourceMutated, sym, "")
		return rtUnknownValue(), []*rtState{s}, true
	}
	source := q.SourceID
	if source == "" {
		ev.Reason = "binding-missing"
		ev.Boundary = seqRuntimeBoundaryFor(SeqRuntimeCodeBinding)
		e.recordReadResource(call, SeqResourceBindingMissing, sym, "")
		return rtUnknownValue(), []*rtState{s}, true
	}
	ev.SourceID = source
	e.recordReadResource(call, SeqResourceBound, sym, source)
	obs, err := runtimeObservationFor(e.an.snap, q)
	if err != nil {
		ev.Reason = err.Error()
		if gap, ok := err.(*seqRuntimeGap); ok {
			ev.Boundary = seqRuntimeBoundaryFor(gap.Code)
		}
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

// runtimeQuerySQLArg 标准库读取方法的 SQL 实参位置（T19c）：QueryRow/Query
// 没有 Context 参数（SQL 在 0），*Context 版本在 1。调用方已按接收者身份
// 证明是 database/sql（自定义同名方法走通用调用路径）。
func runtimeQuerySQLArg(name string) int {
	switch name {
	case "QueryRow", "Query":
		return 0
	}
	return 1
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
		if len(d.Values) > 0 {
			return false // 已初始化（`var DB = open()` / `= (*sql.DB)(nil)`）
		}
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

package action

// todo11-4：把 `query effects` 的函数级汇总细化为**路径级**摘要。
// todo12-1/7：目标筛选（字段/返回槽）、路径预算与完整性诊断。
//
// 用户确认的 Q6：
//   - 输出 `paths[]`：`{id, conditions, writes, remote_writes, tx, result,
//     reachable, notes}`；互斥分支各自成路径，`return` 之后的写点不进该路径
//   - 场景约束按**路径可达性**生效（被场景排除的分支 → 该路径 reachable=false
//     + 依据），不再按"场景键与字段名匹配"过滤写入
//   - 函数级汇总保留（db_writes / remote_writes / tx / result）作为兼容视图
//
// 路径枚举口径：AST 语句顺序 + 分支两侧；循环体按"0..N 次"处理（区间覆盖 +
// notes 标注，不假装只执行一次）；defer 标注"函数退出时执行"。
// todo12-7：预算/嵌套/循环/switch 截断一律进诊断（analysis.status=partial），
// 不再静默丢弃待展开路径。

import (
	"go/ast"
	"go/token"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// EffectPath 一条执行路径的副作用与返回。
type EffectPath struct {
	ID         string         `json:"id"` // p1, p2, ...
	Conditions []string       `json:"conditions,omitempty"`
	Writes     []DBEffect     `json:"writes"`
	Remote     []RemoteEffect `json:"remote_writes"`
	Calls      []EffectCall   `json:"calls,omitempty"` // todo12-1：路径上的调用（被调写入/依赖依据）
	Tx         *TxScope       `json:"tx,omitempty"`
	Result     *ResultEffect  `json:"result,omitempty"`
	Reachable  bool           `json:"reachable"`
	Notes      []string       `json:"notes,omitempty"`
}

// EffectCall 路径上的一次调用（todo12-1 R3/A6：保留影响目标的调用）。
type EffectCall struct {
	Function string `json:"function,omitempty"` // 被调符号（canonical ID）
	File     string `json:"file,omitempty"`     // 被调定义文件（仓库相对）
	Line     int    `json:"line,omitempty"`     // 调用点行号
	Target   bool   `json:"target,omitempty"`   // 该调用命中目标依赖（写目标字段等）
	Reason   string `json:"reason,omitempty"`
}

// dbWriteSite 一个字段写入点（源码观察；与 SSA 摘要行号可能不同——
// 摘要按 (function,access,field) 唯一，多行写会坍缩成一条）。
type dbWriteSite struct {
	field string
	line  int
	rhs   []ast.Expr // 右值表达式（依赖分析用；可能为空）
}

// localAssign 局部变量赋值（dependency 分析用——不直接输出为字段写入）。
type localAssign struct {
	name string
	line int
	rhs  []ast.Expr
}

// effectCallSite 路径上的调用表达式（映射被调符号在 build 阶段完成）。
type effectCallSite struct {
	line  int
	name  string
	args  []ast.Expr
	rhss  []ast.Expr // 赋值右侧的调用（结果赋值给局部/字段）
	assig string     // 结果接收者（局部名/字段文本）
}

// pathSpec 枚举期间的一条路径（区间 + 条件 + 返回）。
type pathSpec struct {
	conditions  []string
	writes      []dbWriteSite // AST 观察到的字段写入（路径归属用）
	locals      []localAssign // 局部变量赋值（依赖分析用）
	calls       []effectCallSite
	intervals   [][2]int
	returnLine  int
	returnVals  []string
	returnExprs []ast.Expr
	returnNil   bool
	notes       []string
	reachable   bool
}

// pathWalkStats 路径枚举的完整性统计（todo12-7）。
type pathWalkStats struct {
	dropped      int // 预算截断丢弃的终态路径数
	nestTrunc    int // 嵌套过深未展开的路径数
	loops        int // 未逐次展开的循环数
	switches     int // 未细化的 switch/select 数
	unknownCalls int // 无法映射到索引符号的调用点（保守保留）
}

// maxEffectPaths 默认路径数上限（分支爆炸保护；--path-budget 可调）。
const maxEffectPaths = 24

// effectPaths 枚举函数体的执行路径（分支两侧各一条；return 终止当前路径）。
//
// 实现用"待续路径"模型：每步对每条待续路径展开，`return` 直接落为终态，
// 语句列表走完仍待续的路径在顶层收尾——这样"if 后还有语句"不会被误判成
// 路径结束（曾因此产出重复/空路径）。
func (a *Actions) effectPaths(fset *token.FileSet, src []byte, body *ast.BlockStmt,
	scene Scene, args map[string]string, budget int) ([]pathSpec, *pathWalkStats) {
	if budget <= 0 {
		budget = maxEffectPaths
	}
	st := &pathWalkStats{}
	base := pathSpec{reachable: true}
	out := []pathSpec{}
	pending := walkEffectPaths(fset, src, body.List, []pathSpec{base}, &out, scene, args, 0, st, budget)
	for _, p := range pending {
		if len(out) >= budget {
			st.dropped++
			continue
		}
		p.notes = append(p.notes, "路径自然结束（无显式 return）")
		out = append(out, p)
	}
	if len(out) == 0 {
		base.notes = append(base.notes, "路径自然结束（无显式 return）")
		out = append(out, base)
	}
	return out, st
}

// walkEffectPaths 语句列表 → (终态路径写入 out, 返回待续路径)。
func walkEffectPaths(fset *token.FileSet, src []byte, stmts []ast.Stmt, pending []pathSpec,
	out *[]pathSpec, scene Scene, args map[string]string, depth int,
	st *pathWalkStats, budget int) []pathSpec {
	if depth > 12 {
		for i := range pending {
			pending[i].notes = append(pending[i].notes, "分支嵌套过深：未继续展开")
			st.nestTrunc++
		}
		return pending
	}
	span := func(n ast.Node) [2]int {
		return [2]int{fset.Position(n.Pos()).Line, fset.Position(n.End()).Line}
	}
	for _, stmt := range stmts {
		if len(*out) >= budget {
			st.dropped += len(pending)
			return nil
		}
		var next []pathSpec
		for _, cur := range pending {
			switch x := stmt.(type) {
			case *ast.ReturnStmt:
				done := cur
				done.intervals = append(done.intervals, span(x))
				done.returnLine = fset.Position(x.Pos()).Line
				for _, r := range x.Results {
					done.returnVals = append(done.returnVals, strings.TrimSpace(nodeText(fset, src, r)))
					done.returnExprs = append(done.returnExprs, r)
					if id, ok := r.(*ast.Ident); ok && id.Name == "nil" {
						done.returnNil = true
					}
				}
				if len(*out) < budget {
					*out = append(*out, done)
				} else {
					st.dropped++
				}
				continue // return 之后不属于该路径（不进入待续）
			case *ast.IfStmt:
				cond := seqExprText(fset, src, x.Cond)
				fit := scene.matchCond(x.Cond, args)
				thenP := cur
				thenP.conditions = append(append([]string{}, cur.conditions...), cond)
				thenP.intervals = append(thenP.intervals, span(x.Body))
				if fit.Status == SceneExcluded {
					thenP.reachable = false
					thenP.notes = append(thenP.notes, "场景排除："+sceneReason(&fit))
				}
				next = append(next, walkEffectPaths(fset, src, x.Body.List, []pathSpec{thenP}, out, scene, args, depth+1, st, budget)...)

				elseP := cur
				elseP.conditions = append(append([]string{}, cur.conditions...), "!("+cond+")")
				if fit.Status == SceneTaken {
					elseP.reachable = false
					elseP.notes = append(elseP.notes, "场景排除："+sceneReason(&fit)+"（else 不成立）")
				}
				switch e := x.Else.(type) {
				case *ast.BlockStmt:
					ep := elseP
					ep.intervals = append(ep.intervals, span(e))
					next = append(next, walkEffectPaths(fset, src, e.List, []pathSpec{ep}, out, scene, args, depth+1, st, budget)...)
				case *ast.IfStmt:
					next = append(next, walkEffectPaths(fset, src, []ast.Stmt{e}, []pathSpec{elseP}, out, scene, args, depth+1, st, budget)...)
				default:
					next = append(next, elseP) // 无 else：条件不成立时继续后续语句
				}
			case *ast.ForStmt, *ast.RangeStmt:
				cur.intervals = append(cur.intervals, span(x))
				cur.notes = append(cur.notes, "循环体：可能执行 0..N 次（写点按可能发生计，未逐次展开）")
				cur.calls = append(cur.calls, collectCallSites(fset, src, x)...)
				st.loops++
				next = append(next, cur)
			case *ast.DeferStmt:
				cur.intervals = append(cur.intervals, span(x))
				cur.notes = append(cur.notes, "defer：函数退出时执行（写点晚于 return 求值）")
				cur.calls = append(cur.calls, collectCallSites(fset, src, x)...)
				next = append(next, cur)
			case *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt:
				cur.intervals = append(cur.intervals, span(x))
				cur.notes = append(cur.notes, "switch/select：分支未细化为独立路径（写点按可能发生计）")
				cur.calls = append(cur.calls, collectCallSites(fset, src, x)...)
				st.switches++
				next = append(next, cur)
			case *ast.AssignStmt:
				cur.intervals = append(cur.intervals, span(x))
				for i, lhs := range x.Lhs {
					var rhs []ast.Expr
					if len(x.Rhs) == len(x.Lhs) {
						rhs = []ast.Expr{x.Rhs[i]}
					} else {
						rhs = x.Rhs
					}
					switch l := lhs.(type) {
					case *ast.SelectorExpr:
						cur.writes = append(cur.writes, dbWriteSite{field: exprTextShallow(l),
							line: fset.Position(l.Pos()).Line, rhs: rhs})
					case *ast.Ident:
						cur.locals = append(cur.locals, localAssign{name: l.Name,
							line: fset.Position(l.Pos()).Line, rhs: rhs})
					}
				}
				cur.calls = append(cur.calls, collectCallSites(fset, src, x)...)
				next = append(next, cur)
			case *ast.DeclStmt:
				cur.intervals = append(cur.intervals, span(x))
				if gd, ok := x.Decl.(*ast.GenDecl); ok {
					for _, sp := range gd.Specs {
						vs, ok := sp.(*ast.ValueSpec)
						if !ok {
							continue
						}
						for i, name := range vs.Names {
							var rhs []ast.Expr
							if len(vs.Values) == len(vs.Names) {
								rhs = []ast.Expr{vs.Values[i]}
							} else {
								rhs = vs.Values
							}
							cur.locals = append(cur.locals, localAssign{name: name.Name,
								line: fset.Position(name.Pos()).Line, rhs: rhs})
						}
					}
				}
				next = append(next, cur)
			default:
				cur.intervals = append(cur.intervals, span(stmt))
				cur.calls = append(cur.calls, collectCallSites(fset, src, stmt)...)
				next = append(next, cur)
			}
			if len(next) > budget*2 {
				break
			}
		}
		pending = next
		if len(pending) == 0 {
			return nil
		}
	}
	return pending
}

// collectCallSites 语句内的调用表达式（直接调用；嵌套调用也记录，build 阶段
// 按行号映射索引边）。
func collectCallSites(fset *token.FileSet, src []byte, n ast.Node) []effectCallSite {
	var out []effectCallSite
	ast.Inspect(n, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		cs := effectCallSite{line: fset.Position(call.Lparen).Line,
			name: strings.TrimSpace(nodeText(fset, src, call.Fun))}
		cs.args = append(cs.args, call.Args...)
		out = append(out, cs)
		return true
	})
	return out
}

// sceneReason 场景判定的依据文本（路径 notes 用）。
func sceneReason(f *SceneFit) string {
	if f == nil {
		return "unknown"
	}
	if f.Reason != "" {
		return f.Reason
	}
	return f.Status
}

// pathCovers 行是否落在路径区间内（区间为闭区间）。
func pathCovers(p pathSpec, line int) bool {
	if line <= 0 {
		return false
	}
	for _, iv := range p.intervals {
		if line >= iv[0] && line <= iv[1] {
			return true
		}
	}
	return false
}

// buildParams 路径构建上下文（路径归属 + 目标筛选）。
type buildParams struct {
	colIdx      tableColumnIndex
	remote      []RemoteEffect
	tx          TxScope
	suspicious  []string
	resultNames []string
	// 目标筛选（nil = 不筛选，保持既有契约）
	target *effectsTarget
	// 被调写入归属（todo12-1 A6 / todo12-8）
	file         string            // 当前函数文件（证据定位用）
	varTypes     map[string]string // 局部变量/形参 → 类型短名
	pkgPath      string
	callTargets  map[int][]string      // 调用行 → 被调符号
	calleeWrites map[string][]DBEffect // 被调符号 → 字段写入（带文件/行）
	calleeFiles  map[string]string
}

// buildEffectPaths 把函数级汇总按路径区间归属到各路径（todo11-4/12-1/12-7/12-8）。
func buildEffectPaths(specs []pathSpec, bp buildParams) []EffectPath {
	out := make([]EffectPath, 0, len(specs))
	for i, sp := range specs {
		p := EffectPath{ID: "p" + itoaSeq(i+1), Conditions: sp.conditions,
			Reachable: sp.reachable, Notes: dedupStrings(sp.notes),
			Writes: []DBEffect{}, Remote: []RemoteEffect{}}
		// 路径写入：AST 观察点（同一字段多次写不会被摘要坍缩），带表列映射
		seen := map[string]bool{}
		for _, ws := range sp.writes {
			id, known := writeIdentity(ws.field, bp.varTypes, bp.pkgPath)
			keep, unknown := true, false
			if bp.target != nil && bp.target.kind == "field" {
				keep, unknown = bp.target.keepFieldWrite(id, known, fieldLeaf(ws.field))
			}
			if bp.target != nil && bp.target.kind == "return" {
				keep = bp.target.keepWriteForReturn(ws, sp)
			}
			if !keep {
				continue
			}
			key := ws.field + ":" + itoaSeq(ws.line)
			if seen[key] {
				continue
			}
			seen[key] = true
			ev := &SeqEvidence{Source: SeqEvidenceAST, CallFile: bp.file, CallLine: ws.line,
				Reason: "ast-assign", Verdict: SeqVerdictCorroborated,
				Query: "codeintel query table " + fieldLeaf(ws.field)}
			if unknown {
				ev.Reason = "ast-assign;target-match:unresolved-type"
				ev.Verdict = SeqVerdictUnverified
			}
			table, column, ok := bp.colIdx.lookup(fieldLeaf(ws.field))
			if !ok {
				ev.Verdict = SeqVerdictUnverified
				if ev.Reason == "" {
					ev.Reason = "no_table_mapping"
				} else {
					ev.Reason += ";no_table_mapping"
				}
			}
			p.Writes = append(p.Writes, DBEffect{Table: table, Column: column, Field: ws.field,
				Line: ws.line, Access: string(domain.SummaryDirectWrite), Evidence: ev})
		}
		// 被调写入（depth 内）：按调用行归属到路径（todo12-1 A6 / todo12-8）
		p.Calls = bp.pathCalls(sp, &p)
		for _, r := range bp.remote {
			if bp.target != nil {
				break // 目标模式下远程写与值目标无关（排除已证明无关内容）
			}
			if pathCovers(sp, r.Line) {
				p.Remote = append(p.Remote, r)
			}
		}
		if bp.tx.Kind != "none" && (pathCovers(sp, bp.tx.BeginLine) || pathCovers(sp, bp.tx.CommitLine)) {
			t := bp.tx
			p.Tx = &t
		}
		// 返回结果：该路径的 return（early return 路径只到它自己）
		if bp.target != nil {
			p.Result = bp.target.filterPathResult(sp, bp.resultNames)
			if bp.target.kind == "field" && p.Result == nil {
				p.Notes = append(p.Notes, "该路径的返回不涉及目标字段")
			}
			p.Notes = dedupStrings(p.Notes)
		} else if sp.returnLine > 0 {
			res := &ResultEffect{Returns: sp.returnVals, Line: sp.returnLine,
				Verdict: SeqVerdictCorroborated, Detail: "返回值来自该路径的 return"}
			for i, v := range sp.returnVals {
				res.Slots = append(res.Slots, seqResultSlot(i, len(sp.returnVals), bp.resultNames))
				_ = v
			}
			for _, s := range bp.suspicious {
				if strings.HasSuffix(s, ":"+itoaSeq(sp.returnLine)) {
					res.Verdict = SeqVerdictUnverified
					res.Detail = "该路径存在错误被覆盖/吞掉的形态（可疑）"
					res.Suspicious = []string{s}
				}
			}
			p.Result = res
		}
		out = append(out, p)
	}
	return out
}

// pathCalls 路径上的调用：直接调用（从 statements 收集）+ 被调字段写入归属。
func (bp buildParams) pathCalls(sp pathSpec, p *EffectPath) []EffectCall {
	if len(sp.calls) == 0 {
		return nil
	}
	var out []EffectCall
	seen := map[string]bool{}
	for _, cs := range sp.calls {
		for _, tid := range bp.callTargets[cs.line] {
			key := tid + ":" + itoaSeq(cs.line)
			if seen[key] {
				continue
			}
			seen[key] = true
			ec := EffectCall{Function: tid, File: bp.calleeFiles[tid], Line: cs.line}
			for _, w := range bp.calleeWrites[tid] {
				keep := true
				if bp.target != nil && bp.target.kind == "field" {
					keep = bp.target.fieldID != "" && w.Field == bp.target.fieldID
				} else if bp.target != nil && bp.target.kind == "return" {
					keep = false // 返回槽目标：被调字段写入未证明影响返回值
				}
				if !keep {
					continue
				}
				if seen["w:"+w.Function+":"+w.Field+":"+itoaSeq(w.Line)] {
					continue
				}
				seen["w:"+w.Function+":"+w.Field+":"+itoaSeq(w.Line)] = true
				if w.Evidence != nil {
					ev := *w.Evidence
					ev.CallFile = bp.file
					ev.CallLine = cs.line
					if !strings.Contains(ev.Reason, "callee") {
						ev.Reason = strings.TrimSuffix("callee-field-summary;"+ev.Reason, ";")
					}
					w.Evidence = &ev
				}
				p.Writes = append(p.Writes, w)
				ec.Target = true
				ec.Reason = "callee-write"
			}
			if bp.target != nil && (bp.target.kind == "field" && !ec.Target) {
				continue // 目标字段模式：只保留命中目标的调用
			}
			out = append(out, ec)
		}
	}
	return out
}

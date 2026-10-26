package action

// todo11-4：把 `query effects` 的函数级汇总细化为**路径级**摘要。
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
	Tx         *TxScope       `json:"tx,omitempty"`
	Result     *ResultEffect  `json:"result,omitempty"`
	Reachable  bool           `json:"reachable"`
	Notes      []string       `json:"notes,omitempty"`
}

// dbWriteSite 一个字段写入点（源码观察；与 SSA 摘要行号可能不同——
// 摘要按 (function,access,field) 唯一，多行写会坍缩成一条）。
type dbWriteSite struct {
	field string
	line  int
}

// maxEffectPaths 路径数上限（分支爆炸保护；超出即停并在 notes 说明）。
const maxEffectPaths = 24

// pathSpec 枚举期间的一条路径（区间 + 条件 + 返回）。
type pathSpec struct {
	conditions []string
	writes     []dbWriteSite // AST 观察到的字段写入（路径归属用）
	intervals  [][2]int
	returnLine int
	returnVals []string
	returnNil  bool
	notes      []string
	reachable  bool
}

// effectPaths 枚举函数体的执行路径（分支两侧各一条；return 终止当前路径）。
//
// 实现用"待续路径"模型：每步对每条待续路径展开，`return` 直接落为终态，
// 语句列表走完仍待续的路径在顶层收尾——这样"if 后还有语句"不会被误判成
// 路径结束（曾因此产出重复/空路径）。
func (a *Actions) effectPaths(fset *token.FileSet, src []byte, body *ast.BlockStmt,
	scene Scene, args map[string]string) []pathSpec {
	base := pathSpec{reachable: true}
	out := []pathSpec{}
	pending := walkEffectPaths(fset, src, body.List, []pathSpec{base}, &out, scene, args, 0)
	for _, p := range pending {
		if len(out) >= maxEffectPaths {
			break
		}
		p.notes = append(p.notes, "路径自然结束（无显式 return）")
		out = append(out, p)
	}
	if len(out) == 0 {
		base.notes = append(base.notes, "路径自然结束（无显式 return）")
		out = append(out, base)
	}
	return out
}

// walkEffectPaths 语句列表 → (终态路径写入 out, 返回待续路径)。
func walkEffectPaths(fset *token.FileSet, src []byte, stmts []ast.Stmt, pending []pathSpec,
	out *[]pathSpec, scene Scene, args map[string]string, depth int) []pathSpec {
	if depth > 12 {
		for i := range pending {
			pending[i].notes = append(pending[i].notes, "分支嵌套过深：未继续展开")
		}
		return pending
	}
	span := func(n ast.Node) [2]int {
		return [2]int{fset.Position(n.Pos()).Line, fset.Position(n.End()).Line}
	}
	for _, st := range stmts {
		if len(*out) >= maxEffectPaths {
			return nil
		}
		var next []pathSpec
		for _, cur := range pending {
			switch x := st.(type) {
			case *ast.ReturnStmt:
				done := cur
				done.intervals = append(done.intervals, span(x))
				done.returnLine = fset.Position(x.Pos()).Line
				for _, r := range x.Results {
					done.returnVals = append(done.returnVals, strings.TrimSpace(nodeText(fset, src, r)))
					if id, ok := r.(*ast.Ident); ok && id.Name == "nil" {
						done.returnNil = true
					}
				}
				if len(*out) < maxEffectPaths {
					*out = append(*out, done)
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
				next = append(next, walkEffectPaths(fset, src, x.Body.List, []pathSpec{thenP}, out, scene, args, depth+1)...)

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
					next = append(next, walkEffectPaths(fset, src, e.List, []pathSpec{ep}, out, scene, args, depth+1)...)
				case *ast.IfStmt:
					next = append(next, walkEffectPaths(fset, src, []ast.Stmt{e}, []pathSpec{elseP}, out, scene, args, depth+1)...)
				default:
					next = append(next, elseP) // 无 else：条件不成立时继续后续语句
				}
			case *ast.ForStmt, *ast.RangeStmt:
				cur.intervals = append(cur.intervals, span(x))
				cur.notes = append(cur.notes, "循环体：可能执行 0..N 次（写点按可能发生计）")
				next = append(next, cur)
			case *ast.DeferStmt:
				cur.intervals = append(cur.intervals, span(x))
				cur.notes = append(cur.notes, "defer：函数退出时执行（写点晚于 return 求值）")
				next = append(next, cur)
			case *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt:
				cur.intervals = append(cur.intervals, span(x))
				cur.notes = append(cur.notes, "switch/select：分支未细化为独立路径（写点按可能发生计）")
				next = append(next, cur)
			case *ast.AssignStmt:
				cur.intervals = append(cur.intervals, span(x))
				for _, lhs := range x.Lhs {
					if sel, ok := lhs.(*ast.SelectorExpr); ok {
						cur.writes = append(cur.writes, dbWriteSite{field: exprTextShallow(sel),
							line: fset.Position(sel.Pos()).Line})
					}
				}
				next = append(next, cur)
			default:
				cur.intervals = append(cur.intervals, span(st))
				next = append(next, cur)
			}
			if len(next) > maxEffectPaths*2 {
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

// buildEffectPaths 把函数级汇总按路径区间归属到各路径（todo11-4）。
func buildEffectPaths(specs []pathSpec, colIdx tableColumnIndex, remote []RemoteEffect,
	tx TxScope, suspicious []string) []EffectPath {
	out := make([]EffectPath, 0, len(specs))
	for i, sp := range specs {
		p := EffectPath{ID: "p" + itoaSeq(i+1), Conditions: sp.conditions,
			Reachable: sp.reachable, Notes: dedupStrings(sp.notes),
			Writes: []DBEffect{}, Remote: []RemoteEffect{}}
		// 路径写入：AST 观察点（同一字段多次写不会被摘要坍缩），带表列映射
		seen := map[string]bool{}
		for _, ws := range sp.writes {
			key := ws.field + ":" + itoaSeq(ws.line)
			if seen[key] {
				continue
			}
			seen[key] = true
			ev := &SeqEvidence{Source: SeqEvidenceAST, CallLine: ws.line, Reason: "ast-assign",
				Verdict: SeqVerdictCorroborated, Query: "codeintel query table " + fieldLeaf(ws.field)}
			table, column, ok := colIdx.lookup(fieldLeaf(ws.field))
			if !ok {
				ev.Verdict, ev.Reason = SeqVerdictUnverified, "no_table_mapping"
			}
			p.Writes = append(p.Writes, DBEffect{Table: table, Column: column, Field: ws.field,
				Line: ws.line, Access: string(domain.SummaryDirectWrite), Evidence: ev})
		}
		for _, r := range remote {
			if pathCovers(sp, r.Line) {
				p.Remote = append(p.Remote, r)
			}
		}
		if tx.Kind != "none" && (pathCovers(sp, tx.BeginLine) || pathCovers(sp, tx.CommitLine)) {
			t := tx
			p.Tx = &t
		}
		// 返回结果：该路径的 return（early return 路径只到它自己）
		if sp.returnLine > 0 {
			res := &ResultEffect{Returns: sp.returnVals, Line: sp.returnLine,
				Verdict: SeqVerdictCorroborated, Detail: "返回值来自该路径的 return"}
			for _, s := range suspicious {
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

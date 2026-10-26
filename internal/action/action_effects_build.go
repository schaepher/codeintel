package action

import (
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

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
func buildEffectPaths(specs []pathSpec, bp buildParams) ([]EffectPath, int) {
	out := make([]EffectPath, 0, len(specs))
	unresolvedCalls := 0
	for i, sp := range specs {
		p := EffectPath{ID: "p" + itoaSeq(i+1), Conditions: sp.conditions,
			Reachable: sp.reachable, Notes: dedupStrings(sp.notes),
			Writes: []DBEffect{}, Remote: []RemoteEffect{}}

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

			table, column, ok := bp.colIdx.lookupExact(ws.field)
			if !ok {
				ev.Verdict = SeqVerdictUnverified
				if ev.Reason == "" {
					ev.Reason = "no_table_mapping"
				} else {
					ev.Reason += ";no_table_mapping"
				}
			}
			p.Writes = append(p.Writes, DBEffect{Table: table, Column: column, Field: ws.field,
				Line: ws.line, Access: string(domain.SummaryDirectWrite), Origin: "ast",
				Kind: "field_assign", Evidence: ev})
		}

		p.Calls, unresolvedCalls = bp.pathCalls(sp, &p, unresolvedCalls)
		for _, r := range bp.remote {
			if bp.target != nil {
				break
			}
			if pathCovers(sp, r.Line) {
				p.Remote = append(p.Remote, r)
			}
		}
		if bp.tx.Kind != "none" && (pathCovers(sp, bp.tx.BeginLine) || pathCovers(sp, bp.tx.CommitLine)) {
			t := bp.tx
			p.Tx = &t
		}

		if bp.target != nil {
			p.Result = bp.target.filterPathResult(sp, bp.resultNames)
			if p.Result != nil {
				p.Result.ErrorSources = effectErrorSources(sp, bp.resultNames, p.Result.Slots)
			}
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
			res.ErrorSources = effectErrorSources(sp, bp.resultNames, res.Slots)
			p.Result = res
		}
		out = append(out, p)
	}
	return out, unresolvedCalls
}

// pathCalls 路径上的调用：直接调用（从 statements 收集）+ 被调字段写入归属。
func (bp buildParams) pathCalls(sp pathSpec, p *EffectPath, unresolved int) ([]EffectCall, int) {
	if len(sp.calls) == 0 {
		return nil, unresolved
	}
	var out []EffectCall
	seen := map[string]bool{}
	for _, cs := range sp.calls {
		if len(bp.callTargets[cs.line]) == 0 {
			unresolved++
			continue
		}
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
					keep = false
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
				w.Origin = "callee"
				if w.Kind == "" {
					w.Kind = "callee_write"
				}
				p.Writes = append(p.Writes, w)
				ec.Target = true
				ec.Reason = "callee-write"
			}
			if bp.target != nil && (bp.target.kind == "field" && !ec.Target) {
				continue
			}
			out = append(out, ec)
		}
	}
	return out, unresolved
}

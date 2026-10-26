package action

// todo10-7：按路径输出副作用与返回结果（`query effects`）。
//
// 输出四组（用户确认的 Q5）：
//   - db_writes：表.列 写入（SSA 摘要的 direct/indirect_write + 表列映射）
//   - remote_writes：grpc/http 出站写（索引边）
//   - tx_scope：事务范围（Begin/Commit/Rollback 调用序列，以及写点是否在范围内）
//   - result：返回值与错误 + "错误被后续赋值覆盖"的可疑形态
//
// 契约：优先覆盖"指定函数"的相关路径；未知一律带 verdict（direct /
// corroborated / unverified）与原因，不把静态推断当线上事实。
//
// 明确不做：把错误返回解释成"全部写入回滚"——事务范围只陈述观察到的事实。

import (
	"fmt"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// EffectsRequest query effects 参数。
type EffectsRequest struct {
	Target  string // 函数/方法（canonical ID 或名称）
	RepoAbs string
	Depth   int // 展开层级（1 = 只本函数；默认 1）
	Scene   Scene
	// todo12-1：目标筛选（互斥；空 = 不筛选，保持既有契约）
	TargetField  string // 类型限定字段路径（pkg.Type.Field）
	TargetReturn string // 返回槽名字或 #序号
	TargetSet    bool   // 是否显式指定了目标（用于区分"未指定"与"空值"）
	PathBudget   int    // 路径枚举预算（0 = 默认 24）
}

// DBEffect 一次表列写入。
type DBEffect struct {
	Table    string       `json:"table,omitempty"`
	Column   string       `json:"column,omitempty"`
	Field    string       `json:"field"`            // SSA 摘要的字段路径
	Function string       `json:"function"`         // 写入函数（canonical ID）
	File     string       `json:"file,omitempty"`   // 写入函数所在文件（被调写入定位用）
	Line     int          `json:"line,omitempty"`   // 写入行
	Access   string       `json:"access"`           // direct_write / indirect_write
	Origin   string       `json:"origin,omitempty"` // todo12-8：ast|callee|summary（写点来源）
	Evidence *SeqEvidence `json:"evidence,omitempty"`
}

// RemoteEffect 一次远程写（grpc/http 出站）。
type RemoteEffect struct {
	Kind     string       `json:"kind"` // grpc | http
	Service  string       `json:"service"`
	Method   string       `json:"method,omitempty"`
	Path     string       `json:"path,omitempty"`
	Function string       `json:"function"`
	Line     int          `json:"line,omitempty"`
	Evidence *SeqEvidence `json:"evidence,omitempty"`
}

// TxScope 事务范围（观察到的事实，不含"是否回滚"的推断）。
type TxScope struct {
	Kind         string `json:"kind"` // none|begin_commit|tx_wrapper
	BeginLine    int    `json:"begin_line,omitempty"`
	CommitLine   int    `json:"commit_line,omitempty"`
	RollbackLine int    `json:"rollback_line,omitempty"`
	CoversWrites bool   `json:"covers_writes"` // 写入行是否落在 [begin, commit) 之间
	Verdict      string `json:"verdict"`
	Detail       string `json:"detail,omitempty"`
}

// ResultEffect 返回结果与可疑覆盖。
// ErrorSource 一条返回槽的错误来源判定（todo12-8 R3）。
type ErrorSource struct {
	Slot          int    `json:"slot"`
	Expr          string `json:"expr"`
	Var           string `json:"var,omitempty"`
	Line          int    `json:"line,omitempty"`
	Status        string `json:"status"` // returned|wrapped|swallowed|overwritten|stale|nil|unknown
	OverwrittenAt int    `json:"overwritten_at,omitempty"`
	Detail        string `json:"detail,omitempty"`
}

type ResultEffect struct {
	Returns    []string `json:"returns,omitempty"`    // 返回表达式文本
	Slots      []string `json:"slots,omitempty"`      // 结果槽
	Line       int      `json:"line,omitempty"`       // return 行
	ErrorVars  []string `json:"error_vars,omitempty"` // 被当作错误的变量
	Suspicious []string `json:"suspicious"`           // "错误被后续赋值覆盖"的可疑点（file:line；空数组也输出）
	// todo12-8：每条返回槽的错误来源（区分返回/覆盖/吞掉/旧错误）
	ErrorSources []ErrorSource `json:"error_sources,omitempty"`
	Verdict      string        `json:"verdict"`
	Detail       string        `json:"detail,omitempty"`
}

// EffectsResult query effects 输出契约。
type EffectsResult struct {
	Target   string `json:"target"`
	Function string `json:"function"`
	File     string `json:"file,omitempty"`
	// todo12-1：目标筛选（不指定目标时省略，保持既有契约）
	TargetInfo   *EffectsTarget   `json:"target_info,omitempty"`
	Analysis     *EffectsAnalysis `json:"analysis,omitempty"`
	DBWrites     []DBEffect       `json:"db_writes"`
	RemoteWrites []RemoteEffect   `json:"remote_writes"`
	Paths        []EffectPath     `json:"paths,omitempty"` // todo11-4：按执行路径
	Tx           TxScope          `json:"tx"`
	Result       ResultEffect     `json:"result"`
	Diagnostics  []SeqDiagCount   `json:"diagnostics,omitempty"`
}

// tx 调用名（扫描 AST 用）。
var txBeginNames = map[string]bool{"begin": true, "begintx": true, "beginx": true}
var txCommitNames = map[string]bool{"commit": true, "committx": true}
var txRollbackNames = map[string]bool{"rollback": true, "rollbacktx": true}
var txWrapperNames = map[string]bool{"transaction": true, "tx": true, "withtransaction": true}

// Effects 收集指定函数的副作用与返回结果。
func (a *Actions) Effects(req EffectsRequest) (*EffectsResult, error) {
	logger := zap.L()
	logger.Info("enter (Actions).Effects", zap.String("target", req.Target), zap.Int("depth", req.Depth))
	defer logger.Info("exit (Actions).Effects")
	if req.Depth <= 0 {
		req.Depth = 1
	}
	n, err := a.ResolveSymbol(req.Target)
	if err != nil {
		return nil, err
	}
	res := &EffectsResult{Target: req.Target, Function: string(n.ID), File: n.FilePath,
		DBWrites: []DBEffect{}, RemoteWrites: []RemoteEffect{},
		Tx:     TxScope{Kind: "none", Verdict: SeqVerdictUnverified, Detail: "本函数未观察到事务调用"},
		Result: ResultEffect{Verdict: SeqVerdictUnverified},
	}

	// 参与分析的函数集合（本函数 + 深度展开的被调者）
	funcs := []domain.CanonicalID{n.ID}
	var calleeFacts []*domain.Fact
	if req.Depth > 1 {
		if facts, cerr := a.CalleesConcrete(n.ID, req.Depth-1); cerr == nil {
			calleeFacts = facts
			seen := map[domain.CanonicalID]bool{n.ID: true}
			for _, f := range facts {
				if !seen[f.TargetID] {
					seen[f.TargetID] = true
					funcs = append(funcs, f.TargetID)
				}
			}
		}
	}

	colIndex := a.tableColumnIndex()
	fileOf := map[domain.CanonicalID]string{n.ID: n.FilePath}
	calleeWrites := map[string][]DBEffect{}
	// 被调写入（todo12-1 A6 / todo12-8）：带被调文件与行号；路径归属在 build 阶段
	for _, fid := range funcs[1:] {
		rows, ferr := a.repo.GetFunctionFields(fid)
		if ferr != nil {
			continue
		}
		file := ""
		if cn, cerr := a.repo.GetSymbol(fid); cerr == nil {
			file = cn.FilePath
		}
		fileOf[fid] = file
		for _, r := range rows {
			if r.AccessKind != domain.SummaryDirectWrite && r.AccessKind != domain.SummaryIndirectWrite {
				continue
			}
			calleeWrites[string(fid)] = append(calleeWrites[string(fid)],
				a.effectWriteRow(fid, file, r, colIndex))
		}
	}

	// 1) db_writes：SSA 摘要 + 表列映射
	for _, fid := range funcs {
		rows, ferr := a.repo.GetFunctionFields(fid)
		if ferr != nil {
			continue
		}
		for _, r := range rows {
			if r.AccessKind != domain.SummaryDirectWrite && r.AccessKind != domain.SummaryIndirectWrite {
				continue
			}
			res.DBWrites = append(res.DBWrites, a.effectWriteRow(fid, fileOf[fid], r, colIndex))
		}
	}
	// todo11-4：场景约束改为**路径可达性**（不再按"字段名命中场景键"过滤
	// 写入——那是 todo10-7 的近似，已被本项取代）
	sort.Slice(res.DBWrites, func(i, j int) bool {
		if res.DBWrites[i].Field != res.DBWrites[j].Field {
			return res.DBWrites[i].Field < res.DBWrites[j].Field
		}
		return res.DBWrites[i].Line < res.DBWrites[j].Line
	})

	// 2) remote_writes：grpc_call / http_call 出站边
	inSet := map[string]bool{}
	for _, f := range funcs {
		inSet[string(f)] = true
	}
	if facts, ferr := a.repo.GetFactsByKinds(string(domain.FactGrpcCall), string(domain.FactHTTPCall)); ferr == nil {
		for _, f := range facts {
			if !inSet[string(f.SourceID)] {
				continue
			}
			ev := &SeqEvidence{Source: SeqEvidenceCallEdge, SymbolID: string(f.TargetID),
				CallLine: metaLine(f.Metadata), Confidence: f.Confidence,
				Query: "codeintel query grpc-callers " + string(f.SourceID)}
			ev.Verdict = seqEvidenceVerdict(ev)
			if f.Kind == domain.FactHTTPCall {
				res.RemoteWrites = append(res.RemoteWrites, RemoteEffect{Kind: "http",
					Service: metaStr(f.Metadata, "host"), Path: metaStr(f.Metadata, "path"),
					Function: string(f.SourceID), Line: metaLine(f.Metadata), Evidence: ev})
				continue
			}
			res.RemoteWrites = append(res.RemoteWrites, RemoteEffect{Kind: "grpc",
				Service: svcNameOfTarget(string(f.TargetID)), Method: metaStr(f.Metadata, "method"),
				Function: string(f.SourceID), Line: metaLine(f.Metadata), Evidence: ev})
		}
	}
	sort.Slice(res.RemoteWrites, func(i, j int) bool {
		if res.RemoteWrites[i].Service != res.RemoteWrites[j].Service {
			return res.RemoteWrites[i].Service < res.RemoteWrites[j].Service
		}
		return res.RemoteWrites[i].Line < res.RemoteWrites[j].Line
	})

	// 3)/4) 事务范围 + 返回结果（AST 扫描本函数）+ 路径级归属（todo11-4）
	scan := a.scanEffectsAST(req.RepoAbs, n, req.Scene, nil, req.PathBudget)
	res.Tx = scan.tx
	res.Result = scan.result

	// todo12-1：目标解析（需先解析源码拿到签名/参数类型）
	var tgt *effectsTarget
	if req.TargetSet || strings.TrimSpace(req.TargetField) != "" || strings.TrimSpace(req.TargetReturn) != "" {
		if scan.fn == nil {
			return nil, fmt.Errorf("无法解析目标：%s（需要源码与函数签名）", scan.tx.Detail)
		}
		tgt, err = a.resolveEffectsTarget(req.TargetField, req.TargetReturn,
			scan.fn, scan.fset, scan.src, scan.varTypes, scan.pkgPath)
		if err != nil {
			return nil, err
		}
		res.TargetInfo = &tgt.output
	}

	// 直接调用 → 被调符号（按调用行归属；接口具体化后的目标也算）
	callTargets := map[int][]string{}
	for _, f := range calleeFacts {
		if f.SourceID != n.ID {
			continue
		}
		if line := metaLine(f.Metadata); line > 0 {
			callTargets[line] = append(callTargets[line], string(f.TargetID))
		}
	}
	var unresolvedCalls int
	res.Paths, unresolvedCalls = buildEffectPaths(scan.paths, buildParams{
		colIdx: colIndex, remote: res.RemoteWrites, tx: res.Tx,
		suspicious: res.Result.Suspicious, resultNames: scan.resultNames,
		target: tgt, file: n.FilePath, varTypes: scan.varTypes, pkgPath: scan.pkgPath,
		callTargets: callTargets, calleeWrites: calleeWrites, calleeFiles: fileOfStrings(fileOf),
	})

	// 目标模式：同步筛选函数级汇总（路径写点的并集 > 摘要过滤）
	if tgt != nil {
		if tgt.kind == "field" {
			kept := res.DBWrites[:0]
			for _, w := range res.DBWrites {
				if w.Field == tgt.fieldID {
					kept = append(kept, w)
				}
			}
			res.DBWrites = kept
		} else {
			res.DBWrites = unionPathWrites(res.Paths)
		}
		res.RemoteWrites = []RemoteEffect{}
		res.Result = targetResultSummary(res.Paths, res.Result)
	}

	// todo12-5/7：完整性诊断（未知/截断 → partial）
	res.Analysis = effectAnalysis(req.Depth, scan.stats, tgt, res.DBWrites, unresolvedCalls)
	if len(res.DBWrites) == 0 && len(res.RemoteWrites) == 0 {
		res.Diagnostics = append(res.Diagnostics, SeqDiagCount{Code: "no_writes", Count: 1})
	}
	// 事务是否覆盖写入行（只陈述"写点在不在范围内"）
	if res.Tx.Kind != "none" && len(res.DBWrites) > 0 {
		res.Tx.CoversWrites = txCovers(&res.Tx, res.DBWrites)
	}
	return res, nil
}

// effectWriteRow 函数字段摘要 → DBEffect（带被调文件/行）。
func (a *Actions) effectWriteRow(fid domain.CanonicalID, file string, r *domain.FunctionFieldSummary,
	colIndex tableColumnIndex) DBEffect {
	ev := &SeqEvidence{Source: SeqEvidenceAST, SymbolID: string(fid), File: file,
		Line: r.LineStart, Query: "codeintel query table " + fieldLeaf(r.FieldPath)}
	table, column, ok := colIndex.lookup(fieldLeaf(r.FieldPath))
	if ok {
		ev.Verdict = SeqVerdictCorroborated
		ev.Reason = "field-to-column:" + fieldLeaf(r.FieldPath)
	} else {
		ev.Verdict = SeqVerdictUnverified
		ev.Reason = "no_table_mapping"
	}
	return DBEffect{Table: table, Column: column, Field: r.FieldPath, Function: string(fid),
		File: file, Line: r.LineStart, Access: string(r.AccessKind), Origin: "summary", Evidence: ev}
}

// fileOfStrings 文件映射（canonical ID → file）转字符串键。
func fileOfStrings(m map[domain.CanonicalID]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[string(k)] = v
	}
	return out
}

// unionPathWrites 路径写入并集（目标返回槽模式的函数级汇总）。
func unionPathWrites(paths []EffectPath) []DBEffect {
	seen := map[string]bool{}
	out := []DBEffect{}
	for _, p := range paths {
		for _, w := range p.Writes {
			key := w.Function + "\x00" + w.Field + "\x00" + itoaSeq(w.Line)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, w)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Field != out[j].Field {
			return out[i].Field < out[j].Field
		}
		return out[i].Line < out[j].Line
	})
	return out
}

// targetResultSummary 目标模式的函数级返回汇总（路径返回槽并集，不重编号）。
func targetResultSummary(paths []EffectPath, old ResultEffect) ResultEffect {
	seen := map[string]bool{}
	var returns, slots []string
	line := 0
	for _, p := range paths {
		if p.Result == nil {
			continue
		}
		if line == 0 {
			line = p.Result.Line
		}
		for i := range p.Result.Returns {
			key := itoaSeq(i) + "\x00" + p.Result.Returns[i]
			if seen[key] {
				continue
			}
			seen[key] = true
			returns = append(returns, p.Result.Returns[i])
			if i < len(p.Result.Slots) {
				slots = append(slots, p.Result.Slots[i])
			}
		}
	}
	res := ResultEffect{Returns: returns, Slots: slots, Line: line,
		Verdict: SeqVerdictCorroborated, ErrorVars: old.ErrorVars,
		Detail: "目标筛选后的函数级汇总（按路径返回槽）"}
	return res
}

// effectPartialCodes 判为 partial 的诊断代码。
var effectPartialCodes = map[string]bool{
	EffectDiagPathBudget:       true,
	EffectDiagPathNesting:      true,
	EffectDiagLoopUnrefined:    true,
	EffectDiagSwitchUnrefine:   true,
	EffectDiagTargetUnknown:    true,
	EffectDiagSlotUnresolved:   true,
	EffectDiagCalleeUnresolved: true,
}

// effectAnalysis 聚合完整性诊断（未知/截断 → partial）。
func effectAnalysis(depth int, st *pathWalkStats, tgt *effectsTarget, writes []DBEffect, unresolvedCalls int) *EffectsAnalysis {
	a := &EffectsAnalysis{Status: "complete", Depth: depth, Diagnostics: []SeqDiagCount{}}
	add := func(code, detail string, count int) {
		if count <= 0 {
			return
		}
		a.Diagnostics = append(a.Diagnostics, SeqDiagCount{Code: code, Count: count, Detail: detail})
		if effectPartialCodes[code] {
			a.Status = "partial"
		}
	}
	if st != nil {
		add(EffectDiagPathBudget,
			fmt.Sprintf("路径预算已达上限（丢弃 %d 个未展开终态；用 --path-budget 提高预算）", st.dropped), st.dropped)
		add(EffectDiagPathNesting, "分支嵌套超过 12 层，未继续展开", st.nestTrunc)
		add(EffectDiagLoopUnrefined, "循环体按 0..N 次计（未逐次展开）", st.loops)
		add(EffectDiagSwitchUnrefine, "switch/select 分支未细化为独立路径", st.switches)
	}
	add("path_callee_unresolved", "路径上存在未映射索引的调用点（被调写入可能遗漏）", unresolvedCalls)
	if tgt != nil {
		add(EffectDiagTargetUnknown,
			"目标依赖里有类型无法判定的写入（已保守保留，未证明与目标同类型）", tgt.unknown)
		add(EffectDiagSlotUnresolved,
			"部分路径的 return 未提供目标槽（多值调用/表达式个数不足）", tgt.slotMiss)
		add(EffectDiagTargetNoWrite, "目标合法但未观察到写入", boolCount(len(writes) == 0))
	}
	return a
}

// boolCount 条件计数（true = 1）。
func boolCount(b bool) int {
	if b {
		return 1
	}
	return 0
}

// txCovers 写点是否落在 [begin, commit) 之间（事务范围判定的事实陈述）。
func txCovers(tx *TxScope, writes []DBEffect) bool {
	if tx.BeginLine <= 0 {
		return false
	}
	end := tx.CommitLine
	if end <= 0 {
		end = int(^uint(0) >> 1) // 无 commit：视为开放范围
	}
	covers := 0
	for _, w := range writes {
		if w.Line >= tx.BeginLine && w.Line < end {
			covers++
		}
	}
	return covers > 0
}

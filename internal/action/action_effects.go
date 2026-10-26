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

// DBEffect 一次表列写入。

// RemoteEffect 一次远程写（grpc/http 出站）。

// TxScope 事务范围（观察到的事实，不含"是否回滚"的推断）。

// ResultEffect 返回结果与可疑覆盖。
// ErrorSource 一条返回槽的错误来源判定（todo12-8 R3）。

// EffectsResult query effects 输出契约。

// tx 调用名（扫描 AST 用）。

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
	// todo12-8 R2：写点提交范围（只陈述观察到的事实）
	setEffectCommitScope(res.DBWrites, &res.Tx)
	for i := range res.Paths {
		setEffectCommitScope(res.Paths[i].Writes, &res.Tx)
	}
	return res, nil
}

// effectWriteRow 函数字段摘要 → DBEffect（带被调文件/行）。
func (a *Actions) effectWriteRow(fid domain.CanonicalID, file string, r *domain.FunctionFieldSummary,
	colIndex tableColumnIndex) DBEffect {
	ev := &SeqEvidence{Source: SeqEvidenceAST, SymbolID: string(fid), File: file,
		Line: r.LineStart, Query: "codeintel query table " + fieldLeaf(r.FieldPath)}
	var table, column string
	kind := "field_write"
	if t, c, ok := colIndex.lookupExact(r.FieldPath); ok {
		// 只有完整 table.column 才证明是数据库写点（字段赋值不冒充）
		kind, table, column = "db_write", t, c
		ev.Verdict, ev.Reason = SeqVerdictCorroborated, "table-column-exact:"+t+"."+c
	} else {
		ev.Verdict = SeqVerdictUnverified
		ev.Reason = "no_table_mapping"
	}
	return DBEffect{Table: table, Column: column, Field: r.FieldPath, Function: string(fid),
		File: file, Line: r.LineStart, Access: string(r.AccessKind), Origin: "summary",
		Kind: kind, Evidence: ev}
}

// fileOfStrings 文件映射（canonical ID → file）转字符串键。

// unionPathWrites 路径写入并集（目标返回槽模式的函数级汇总）。

// targetResultSummary 目标模式的函数级返回汇总（路径返回槽并集，不重编号）。

// effectPartialCodes 判为 partial 的诊断代码。

// effectAnalysis 聚合完整性诊断（未知/截断 → partial）。

// setEffectCommitScope 标注每个写点是否落在观察到的 begin/commit 范围内
// （只陈述事实：不在范围内/无事务 → outside_or_unknown / none）。

// boolCount 条件计数（true = 1）。

// txCovers 写点是否落在 [begin, commit) 之间（事务范围判定的事实陈述）。

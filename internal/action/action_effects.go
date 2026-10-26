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
	"sort"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// EffectsRequest query effects 参数。
type EffectsRequest struct {
	Target  string // 函数/方法（canonical ID 或名称）
	RepoAbs string
	Depth   int // 展开层级（1 = 只本函数；默认 1）
	Scene   Scene
}

// DBEffect 一次表列写入。
type DBEffect struct {
	Table    string       `json:"table,omitempty"`
	Column   string       `json:"column,omitempty"`
	Field    string       `json:"field"`          // SSA 摘要的字段路径
	Function string       `json:"function"`       // 写入函数（canonical ID）
	Line     int          `json:"line,omitempty"` // 写入行
	Access   string       `json:"access"`         // direct_write / indirect_write
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
type ResultEffect struct {
	Returns    []string `json:"returns,omitempty"`    // 返回表达式文本
	Slots      []string `json:"slots,omitempty"`      // 结果槽
	Line       int      `json:"line,omitempty"`       // return 行
	ErrorVars  []string `json:"error_vars,omitempty"` // 被当作错误的变量
	Suspicious []string `json:"suspicious"`           // "错误被后续赋值覆盖"的可疑点（file:line；空数组也输出）
	Verdict    string   `json:"verdict"`
	Detail     string   `json:"detail,omitempty"`
}

// EffectsResult query effects 输出契约。
type EffectsResult struct {
	Target       string         `json:"target"`
	Function     string         `json:"function"`
	File         string         `json:"file,omitempty"`
	DBWrites     []DBEffect     `json:"db_writes"`
	RemoteWrites []RemoteEffect `json:"remote_writes"`
	Paths        []EffectPath   `json:"paths,omitempty"` // todo11-4：按执行路径
	Tx           TxScope        `json:"tx"`
	Result       ResultEffect   `json:"result"`
	Diagnostics  []SeqDiagCount `json:"diagnostics,omitempty"`
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
	if req.Depth > 1 {
		if facts, cerr := a.CalleesConcrete(n.ID, req.Depth-1); cerr == nil {
			seen := map[domain.CanonicalID]bool{n.ID: true}
			for _, f := range facts {
				if !seen[f.TargetID] {
					seen[f.TargetID] = true
					funcs = append(funcs, f.TargetID)
				}
			}
		}
	}

	// 1) db_writes：SSA 摘要 + 表列映射
	colIndex := a.tableColumnIndex()
	for _, fid := range funcs {
		rows, ferr := a.repo.GetFunctionFields(fid)
		if ferr != nil {
			continue
		}
		for _, r := range rows {
			if r.AccessKind != domain.SummaryDirectWrite && r.AccessKind != domain.SummaryIndirectWrite {
				continue
			}
			ev := &SeqEvidence{Source: SeqEvidenceAST, SymbolID: string(fid), File: n.FilePath,
				Query: "codeintel query table " + fieldLeaf(r.FieldPath)}
			table, column, ok := colIndex.lookup(fieldLeaf(r.FieldPath))
			if ok {
				ev.Verdict = SeqVerdictCorroborated
				ev.Reason = "field-to-column:" + fieldLeaf(r.FieldPath)
			} else {
				ev.Verdict = SeqVerdictUnverified
				ev.Reason = "no_table_mapping"
			}
			res.DBWrites = append(res.DBWrites, DBEffect{Table: table, Column: column,
				Field: r.FieldPath, Function: string(fid), Line: r.LineStart,
				Access: string(r.AccessKind), Evidence: ev})
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
	scan := a.scanEffectsAST(req.RepoAbs, n, req.Scene, nil)
	res.Tx = scan.tx
	res.Result = scan.result
	res.Paths = buildEffectPaths(scan.paths, colIndex, res.RemoteWrites, res.Tx, res.Result.Suspicious)
	// 事务是否覆盖写入行（只陈述"写点在不在范围内"）
	if res.Tx.Kind != "none" && len(res.DBWrites) > 0 {
		res.Tx.CoversWrites = txCovers(&res.Tx, res.DBWrites)
	}
	if len(res.DBWrites) == 0 && len(res.RemoteWrites) == 0 {
		res.Diagnostics = append(res.Diagnostics, SeqDiagCount{Code: "no_writes", Count: 1})
	}
	return res, nil
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

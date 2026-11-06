package action

// U04b：跨入口条件化读写连接（资源/表/全部 AND 等式键）。
//
// 只做**条件化匹配**：写入成功、影响记录、读取成功、请求顺序、记录唯一性、
// 存储值保持原样、没有中间覆写、参数传递与资源绑定都未证明；不把连接当成
// 真实请求已经发生。

import (
	"go/ast"
	"path/filepath"

	"github.com/schaepher/codeintel/internal/domain"
)

// 连接预算与证明代码。
const (
	seqWFMatchMax      = 4096
	seqWFConnectionMax = 256

	SeqProofWriteSuccess   = "write_success"
	SeqProofWriteAffected  = "write_record_affected"
	SeqProofReadSuccess    = "read_success"
	SeqProofRequestOrder   = "request_order"
	SeqProofRecordUnique   = "record_uniqueness"
	SeqProofStoredValue    = "stored_value_preserved"
	SeqProofNoIntervening  = "no_intervening_write"
	SeqProofParamPassing   = "parameter_passing"
	SeqProofResourceBind   = "resource_binding"
	SeqProofTransaction    = "transaction_commit"
	SeqProofJointReads     = "joint_read_values_preserved"
	SeqProofScanGuard      = "scan_success_guard"
	SeqProofInstanceBind   = "instance_parameter_binding"
	SeqProofInstanceSubset = "instance_read_substitution"
)

// SeqConnEndpoint 连接端点（入口身份 + 源码位置）。
type SeqConnEndpoint struct {
	Entry string `json:"entry"`
	Repo  string `json:"repo,omitempty"`
	File  string `json:"file,omitempty"`
	Line  int    `json:"line,omitempty"`
	// Path 入口 sequence 树内的实际节点路径（如 /Nodes/0）。
	Path string `json:"path,omitempty"`
}

// SeqConnValue 键值（来源身份 + 作用域）。
type SeqConnValue struct {
	Column string            `json:"column"`
	Scope  string            `json:"scope,omitempty"`
	Origin *SeqStorageOrigin `json:"origin,omitempty"`
}

// SeqConnKey 一条记录键（written → requested）。
type SeqConnKey struct {
	Written   SeqConnValue `json:"written"`
	Requested SeqConnValue `json:"requested"`
}

// SeqReadValue 写入列能提供的条件化读取值。
type SeqReadValue struct {
	Column      string            `json:"column"`
	Origin      *SeqStorageOrigin `json:"origin,omitempty"`
	SourceGuard string            `json:"source_guard,omitempty"`
	TargetGuard string            `json:"target_guard,omitempty"`
	ReadID      string            `json:"read_id,omitempty"`
	ScanID      string            `json:"scan_id,omitempty"`
}

// SeqWorkflowConnection 条件化连接。
type SeqWorkflowConnection struct {
	From       SeqConnEndpoint `json:"from"`
	To         SeqConnEndpoint `json:"to"`
	Keys       []SeqConnKey    `json:"keys,omitempty"`
	ReadValues []SeqReadValue  `json:"read_values,omitempty"`
	Verdict    string          `json:"verdict"`
	// B06b：该连接实际依赖的成功路径约束（如 commit_success）。
	SuccessConditions []string `json:"success_conditions,omitempty"`
	MissingProofs     []string `json:"missing_proofs,omitempty"`
	WriteID           string   `json:"write_id,omitempty"`
	ReadID            string   `json:"read_id,omitempty"`
	Table             string   `json:"table,omitempty"`
}

// seqWorkflowConnections 生成条件化连接（跨入口 + 同入口两次请求）。
func seqWorkflowConnections(entries []SeqWorkflowEntry, maxAttempts, maxConns int,
	facts ...*seqWorkflowFacts) ([]SeqWorkflowConnection, bool) {
	var fact *seqWorkflowFacts
	if len(facts) > 0 {
		fact = facts[0]
	}
	var out []SeqWorkflowConnection
	partial := false
	attempts := 0
	var writes []seqWFWrite
	var reads []seqWFRead
	for _, e := range entries {
		seqCollectWorkflowWrites(e.Sequence, e.Symbol, e.Repo, "", &writes)
		seqCollectWorkflowReads(e.Sequence, e.Symbol, e.Repo, "", &reads)
	}
	// Scan 目标映射按入口树补齐。
	for _, e := range entries {
		reads = seqAttachWFScanEntry(e.Sequence, reads)
	}
	// B06：事务写入的底层资源 = Begin 调用的 DB 声明。
	for i := range writes {
		if writes[i].Tx && writes[i].Resource == "" && writes[i].TxID != "" {
			writes[i].Resource = seqWFResolveTxResource(entries, writes[i].Entry, writes[i].TxID,
				writes[i].Repo)
		}
	}
	for _, w := range writes {
		for _, r := range reads {
			attempts++
			if attempts > maxAttempts {
				return out, true
			}
			if w.Table != r.Table {
				continue
			}
			// U04bR：资源声明被改写（binding_mutated）后不能再按同名匹配。
			if fact != nil {
				wExec := seqWFExecOffset(entries, w.Entry, w.Path)
				if wExec == 0 {
					wExec = w.Offset
				}
				rExec := seqWFExecOffset(entries, r.Entry, r.Path)
				if rExec == 0 {
					rExec = r.Offset
				}
				if fact.mutatedAt(w.Entry, w.Resource, wExec) ||
					fact.mutatedAt(r.Entry, r.Resource, rExec) {
					continue
				}
			}
			if len(r.Preds) == 0 {
				continue
			}
			// 资源必须同一声明；两侧都未证明时只做条件化匹配（资源绑定缺失）。
			if w.Resource != r.Resource {
				continue
			}
			conn, ok := seqBuildConnection(w, r)
			if !ok {
				continue
			}
			// B06b：源入口显式检查 Commit 错误 → 成功路径携带 commit_success。
			if fact != nil && w.Tx && fact.commitProven(entries, w) {
				conn.SuccessConditions = appendUniqueStr(conn.SuccessConditions, "commit_success")
				var proofs []string
				for _, p := range conn.MissingProofs {
					if p != SeqProofTransaction {
						proofs = append(proofs, p)
					}
				}
				conn.MissingProofs = proofs
			}
			if len(out) >= maxConns {
				return out, true
			}
			out = append(out, conn)
		}
	}
	return out, partial
}

// seqAttachWFScanEntry 遍历单个入口树补齐 Scan 目标映射。
func seqAttachWFScanEntry(tree *CodeSeqNode, reads []seqWFRead) []seqWFRead {
	if tree == nil {
		return reads
	}
	for _, n := range tree.Nodes {
		reads = seqAttachWFScanEntry(n, reads)
	}
	for _, n := range tree.Else {
		reads = seqAttachWFScanEntry(n, reads)
	}
	if tree.Evidence != nil && tree.Evidence.Scan != nil && tree.Evidence.Scan.Read != nil {
		sc := tree.Evidence.Scan
		for i := range reads {
			if reads[i].ReadID != sc.Read.ID {
				continue
			}
			cols := map[string]string{}
			for _, t := range sc.Targets {
				if t.Column != "" && t.Target != "" {
					cols[t.Column] = t.Target
				}
			}
			reads[i].Scans = append(reads[i].Scans, seqWFScan{ReadID: sc.Read.ID,
				ScanID: sc.ID, Columns: cols})
		}
	}
	return reads
}

// seqBuildConnection 单对写入→读取的条件化连接。
func seqBuildConnection(w seqWFWrite, r seqWFRead) (SeqWorkflowConnection, bool) {
	keys := make([]SeqConnKey, 0, len(r.Preds))
	keyCols := map[string]bool{}
	for _, p := range r.Preds {
		wv, ok := seqWFWriteColumn(w, p.Column)
		if !ok || !seqWFCompatible(wv, p.Value) {
			return SeqWorkflowConnection{}, false
		}
		if keyCols[p.Column] {
			continue
		}
		keyCols[p.Column] = true
		keys = append(keys, SeqConnKey{
			Written:   SeqConnValue{Column: p.Column, Scope: "source_request", Origin: wv.Origin},
			Requested: SeqConnValue{Column: p.Column, Scope: "target_request", Origin: p.Value.Origin},
		})
	}
	if len(keys) == 0 {
		return SeqWorkflowConnection{}, false
	}
	var vals []SeqReadValue
	for _, c := range r.Columns {
		if keyCols[c] {
			continue
		}
		wv, ok := seqWFWriteColumn(w, c)
		if !ok {
			continue
		}
		vals = append(vals, SeqReadValue{Column: c, Origin: wv.Origin,
			SourceGuard: w.Guard, TargetGuard: r.Guard, ReadID: r.ReadID})
	}
	proofs := []string{SeqProofWriteSuccess, SeqProofWriteAffected, SeqProofReadSuccess,
		SeqProofRequestOrder, SeqProofRecordUnique, SeqProofStoredValue,
		SeqProofNoIntervening, SeqProofParamPassing, SeqProofResourceBind}
	if w.Tx {
		proofs = append(proofs, SeqProofTransaction)
	}
	return SeqWorkflowConnection{
		From:          SeqConnEndpoint{Entry: w.Entry, Repo: w.Repo, File: w.File, Line: w.Line, Path: w.Path},
		To:            SeqConnEndpoint{Entry: r.Entry, Repo: r.Repo, File: r.File, Line: r.Line, Path: r.Path},
		Keys:          keys,
		ReadValues:    vals,
		Verdict:       "conditional",
		MissingProofs: proofs,
		WriteID:       w.ID,
		ReadID:        r.ReadID,
		Table:         w.Table,
	}, true
}

// seqWFResolveTxResource 事务 Begin 调用接收者的包级 DB 声明符号。
//
// 跨文件引用在单文件 parser 里没有 ast.Object——按**包级 var 名称表**回退
// （局部同名变量仍不命中，因为只有包级声明在表里）。
func seqWFResolveTxResource(entries []SeqWorkflowEntry, entry, txID, repo string) string {
	tree := seqWFEntryTree(entries, entry)
	if tree == nil {
		return ""
	}
	var pkgVars map[string]bool
	var found string
	var walk func(nodes []*CodeSeqNode)
	walk = func(nodes []*CodeSeqNode) {
		for _, n := range nodes {
			if found != "" || n == nil {
				continue
			}
			tx := n.Evidence
			if tx != nil && tx.Transaction != nil && tx.Transaction.Origin != nil &&
				tx.Transaction.Origin.ID == txID && tx.Transaction.Action == SeqTxBegin &&
				n.call != nil {
				if sel, ok := n.call.Fun.(*ast.SelectorExpr); ok {
					if id, ok2 := runtimeUnparen(sel.X).(*ast.Ident); ok2 {
						if id.Obj != nil {
							found = "symbol:go:" + pkgOfID(domain.CanonicalID(entry)) + ":" + id.Name
						} else {
							if pkgVars == nil {
								dir := filepath.Dir(filepath.Join(repo, tx.CallFile))
								pkgVars = seqPkgVarNames(seqParsePackageDir(dir))
							}
							if pkgVars[id.Name] {
								found = "symbol:go:" + pkgOfID(domain.CanonicalID(entry)) + ":" + id.Name
							}
						}
					}
				}
			}
			walk(n.Nodes)
			walk(n.Else)
		}
	}
	walk(tree.Nodes)
	return found
}

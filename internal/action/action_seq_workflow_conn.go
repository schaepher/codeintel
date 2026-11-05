package action

// U04b：跨入口条件化读写连接（资源/表/全部 AND 等式键）。
//
// 只做**条件化匹配**：写入成功、影响记录、读取成功、请求顺序、记录唯一性、
// 存储值保持原样、没有中间覆写、参数传递与资源绑定都未证明；不把连接当成
// 真实请求已经发生。

import (
	"go/ast"
	"strings"

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
	From          SeqConnEndpoint `json:"from"`
	To            SeqConnEndpoint `json:"to"`
	Keys          []SeqConnKey    `json:"keys,omitempty"`
	ReadValues    []SeqReadValue  `json:"read_values,omitempty"`
	Verdict       string          `json:"verdict"`
	MissingProofs []string        `json:"missing_proofs,omitempty"`
	WriteID       string          `json:"write_id,omitempty"`
	ReadID        string          `json:"read_id,omitempty"`
	Table         string          `json:"table,omitempty"`
}

// seqWFWrite 分析树里的可信写入。
type seqWFWrite struct {
	Entry    string
	Repo     string
	File     string
	Line     int
	Offset   int
	ID       string
	Table    string
	Op       string
	Guard    string
	Values   []SeqStorageWrite
	Preds    []SeqStoragePredicate
	Resource string
	Tx       bool
	TxID     string
}

// seqWFScan Scan 目标映射（read id + column → 变量/Scan 身份）。
type seqWFScan struct {
	ReadID  string
	ScanID  string
	Columns map[string]string
}

// seqWFRead 分析树里的可信读取。
type seqWFRead struct {
	Entry    string
	Repo     string
	File     string
	Line     int
	ReadID   string
	Table    string
	Columns  []string
	Preds    []SeqStoragePredicate
	Resource string
	Guard    string
	Scans    []seqWFScan
}

// seqWorkflowConnections 生成条件化连接（跨入口 + 同入口两次请求）。
func seqWorkflowConnections(entries []SeqWorkflowEntry, maxAttempts, maxConns int) ([]SeqWorkflowConnection, bool) {
	var out []SeqWorkflowConnection
	partial := false
	attempts := 0
	var writes []seqWFWrite
	var reads []seqWFRead
	for _, e := range entries {
		seqCollectWorkflowWrites(e.Sequence, e.Symbol, e.Repo, &writes)
		seqCollectWorkflowReads(e.Sequence, e.Symbol, e.Repo, &reads)
	}
	// Scan 目标映射按入口树补齐。
	for _, e := range entries {
		reads = seqAttachWFScanEntry(e.Sequence, reads)
	}
	// B06：事务写入的底层资源 = Begin 调用的 DB 声明。
	for i := range writes {
		if writes[i].Tx && writes[i].Resource == "" && writes[i].TxID != "" {
			writes[i].Resource = seqWFResolveTxResource(entries, writes[i].Entry, writes[i].TxID)
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
		From:          SeqConnEndpoint{Entry: w.Entry, Repo: w.Repo, File: w.File, Line: w.Line},
		To:            SeqConnEndpoint{Entry: r.Entry, Repo: r.Repo, File: r.File, Line: r.Line},
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
func seqWFResolveTxResource(entries []SeqWorkflowEntry, entry, txID string) string {
	tree := seqWFEntryTree(entries, entry)
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
					if id, ok2 := runtimeUnparen(sel.X).(*ast.Ident); ok2 && id.Obj != nil {
						found = "symbol:go:" + pkgOfID(domain.CanonicalID(entry)) + ":" + id.Name
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

// seqWFOriginKey 键来源的身份文本（用于实例分组去重）。
func seqWFOriginKey(o *SeqStorageOrigin) string {
	if o == nil {
		return ""
	}
	parts := []string{o.Kind}
	if o.Symbol != "" {
		parts = append(parts, o.Symbol)
	}
	if o.Parameter != nil {
		parts = append(parts, itoaSeq(*o.Parameter))
	}
	if o.Literal != "" {
		parts = append(parts, o.Literal)
	}
	for i := range o.Parts {
		parts = append(parts, seqWFOriginKey(&o.Parts[i]))
	}
	return strings.Join(parts, "|")
}

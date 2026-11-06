package action

// U04b：入口树里的可信写入/读取与 Scan 目标映射（从
// action_seq_workflow_conn.go 拆出）。

import "strconv"

// seqWFWrite 分析树里的可信写入。
type seqWFWrite struct {
	Entry    string
	Repo     string
	File     string
	Line     int
	Offset   int
	Path     string
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
	Path     string
	ReadID   string
	Table    string
	Columns  []string
	Preds    []SeqStoragePredicate
	Resource string
	Guard    string
	Scans    []seqWFScan
}

// seqCollectWorkflowWrites 收集写入（跳过候选/歧义节点）。
func seqCollectWorkflowWrites(tree *CodeSeqNode, entry, repo, path string, out *[]seqWFWrite) {
	if tree == nil {
		return
	}
	for i, n := range tree.Nodes {
		seqCollectWorkflowWrites(n, entry, repo, path+"/Nodes/"+itoaSeq(i), out)
	}
	for i, n := range tree.Else {
		seqCollectWorkflowWrites(n, entry, repo, path+"/Else/"+itoaSeq(i), out)
	}
	if tree.Candidate || tree.Evidence == nil || tree.Evidence.Ambiguous {
		return
	}
	st := tree.Evidence.Storage
	if st == nil {
		// B03：ORM 写入（xorm 风格模型/表列映射）。
		items := seqORMWritesFromEvidence(tree, entry, repo)
		for i := range items {
			items[i].Path = path
		}
		*out = append(*out, items...)
		return
	}
	if (st.Operation != SeqStorageInsert && st.Operation != SeqStorageUpdate) ||
		st.Table == "" || len(st.Writes) == 0 {
		return
	}
	id := ""
	if st.ErrorResult != nil {
		id = st.ErrorResult.CallID
	}
	if id == "" {
		id = "write@" + tree.Evidence.CallFile + ":" + strconv.Itoa(tree.Evidence.CallLine)
	}
	tx := false
	txID := ""
	if tree.Evidence.Transaction != nil && tree.Evidence.Transaction.Origin != nil {
		tx = true
		txID = tree.Evidence.Transaction.Origin.ID
	}
	*out = append(*out, seqWFWrite{Entry: entry, Repo: repo, File: tree.Evidence.CallFile,
		Line: tree.Evidence.CallLine, ID: id, Table: st.Table, Op: st.Operation,
		Guard: st.Guard, Values: st.Writes, Preds: st.Predicates, Path: path,
		Resource: st.ResourceDeclaration, Tx: tx, TxID: txID})
}

// seqCollectWorkflowReads 收集读取（需要读取结果身份 + 明确列 + AND 等式键）。
func seqCollectWorkflowReads(tree *CodeSeqNode, entry, repo, path string, out *[]seqWFRead) {
	if tree == nil {
		return
	}
	for i, n := range tree.Nodes {
		seqCollectWorkflowReads(n, entry, repo, path+"/Nodes/"+itoaSeq(i), out)
	}
	for i, n := range tree.Else {
		seqCollectWorkflowReads(n, entry, repo, path+"/Else/"+itoaSeq(i), out)
	}
	if tree.Candidate || tree.Evidence == nil || tree.Evidence.Ambiguous {
		return
	}
	rd := tree.Evidence.StorageRead
	if rd == nil {
		// B03：ORM 读取（Get/Find 的模型列 + Where 等式）。
		items := seqORMReadsFromEvidence(tree, entry, repo)
		for i := range items {
			items[i].Path = path
		}
		*out = append(*out, items...)
		return
	}
	if rd.Result == nil || rd.Table == "" || len(rd.Columns) == 0 ||
		len(rd.Predicates) == 0 {
		return
	}
	*out = append(*out, seqWFRead{Entry: entry, Repo: repo, File: tree.Evidence.CallFile,
		Line: tree.Evidence.CallLine, Path: path, ReadID: rd.Result.ID, Table: rd.Table,
		Columns: rd.Columns, Preds: rd.Predicates, Resource: rd.ResourceDeclaration,
		Guard: rd.Guard})
}

// seqAttachWFScan 把 Scan 目标映射到读取身份（同一读取可能有多次 Scan）。
func seqAttachWFScan(tree *CodeSeqNode, reads []seqWFRead) []seqWFRead {
	if tree == nil {
		return reads
	}
	for _, n := range tree.Nodes {
		reads = seqAttachWFScan(n, reads)
	}
	for _, n := range tree.Else {
		reads = seqAttachWFScan(n, reads)
	}
	if n := tree; n.Evidence != nil && n.Evidence.Scan != nil && n.Evidence.Scan.Read != nil {
		sc := n.Evidence.Scan
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

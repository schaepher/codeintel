package action

// U04f：条件化请求实例（从连接生成；不把实例数量当成请求已经发生）。
//
// 每个实例复制目标入口树并按已匹配的读取值标注分支（instance_condition）；
// 被条件排除/证据不足的后代不产生后续请求；预算/循环/缺身份时 partial。

// 实例预算。
const (
	seqWFInstanceMax   = 64
	seqWFInstanceDepth = 8
	seqWFInstanceNodes = 65536
)

// SeqInstanceCondition 分支在实例中的条件化判定。
type SeqInstanceCondition struct {
	Truth    string            `json:"truth"`
	Premises []string          `json:"premises,omitempty"`
	Left     *SeqStorageOrigin `json:"left,omitempty"`
	Right    *SeqStorageOrigin `json:"right,omitempty"`
	Conflict bool              `json:"fact_conflict,omitempty"`
	Reason   string            `json:"reason,omitempty"`
}

// SeqInstanceBinding 目标入口形参 ← 来源实例的键值来源。
type SeqInstanceBinding struct {
	Parameter int               `json:"parameter"`
	Origin    *SeqStorageOrigin `json:"origin,omitempty"`
	Source    string            `json:"source,omitempty"`
}

// SeqInstanceValue 实例写入的列值（origin 直接挂在列上，便于消费）。
type SeqInstanceValue struct {
	Column string            `json:"column"`
	Origin *SeqStorageOrigin `json:"origin,omitempty"`
	Kind   string            `json:"kind,omitempty"`
}

// SeqInstanceWrite 实例可达的写入。
type SeqInstanceWrite struct {
	WriteID   string             `json:"write_id,omitempty"`
	Entry     string             `json:"entry,omitempty"`
	File      string             `json:"file,omitempty"`
	Line      int                `json:"line,omitempty"`
	Table     string             `json:"table,omitempty"`
	Operation string             `json:"operation,omitempty"`
	Guard     string             `json:"guard,omitempty"`
	Values    []SeqInstanceValue `json:"values,omitempty"`
}

// SeqRelatedRead 同一实例里的额外读取（U04h）。
type SeqRelatedRead struct {
	ReadID          string         `json:"read_id"`
	ScanID          string         `json:"scan_id,omitempty"`
	WriteID         string         `json:"write_id,omitempty"`
	ConnectionIndex int            `json:"connection_index"`
	ReadValues      []SeqReadValue `json:"read_values,omitempty"`
}

// SeqWorkflowInstance 条件化请求实例。
type SeqWorkflowInstance struct {
	ID              string               `json:"id"`
	Kind            string               `json:"kind"`
	Verdict         string               `json:"verdict"`
	Entry           string               `json:"entry"`
	Repo            string               `json:"repo,omitempty"`
	ParentID        string               `json:"parent_id,omitempty"`
	ConnectionIndex int                  `json:"connection_index"`
	Ordering        string               `json:"ordering,omitempty"`
	Bindings        []SeqInstanceBinding `json:"bindings,omitempty"`
	ReadValues      []SeqReadValue       `json:"read_values,omitempty"`
	RelatedReads    []SeqRelatedRead     `json:"related_reads,omitempty"`
	Writes          []SeqInstanceWrite   `json:"writes,omitempty"`
	MissingProofs   []string             `json:"missing_proofs,omitempty"`
	Sequence        *CodeSeqNode         `json:"sequence,omitempty"`
}

// seqWFInstState 构建期状态（不序列化）。
type seqWFInstState struct {
	inst      *SeqWorkflowInstance
	params    []*SeqStorageOrigin // 目标入口形参来源（index 对齐）
	writes    []seqWFWrite
	guards    map[string]bool // scan id → 成功守卫成立
	ancestors map[string]bool
}

// seqCloneNodeTree 复制分析树（实例标注不修改原入口树）。
func seqCloneNodeTree(n *CodeSeqNode) *CodeSeqNode {
	if n == nil {
		return nil
	}
	cp := *n
	cp.Workflow = nil
	cp.Meta = nil
	cp.Nodes = nil
	cp.Else = nil
	for _, c := range n.Nodes {
		cp.Nodes = append(cp.Nodes, seqCloneNodeTree(c))
	}
	for _, c := range n.Else {
		cp.Else = append(cp.Else, seqCloneNodeTree(c))
	}
	return &cp
}

// seqWFEntryTree 入口 → 分析树。
func seqWFEntryTree(entries []SeqWorkflowEntry, symbol string) *CodeSeqNode {
	for _, e := range entries {
		if e.Symbol == symbol {
			return e.Sequence
		}
	}
	return nil
}

// seqWFScanGuards 目标入口树里 Scan 成功守卫（err!=nil 分支终止 → 成功路径）。
func seqWFScanGuards(tree *CodeSeqNode, out map[string]bool) {
	if tree == nil {
		return
	}
	for _, n := range tree.Nodes {
		seqWFScanGuards(n, out)
	}
	for _, n := range tree.Else {
		seqWFScanGuards(n, out)
	}
	if tree.Kind != "branch" || tree.Condition == nil || tree.Condition.Left == nil {
		return
	}
	left := tree.Condition.Left
	if left.Kind != SeqOriginScanError || left.ScanResult == nil {
		return
	}
	if seqWFContainsReturn(tree.Nodes) {
		out[left.ScanResult.ID] = true
	}
}

// seqWFContainsReturn 分支内是否终止（return/break）。
func seqWFContainsReturn(nodes []*CodeSeqNode) bool {
	for _, n := range nodes {
		if n == nil {
			continue
		}
		if n.Kind == "return" || n.Terminates {
			return true
		}
		if seqWFContainsReturn(n.Nodes) || seqWFContainsReturn(n.Else) {
			return true
		}
	}
	return false
}

// seqWFResolveOrigin 在来源实例作用域里解析形参来源（沿实例绑定递归）。
func seqWFResolveOrigin(st *seqWFInstState, o *SeqStorageOrigin) *SeqStorageOrigin {
	if o == nil {
		return nil
	}
	cp := cloneSeqScalarOrigin(o)
	switch cp.Kind {
	case SeqOriginParameter:
		if cp.Parameter != nil && cp.Symbol == st.inst.Entry {
			idx := *cp.Parameter
			if idx >= 0 && idx < len(st.params) && st.params[idx] != nil {
				return cloneSeqScalarOrigin(st.params[idx])
			}
		}
		return cp
	case SeqOriginConcat:
		for i := range cp.Parts {
			cp.Parts[i] = *seqWFResolveOrigin(st, &cp.Parts[i])
		}
	}
	return cp
}

// seqWFParamIndex 请求值是否是目标入口形参（返回零基下标）。
func seqWFParamIndex(o *SeqStorageOrigin, entry string) (int, bool) {
	if o == nil || o.Kind != SeqOriginParameter || o.Symbol != entry || o.Parameter == nil {
		return 0, false
	}
	return *o.Parameter, true
}

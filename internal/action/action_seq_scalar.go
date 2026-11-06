package action

// U04c：时序值环境里的**标量来源**（parameter/literal/concat）。
//
// 只描述源码来源身份，不声明运行时值（不证明写入成功/跨入口相等）。

// 标量来源类别。
const (
	SeqOriginParameter = "parameter"
	SeqOriginLiteral   = "literal"
	SeqOriginConcat    = "concat"
	// U04j：写入影响行数来源。
	SeqOriginWriteRowCount = "write_row_count"
	// U04m：标准库 Exec 的返回槽 0（sql.Result）。
	SeqOriginSQLResult = "sql_result"
	// U04n：事务对象与事务错误槽。
	SeqOriginTransaction = "transaction"
	SeqOriginTxError     = "tx_error"
	// U04v：请求结构体字段读取。
	SeqOriginFieldRead = "field_read"
	// U04d：标准 SQL 读取结果身份（QueryRow→Row / Query→Rows）。
	SeqOriginReadResult = "sql_read_result"
	// U04e：Scan 错误槽 / 列来源。
	SeqOriginScanError  = "scan_error"
	SeqOriginReadColumn = "read_column"
	// B05：数据库生成键（LastInsertId）。
	SeqOriginGeneratedKey = "generated_key"
)

// 标量来源树的边界（有界展开：超限即未知，不截取部分）。
const (
	seqScalarOriginMaxDepth = 8
	seqScalarOriginMaxBytes = 64 << 10
)

// SeqStorageOrigin 标量来源（storage value 的可选 origin）。
type SeqStorageOrigin struct {
	Kind       string `json:"kind"`
	ScalarType string `json:"scalar_type,omitempty"`
	// Symbol 入口符号 canonical ID（parameter 来源）。
	Symbol string `json:"symbol,omitempty"`
	// Parameter 入口的零基参数位置（0 必须输出，故用指针）。
	Parameter *int `json:"parameter,omitempty"`
	// Repo 入口仓库绝对路径（跨机比较时可投影掉，但身份字段必须保留）。
	Repo    string             `json:"repo,omitempty"`
	Literal string             `json:"literal,omitempty"`
	Parts   []SeqStorageOrigin `json:"parts,omitempty"`
	// RowCount U04j：该标量是某次写入的影响行数（kind=write_row_count）。
	RowCount *SeqORMRowCountResult `json:"row_count,omitempty"`
	// WriteError U04l：该返回槽携带写入错误身份（kind=write_error）。
	WriteError *SeqWriteErrorResult `json:"write_error,omitempty"`
	// SQLResult U04m：该标量是某次 Exec 的 sql.Result（kind=sql_result）。
	SQLResult *SeqSQLResult `json:"sql_result,omitempty"`
	// TxOrigin / TxError U04n：事务对象与事务操作的错误槽。
	TxOrigin *SeqTxOrigin   `json:"tx_origin,omitempty"`
	TxError  *SeqTxOpResult `json:"tx_error,omitempty"`
	// FieldRead U04v：请求结构体字段读取（独立读取 ID + 声明/读取位置）。
	FieldRead *SeqStorageFieldRead `json:"field_read,omitempty"`
	// ReadResult U04d：SQL 读取结果身份（read-N；槽 0）。
	ReadResult *SeqSQLReadResult `json:"read_result,omitempty"`
	// U04e：Scan 错误槽 / 列来源。
	ScanResult *SeqScan       `json:"scan_result,omitempty"`
	ReadColumn *SeqReadColumn `json:"read_column,omitempty"`
	// U04k：new(T) 分配身份（指针模型；别名共享同一 ID）。
	Allocation *SeqAllocation `json:"allocation,omitempty"`
	// B05：生成键来源。
	GeneratedKey *SeqGeneratedKey `json:"generated_key,omitempty"`
	// snapshot U04l：该来源在求值时刻就已经固定（getter 返回槽），续查后的
	// 后序重算不得用后来的字段状态覆盖它。
	snapshot bool
	// StructLiteral U04w：结构体字面量初始化来源（initialization_only）。
	StructLiteral *SeqStorageStructLiteral `json:"struct_literal,omitempty"`
	// U04x：整值复制的构造祖先（construction_ancestry_only）。
	StructCopy *SeqStorageStructCopy `json:"struct_copy,omitempty"`
}

// cloneSeqScalarOrigin 深拷贝（消费者不得改到请求内的共享来源）。
func cloneSeqScalarOrigin(o *SeqStorageOrigin) *SeqStorageOrigin {
	if o == nil {
		return nil
	}
	out := *o
	if o.Parameter != nil {
		v := *o.Parameter
		out.Parameter = &v
	}
	if len(o.Parts) > 0 {
		out.Parts = make([]SeqStorageOrigin, 0, len(o.Parts))
		for i := range o.Parts {
			out.Parts = append(out.Parts, *cloneSeqScalarOrigin(&o.Parts[i]))
		}
	}
	if o.TxOrigin != nil {
		cp := *o.TxOrigin
		out.TxOrigin = &cp
	}
	if o.TxError != nil {
		cp := *o.TxError
		out.TxError = &cp
	}
	if o.ReadResult != nil {
		cp := *o.ReadResult
		cp.columns = append([]string{}, o.ReadResult.columns...)
		cp.fields = append([]string{}, o.ReadResult.fields...)
		out.ReadResult = &cp
	}
	if o.ScanResult != nil {
		cp := *o.ScanResult
		out.ScanResult = &cp
	}
	if o.ReadColumn != nil {
		cp := *o.ReadColumn
		out.ReadColumn = &cp
	}
	if o.Allocation != nil {
		cp := *o.Allocation
		out.Allocation = &cp
	}
	if o.GeneratedKey != nil {
		cp := *o.GeneratedKey
		if o.GeneratedKey.Access != nil {
			ac := *o.GeneratedKey.Access
			cp.Access = &ac
		}
		out.GeneratedKey = &cp
	}
	if o.FieldRead != nil {
		cp := *o.FieldRead
		cp.Root = cloneSeqFieldRoot(o.FieldRead.Root)
		cp.Path = append([]string{}, o.FieldRead.Path...)
		out.FieldRead = &cp
	}
	if o.StructCopy != nil {
		cp := *o.StructCopy
		cp.Source = cloneSeqScalarOrigin(o.StructCopy.Source)
		out.StructCopy = &cp
	}
	if o.StructLiteral != nil {
		cp := *o.StructLiteral
		cp.Fields = nil
		for i := range o.StructLiteral.Fields {
			f := o.StructLiteral.Fields[i]
			cp.Fields = append(cp.Fields, SeqStructLiteralField{Name: f.Name,
				Origin: cloneSeqScalarOrigin(f.Origin)})
		}
		out.StructLiteral = &cp
	}
	return &out
}

// sameSeqScalarOrigin 两个来源是否同一（分支合并用）。
func sameSeqScalarOrigin(a, b *SeqStorageOrigin) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if a.Kind != b.Kind || a.ScalarType != b.ScalarType || a.Symbol != b.Symbol ||
		a.Repo != b.Repo || a.Literal != b.Literal || len(a.Parts) != len(b.Parts) {
		return false
	}
	if (a.WriteError == nil) != (b.WriteError == nil) {
		return false
	}
	if a.WriteError != nil && (a.WriteError.CallID != b.WriteError.CallID ||
		a.WriteError.Slot != b.WriteError.Slot || a.WriteError.Symbol != b.WriteError.Symbol) {
		return false
	}
	if (a.RowCount == nil) != (b.RowCount == nil) {
		return false
	}
	if a.RowCount != nil && !sameSeqRowCount(a.RowCount, b.RowCount) {
		return false
	}
	if (a.SQLResult == nil) != (b.SQLResult == nil) {
		return false
	}
	if a.SQLResult != nil && (a.SQLResult.Slot != b.SQLResult.Slot ||
		!sameSeqWriteError(a.SQLResult.Write, b.SQLResult.Write)) {
		return false
	}
	if (a.TxOrigin == nil) != (b.TxOrigin == nil) || (a.TxError == nil) != (b.TxError == nil) {
		return false
	}
	if a.TxOrigin != nil && (a.TxOrigin.ID != b.TxOrigin.ID || a.TxOrigin.Symbol != b.TxOrigin.Symbol) {
		return false
	}
	if a.TxError != nil && (a.TxError.CallID != b.TxError.CallID || a.TxError.Operation != b.TxError.Operation ||
		a.TxError.Slot != b.TxError.Slot) {
		return false
	}
	if (a.ReadResult == nil) != (b.ReadResult == nil) {
		return false
	}
	if a.ReadResult != nil && (a.ReadResult.ID != b.ReadResult.ID ||
		a.ReadResult.Symbol != b.ReadResult.Symbol || a.ReadResult.Slot != b.ReadResult.Slot) {
		return false
	}
	if (a.ScanResult == nil) != (b.ScanResult == nil) {
		return false
	}
	if a.ScanResult != nil && a.ScanResult.ID != b.ScanResult.ID {
		return false
	}
	if (a.GeneratedKey == nil) != (b.GeneratedKey == nil) {
		return false
	}
	if a.GeneratedKey != nil && (a.GeneratedKey.CallID != b.GeneratedKey.CallID ||
		a.GeneratedKey.Slot != b.GeneratedKey.Slot) {
		return false
	}
	if (a.Allocation == nil) != (b.Allocation == nil) {
		return false
	}
	if a.Allocation != nil && (a.Allocation.ID != b.Allocation.ID ||
		a.Allocation.Entry != b.Allocation.Entry || a.Allocation.Repo != b.Allocation.Repo) {
		return false
	}
	if (a.ReadColumn == nil) != (b.ReadColumn == nil) {
		return false
	}
	if a.ReadColumn != nil && (a.ReadColumn.Result == nil || b.ReadColumn.Result == nil ||
		a.ReadColumn.Result.ID != b.ReadColumn.Result.ID ||
		a.ReadColumn.Column != b.ReadColumn.Column) {
		return false
	}
	if (a.FieldRead == nil) != (b.FieldRead == nil) {
		return false
	}
	if a.FieldRead != nil && a.FieldRead.ID != b.FieldRead.ID {
		return false
	}
	if (a.StructLiteral == nil) != (b.StructLiteral == nil) {
		return false
	}
	if a.StructLiteral != nil && a.StructLiteral.ID != b.StructLiteral.ID {
		return false
	}
	if (a.StructCopy == nil) != (b.StructCopy == nil) {
		return false
	}
	if a.StructCopy != nil && a.StructCopy.ID != b.StructCopy.ID {
		return false
	}
	pa, pb := 0, 0
	if a.Parameter != nil {
		pa = *a.Parameter
	}
	if b.Parameter != nil {
		pb = *b.Parameter
	}
	if pa != pb {
		return false
	}
	for i := range a.Parts {
		if !sameSeqScalarOrigin(&a.Parts[i], &b.Parts[i]) {
			return false
		}
	}
	return true
}

// seqOriginParameter 入口参数来源。
func seqOriginParameter(symbol, repo, scalarType string, idx int) *SeqStorageOrigin {
	p := idx
	return &SeqStorageOrigin{Kind: SeqOriginParameter, ScalarType: scalarType,
		Symbol: symbol, Parameter: &p, Repo: repo}
}

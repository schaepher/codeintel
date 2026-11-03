package action

// U04b：SQL 写入证据的数据结构（JSON 契约见 docs/json-contract.md）。

// SeqStorage 写入证据（SeqEvidence.Storage）。
type SeqStorage struct {
	Operation string `json:"operation,omitempty"`
	Table     string `json:"table,omitempty"`
	// Writes SET/列赋值（按 SQL 书写顺序）。
	Writes []SeqStorageWrite `json:"writes,omitempty"`
	// Predicates WHERE 的 AND 等式子集（按书写顺序）。
	Predicates []SeqStoragePredicate `json:"predicates,omitempty"`
	// ResourceDeclaration 只能是**准确的包级** `var X *sql.DB` 声明符号；
	// 局部同名/已初始化/其它类型一律省略。
	ResourceDeclaration string `json:"resource_declaration,omitempty"`
	Guard               string `json:"guard,omitempty"`
	Outcome             string `json:"outcome"`
	Boundary            string `json:"boundary,omitempty"`
	// Resource U04d：写入资源绑定（有快照才输出）。
	Resource *SeqStorageResource `json:"resource,omitempty"`
	// ErrorResult U04h：该写入的错误返回槽（slot=1；call_id 请求内唯一）。
	ErrorResult *SeqWriteErrorResult `json:"error_result,omitempty"`
}

// SeqStorageWrite 单列写入。
type SeqStorageWrite struct {
	Column string          `json:"column"`
	Value  SeqStorageValue `json:"value"`
}

// SeqStoragePredicate WHERE 等式的列与值。
type SeqStoragePredicate struct {
	Column string          `json:"column"`
	Value  SeqStorageValue `json:"value"`
}

// SeqStorageValue 绑定到列的实参或 SQL 字面量。
type SeqStorageValue struct {
	Kind string `json:"kind"`
	// Origin U04c：标量来源（parameter/literal/concat；未知时省略）。
	Origin *SeqStorageOrigin `json:"origin,omitempty"`
	// Argument 零基索引（不含 context/SQL）；SQL 字面量为 -1。
	Argument int `json:"argument"`
	// Expression 实参在**调用处**的 Go 表达式文本（argument）。
	Expression string `json:"expression,omitempty"`
	// SQL 字面量原文（literal）。
	SQL string `json:"sql,omitempty"`
}

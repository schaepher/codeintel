package action

// U04f：XORM 证据的常量与数据结构。

// XORM 框架包身份（只认这些包里的 *Session 方法）。
var seqXormPkgPaths = map[string]bool{
	"xorm.io/xorm":             true,
	"github.com/go-xorm/xorm":  true,
	"github.com/xormplus/xorm": true,
}

// 终端操作。
var seqXormOps = map[string]string{
	"Insert": SeqStorageInsert,
	"Update": SeqStorageUpdate,
	"Delete": "delete",
	"Find":   "select",
	"Get":    "select",
}

// 构建器方法（显式链；未列入的builder → 表身份未证明）。
var seqXormBuilders = map[string]bool{
	"Table": true, "Where": true, "And": true, "Or": true, "Context": true,
	"Alias": true, "In": true,
}

// 边界代码。
const (
	SeqORMStateUnproven   = "orm_state_unproven"
	SeqORMTableUnproven   = "table_identity_unproven"
	SeqORMCoveragePartial = "partial"
)

// SeqORM XORM 操作证据（SeqEvidence.ORM）。
type SeqORM struct {
	Framework string `json:"framework"`
	Operation string `json:"operation,omitempty"`
	Table     string `json:"table,omitempty"`
	// TableExpression 动态表名表达式原文（未证明为表身份）。
	TableExpression string `json:"table_expression,omitempty"`
	// Arguments 终端调用的实参（表达式 + 已有标量来源）。
	Arguments []SeqStorageValue `json:"arguments,omitempty"`
	// Predicates Where 的 AND 等式子集（覆盖率 partial）。
	Predicates        []SeqStoragePredicate `json:"predicates,omitempty"`
	PredicateCoverage string                `json:"predicate_coverage,omitempty"`
	Guard             string                `json:"guard,omitempty"`
	Outcome           string                `json:"outcome"`
	Boundary          string                `json:"boundary,omitempty"`
}

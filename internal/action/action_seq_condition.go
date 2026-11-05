package action

// U04c：if 分支条件的**结构化来源**（比较运算符 + 左右操作数的
// SeqStorageOrigin；逻辑子表达式按源码嵌套保留）。
//
// 边界（刻意保守）：
//   - 只描述来源身份，不改变 Scene 判定，也不把条件当成已经观察到的运行
//     时值；
//   - 未知操作数省略对应来源并记 unknown=operand_origin_unproven，另一侧仍
//     保留；
//   - 超过条件节点/递归上限时**整项**返回 operator=unknown +
//     unknown=condition_budget（不保留部分逻辑表达式再据此判断分支）；
//   - 来源是副本，调用方修改输出不会污染分析环境。

import (
	"go/ast"
	"go/token"
)

// 条件结构上限（U04c）。
const (
	seqConditionMaxNodes = 64
	seqConditionMaxDepth = 16
)

// 条件未知原因。
const (
	SeqConditionUnknownOperand = "operand_origin_unproven"
	SeqConditionUnknownBudget  = "condition_budget"
)

// 逻辑/布尔运算符（非 Go token 的比较运算符）。
const (
	SeqConditionOpAnd  = "&&"
	SeqConditionOpOr   = "||"
	SeqConditionOpNot  = "!"
	SeqConditionOpBool = "bool"
	SeqConditionOpNone = "unknown"
)

// SeqCondition 一个条件子表达式（比较/逻辑/布尔）。
type SeqCondition struct {
	Operator string `json:"operator"`
	// Left/Right 比较操作数来源（未知时省略对应项）。
	Left  *SeqStorageOrigin `json:"left,omitempty"`
	Right *SeqStorageOrigin `json:"right,omitempty"`
	// Terms 逻辑子表达式（&&/|| 按源码顺序；! 只有一个元素）。
	Terms []SeqCondition `json:"terms,omitempty"`
	// Unknown 来源未知原因（operand_origin_unproven / condition_budget）。
	Unknown string `json:"unknown,omitempty"`
	// Expression 源码文本（审计用；不参与判断）。
	Expression string `json:"expression,omitempty"`
}

// seqCondition 从 if 条件表达式构建结构化来源；nil 表示无可输出内容。
func seqCondition(req CodeSequenceRequest, fset *token.FileSet, src []byte, cond ast.Expr) *SeqCondition {
	if cond == nil {
		return nil
	}
	b := &seqCondBuilder{}
	out := b.build(req, cond, 0)
	if b.overflow {
		// 预算耗尽：整项未知，不保留部分逻辑表达式。
		return &SeqCondition{Operator: SeqConditionOpNone, Unknown: SeqConditionUnknownBudget}
	}
	if out == nil {
		return nil
	}
	if fset != nil {
		out.Expression = seqExprText(fset, src, cond)
	}
	return out
}

// seqCondBuilder 条件构建的有界计数（整项共享）。
type seqCondBuilder struct {
	nodes    int
	overflow bool
}

func (b *seqCondBuilder) build(req CodeSequenceRequest, e ast.Expr, depth int) *SeqCondition {
	if e == nil || b.overflow {
		return nil
	}
	if depth > seqConditionMaxDepth {
		b.overflow = true
		return nil
	}
	b.nodes++
	if b.nodes > seqConditionMaxNodes {
		b.overflow = true
		return nil
	}
	switch v := e.(type) {
	case *ast.ParenExpr:
		// 括号不改变来源结构；不计节点（只有真正子表达式才占预算）。
		b.nodes--
		return b.build(req, v.X, depth+1)
	case *ast.UnaryExpr:
		if v.Op == token.NOT {
			inner := b.build(req, v.X, depth+1)
			if inner == nil {
				return &SeqCondition{Operator: SeqConditionOpNot}
			}
			return &SeqCondition{Operator: SeqConditionOpNot, Terms: []SeqCondition{*inner}}
		}
		// 其它一元运算（-x/^x）不是条件来源；保守返回未知操作数。
		return b.unknownOperand(req, e)
	case *ast.BinaryExpr:
		switch v.Op {
		case token.LAND, token.LOR:
			op := SeqConditionOpAnd
			if v.Op == token.LOR {
				op = SeqConditionOpOr
			}
			left := b.build(req, v.X, depth+1)
			right := b.build(req, v.Y, depth+1)
			var terms []SeqCondition
			if left != nil {
				terms = append(terms, *left)
			}
			if right != nil {
				terms = append(terms, *right)
			}
			return &SeqCondition{Operator: op, Terms: terms}
		case token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ:
			c := &SeqCondition{Operator: v.Op.String()}
			c.Left = seqConditionOperand(req, v.X)
			c.Right = seqConditionOperand(req, v.Y)
			if c.Left == nil || c.Right == nil {
				c.Unknown = SeqConditionUnknownOperand
			}
			return c
		}
		return b.unknownOperand(req, e)
	}
	// 裸布尔表达式（true/false/已知 bool 来源）。
	if o := seqScalarOriginOf(req, e); o != nil {
		return &SeqCondition{Operator: SeqConditionOpBool, Left: o}
	}
	return b.unknownOperand(req, e)
}

// unknownOperand 无法结构化的条件（调用表达式等）：保留未知原因，不猜。
func (b *seqCondBuilder) unknownOperand(req CodeSequenceRequest, e ast.Expr) *SeqCondition {
	c := &SeqCondition{Operator: SeqConditionOpBool, Unknown: SeqConditionUnknownOperand}
	if o := seqScalarOriginOf(req, e); o != nil {
		c.Left = o
	}
	return c
}

// seqConditionOperand 比较操作数的来源（未知 → nil）。
func seqConditionOperand(req CodeSequenceRequest, e ast.Expr) *SeqStorageOrigin {
	if e == nil {
		return nil
	}
	// U04e：预声明 nil 作为条件操作数（`err != nil`）——只描述来源类别，
	// 不进入标量环境（不影响"覆盖为 nil 清除来源"的规则）。
	if seqIsNilIdent(e) {
		return &SeqStorageOrigin{Kind: SeqOriginNil}
	}
	return seqScalarOriginOf(req, e)
}

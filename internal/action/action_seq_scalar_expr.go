package action

// U04c/U04d/U04e：表达式 → 标量来源（参数/字面量/字段读取/读取结果/Scan/
// 拼接/构造）。未知一律 nil（不猜）；深度有界（超限即未知）。

import (
	"go/ast"
	"go/token"
)

// seqScalarOriginOf 表达式的标量来源（未知 → nil，不猜）。
func seqScalarOriginOf(req CodeSequenceRequest, e ast.Expr) *SeqStorageOrigin {
	return seqScalarOriginDepth(req, e, 0)
}

func seqScalarOriginDepth(req CodeSequenceRequest, e ast.Expr, depth int) *SeqStorageOrigin {
	if e == nil || depth > seqScalarOriginMaxDepth {
		return nil
	}
	switch v := e.(type) {
	case *ast.ParenExpr:
		return seqScalarOriginDepth(req, v.X, depth+1)
	case *ast.Ident:
		// B06e.1：事务变量/别名（`alias := tx`）——事务来源在 env.txs 槽里，
		// 优先按它给出（重新赋值为非事务来源时槽已被 clearTx 清掉）。
		if tx := seqTxVarOrigin(req, v); tx != nil {
			return &SeqStorageOrigin{Kind: SeqOriginTransaction, TxOrigin: tx}
		}
		if req.env != nil {
			if origin, ok := req.env.scalarOriginAt(v.Obj, v.Pos()); ok {
				return cloneSeqScalarOrigin(origin)
			}
		}
		// 未遮蔽的预声明 true/false 是字面量；被局部/包级变量遮蔽时 Obj
		// 非 nil（不借内建名恢复假常量）。
		if v.Obj == nil && (v.Name == "true" || v.Name == "false") {
			return &SeqStorageOrigin{Kind: SeqOriginLiteral, ScalarType: "bool", Literal: v.Name}
		}
		return nil
	case *ast.BasicLit:
		return seqBasicLitOrigin(v)
	case *ast.SelectorExpr:
		// U04j/U04k：字段槽（Scan 得到的列值，按对象/分配 + 字段隔离）。
		// 分配根已由字段槽跟踪：无槽即显式未知（不回退到旧来源）。
		if o, tracked := seqFieldSlotLookupSelector(req, v); tracked {
			return o
		}
		// U04v：`req.ID` / `req.Nested.ID` → 字段读取来源（独立读取 ID）。
		return seqFieldReadOrigin(req, v)
	case *ast.CallExpr:
		// U04k：已证明的 `new(T)` 分配身份。
		if o := seqNewAllocOrigin(req, v, depth); o != nil {
			return o
		}
		// B05：已证明的 LastInsertId → 生成键来源。
		if o := seqGeneratedKeyOriginAt(req, v); o != nil {
			return o
		}
		// U04e：已证明的 Scan 调用 → 错误槽（Scan 只返回 error）。
		if o := seqScanErrorOriginAt(req, v); o != nil {
			return o
		}
		// B03e：已证明的 ORM 读取错误槽（Find 槽 0 / Get 槽 1）。
		if o := seqORMReadSlotOriginAt(req, v, 0); o != nil {
			return o
		}
		// U04d：已证明的标准 SQL 读取调用 → 结果身份（槽 0）。
		if o := seqReadResultOriginAt(req, v); o != nil {
			return o
		}
		// U04v：getter 调用（单一 return：字面量 / receiver 字段）。
		if o := seqGetterOrigin(req, v); o != nil {
			return o
		}
		// U04y：内建类型转换（`int64(id)`）只改标量类型，不丢失实参来源；
		// 同名局部/包级函数（Obj 非 nil）不按内建解析。
		if id, ok := v.Fun.(*ast.Ident); ok && id.Obj == nil && seqBuiltinTypeName(id.Name) &&
			len(v.Args) == 1 && !v.Ellipsis.IsValid() {
			if o := seqScalarOriginDepth(req, v.Args[0], depth+1); o != nil {
				cp := cloneSeqScalarOrigin(o)
				cp.ScalarType = id.Name
				return cp
			}
			return nil
		}
		// U04w：实参位置的调用（`Session.Insert(Build(id))`）——按已展开的
		// 返回槽来源传播（不重跑函数体）。
		return seqNodeResultOriginAt(req, v, 0)
	case *ast.UnaryExpr:
		// U04w：`&T{...}` 与 `T{...}` 同义（指针字面量）。
		if v.Op == token.AND {
			if lit, ok := runtimeUnparen(v.X).(*ast.CompositeLit); ok {
				return seqStructLiteralOrigin(req, lit, depth)
			}
			// B03b：`&record`（record 是构造字面量/分配）沿变量来源传递，
			// 供 ORM Update 模型值使用（普通标量取址仍不继承来源）。
			if id, ok := runtimeUnparen(v.X).(*ast.Ident); ok {
				if o := seqScalarOriginAtAddr(req, id, v.Pos()); o != nil {
					switch o.Kind {
					case SeqOriginStructLiteral, SeqOriginStructCopy, SeqOriginAllocation:
						return o
					}
				}
			}
		}
		return nil
	case *ast.CompositeLit:
		// U04w：`T{...}` 构造来源（initialization_only）。
		return seqStructLiteralOrigin(req, v, depth)
	case *ast.BinaryExpr:
		if v.Op != token.ADD {
			return nil
		}
		left := seqScalarOriginDepth(req, v.X, depth+1)
		right := seqScalarOriginDepth(req, v.Y, depth+1)
		return seqConcatOrigin(left, right)
	}
	return nil
}

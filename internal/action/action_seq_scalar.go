package action

// U04c：时序值环境里的**标量来源**（parameter/literal/concat）。
//
// 只描述源码来源身份，不声明运行时值（不证明写入成功/跨入口相等）。

import (
	"go/ast"
	"go/token"
	"strconv"
	"strings"
)

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

// seqBasicLitOrigin 字面量来源（Go 语法规范化：字符串解码后重新加引号）。
func seqBasicLitOrigin(lit *ast.BasicLit) *SeqStorageOrigin {
	if lit == nil {
		return nil
	}
	switch lit.Kind {
	case token.STRING:
		s, err := strconv.Unquote(lit.Value)
		if err != nil {
			return nil
		}
		return &SeqStorageOrigin{Kind: SeqOriginLiteral, ScalarType: "string", Literal: strconv.Quote(s)}
	case token.CHAR:
		r, _, _, err := strconv.UnquoteChar(strings.Trim(lit.Value, "'"), '\'')
		if err != nil {
			return nil
		}
		return &SeqStorageOrigin{Kind: SeqOriginLiteral, ScalarType: "rune", Literal: strconv.QuoteRune(r)}
	case token.INT:
		v, err := strconv.ParseInt(lit.Value, 0, 64)
		if err != nil {
			if u, uerr := strconv.ParseUint(lit.Value, 0, 64); uerr == nil {
				return &SeqStorageOrigin{Kind: SeqOriginLiteral, ScalarType: "uint64", Literal: strconv.FormatUint(u, 10)}
			}
			return nil
		}
		return &SeqStorageOrigin{Kind: SeqOriginLiteral, ScalarType: "int", Literal: strconv.FormatInt(v, 10)}
	case token.FLOAT:
		f, err := strconv.ParseFloat(lit.Value, 64)
		if err != nil {
			return nil
		}
		return &SeqStorageOrigin{Kind: SeqOriginLiteral, ScalarType: "float64", Literal: strconv.FormatFloat(f, 'g', -1, 64)}
	}
	return nil
}

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
		// U04v：`req.ID` / `req.Nested.ID` → 字段读取来源（独立读取 ID）。
		return seqFieldReadOrigin(req, v)
	case *ast.CallExpr:
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

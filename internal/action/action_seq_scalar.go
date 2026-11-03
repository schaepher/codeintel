package action

// U04c：时序值环境里的**标量来源**（parameter/literal/concat）。
//
// 只描述源码中的来源身份，不声明运行时值：origin 不证明写入成功，也不
// 证明另一个入口的参数与本入口相等。

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

// seqConcatOrigin 字符串拼接来源（两侧都必须已证明为 string 来源）。
func seqConcatOrigin(left, right *SeqStorageOrigin) *SeqStorageOrigin {
	if !seqOriginIsString(left) || !seqOriginIsString(right) {
		return nil
	}
	parts := make([]SeqStorageOrigin, 0, 2)
	appendParts := func(o *SeqStorageOrigin) {
		if o.Kind == SeqOriginConcat {
			parts = append(parts, o.Parts...)
			return
		}
		parts = append(parts, *cloneSeqScalarOrigin(o))
	}
	appendParts(left)
	appendParts(right)
	if len(parts) > 32 || seqOriginTotalBytes(parts) > seqScalarOriginMaxBytes {
		return nil
	}
	return &SeqStorageOrigin{Kind: SeqOriginConcat, ScalarType: "string", Parts: parts}
}

func seqOriginIsString(o *SeqStorageOrigin) bool {
	if o == nil || o.ScalarType != "string" {
		return false
	}
	// 只有 parameter/literal(string)/concat 的字符串来源可继续拼接。
	switch o.Kind {
	case SeqOriginParameter, SeqOriginConcat:
		return true
	case SeqOriginLiteral:
		return true
	}
	return false
}

func seqOriginTotalBytes(parts []SeqStorageOrigin) int {
	total := 0
	for i := range parts {
		total += len(parts[i].Literal)
		for j := range parts[i].Parts {
			total += len(parts[i].Parts[j].Literal)
		}
	}
	return total
}

// captureSeqArgOrigins 调用点每个实参的标量来源（在实参求值位置立即
// 捕获：后续赋值/改写不影响已捕获的来源）。
func captureSeqArgOrigins(req CodeSequenceRequest, call *ast.CallExpr) []*SeqStorageOrigin {
	if call == nil {
		return nil
	}
	out := make([]*SeqStorageOrigin, len(call.Args))
	for i, arg := range call.Args {
		if call.Ellipsis.IsValid() && i == len(call.Args)-1 {
			// `f(xs...)` 的实参是集合，标量来源未证明。
			out[i] = nil
			continue
		}
		out[i] = seqScalarOriginOf(req, arg)
	}
	return out
}

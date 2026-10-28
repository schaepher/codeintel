package action

// todo25 §13.2：SQL 读取点 → 快照 observation 的匹配与值计算。
// 只支持结构化 SELECT（表/列/WHERE 的 eq/in）；JOIN/聚合/动态 SQL 保持
// unknown（不按表名字符串猜）。

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"vitess.io/vitess/go/vt/sqlparser"
)

var runtimeSQLParser = mustRuntimeSQLParser()

func mustRuntimeSQLParser() *sqlparser.Parser {
	p, err := sqlparser.New(sqlparser.Options{})
	if err != nil {
		panic(err)
	}
	return p
}

type runtimeQuery struct {
	SourceID   string
	Table      string
	Columns    []string
	Predicates []runtimePredicate
}

// parseRuntimeSelect 解析 SELECT（占位符按出现顺序绑定 args）。
func parseRuntimeSelect(sql string, args []rtValue) (*runtimeQuery, error) {
	stmt, err := runtimeSQLParser.Parse(sql)
	if err != nil {
		return nil, fmt.Errorf("SQL 解析失败: %w", err)
	}
	sel, ok := stmt.(*sqlparser.Select)
	if !ok {
		return nil, errors.New("只支持 SELECT")
	}
	if sel.With != nil || len(sel.From) != 1 {
		return nil, errors.New("不支持 CTE/JOIN/多表")
	}
	ate, ok := sel.From[0].(*sqlparser.AliasedTableExpr)
	if !ok {
		return nil, errors.New("不支持派生表/子查询")
	}
	tn, ok := ate.Expr.(sqlparser.TableName)
	if !ok || tn.Name.String() == "" || strings.EqualFold(tn.Name.String(), "dual") {
		return nil, errors.New("无法确定真实表")
	}
	if sel.GroupBy != nil || sel.Having != nil || sel.OrderBy != nil || sel.Limit != nil {
		return nil, errors.New("不支持 GROUP/HAVING/ORDER/LIMIT")
	}
	var cols []string
	for _, e := range sel.SelectExprs.Exprs {
		ae, ok := e.(*sqlparser.AliasedExpr)
		if !ok {
			return nil, errors.New("SELECT * 或表达式列不支持")
		}
		cn, ok := ae.Expr.(*sqlparser.ColName)
		if !ok {
			return nil, errors.New("SELECT 列不是普通列")
		}
		cols = append(cols, cn.Name.String())
	}
	if len(cols) == 0 {
		return nil, errors.New("SELECT 没有可证明列")
	}
	preds, used, err := runtimeWherePredicates(sel.Where, args)
	if err != nil {
		return nil, err
	}
	if used != len(args) {
		return nil, fmt.Errorf("SQL 占位符与实参数量不一致（占位符 %d，实参 %d）", used, len(args))
	}
	return &runtimeQuery{Table: tn.Name.String(), Columns: cols, Predicates: preds}, nil
}

// runtimeWherePredicates 提取 AND 连接的 eq/in 谓词；占位符按遍历顺序消费。
func runtimeWherePredicates(where *sqlparser.Where, args []rtValue) ([]runtimePredicate, int, error) {
	if where == nil {
		return nil, 0, nil
	}
	var exprs []sqlparser.Expr
	flattenRuntimeAnd(where.Expr, &exprs)
	var out []runtimePredicate
	used := 0
	nextArg := func() (rtValue, error) {
		if used >= len(args) {
			return rtValue{}, errors.New("SQL 占位符多于实参")
		}
		v := args[used]
		used++
		if !v.known() || v.isNull() || v.kind == rtSlice || v.kind == rtRow || v.kind == rtRows {
			return rtValue{}, fmt.Errorf("查询参数 %d 不是已知标量", used)
		}
		return v, nil
	}
	for _, e := range exprs {
		cmp, ok := e.(*sqlparser.ComparisonExpr)
		if !ok {
			return nil, used, errors.New("WHERE 含不支持的表达式（只支持 AND + eq/in）")
		}
		cn, ok := cmp.Left.(*sqlparser.ColName)
		if !ok {
			return nil, used, errors.New("WHERE 左侧不是普通列")
		}
		switch cmp.Operator {
		case sqlparser.EqualOp:
			var v rtValue
			var err error
			switch right := cmp.Right.(type) {
			case *sqlparser.Argument:
				v, err = nextArg()
			default:
				v, err = runtimeSQLValue(right)
			}
			if err != nil {
				return nil, used, err
			}
			out = append(out, runtimePredicate{Field: cn.Name.String(), Op: "eq", Value: v})
		case sqlparser.InOp:
			var vals []rtValue
			switch right := cmp.Right.(type) {
			case sqlparser.ValTuple:
				for _, item := range right {
					if _, ok := item.(*sqlparser.Argument); ok {
						v, err := nextArg()
						if err != nil {
							return nil, used, err
						}
						vals = append(vals, v)
						continue
					}
					v, err := runtimeSQLValue(item)
					if err != nil {
						return nil, used, err
					}
					vals = append(vals, v)
				}
			case *sqlparser.Argument:
				v, err := nextArg()
				if err != nil {
					return nil, used, err
				}
				if v.kind != rtSlice {
					return nil, used, errors.New("IN 参数不是已知集合")
				}
				vals = append(vals, v.elems...)
			default:
				return nil, used, errors.New("IN 右侧不支持")
			}
			if len(vals) == 0 {
				return nil, used, errors.New("IN 集合为空")
			}
			out = append(out, runtimePredicate{Field: cn.Name.String(), Op: "in", Value: rtSliceValue(vals)})
		default:
			return nil, used, fmt.Errorf("WHERE 运算符 %v 不支持（只支持 eq/in）", cmp.Operator)
		}
	}
	return out, used, nil
}

func flattenRuntimeAnd(e sqlparser.Expr, out *[]sqlparser.Expr) {
	if a, ok := e.(*sqlparser.AndExpr); ok {
		flattenRuntimeAnd(a.Left, out)
		flattenRuntimeAnd(a.Right, out)
		return
	}
	*out = append(*out, e)
}

func runtimeSQLValue(e sqlparser.Expr) (rtValue, error) {
	switch v := e.(type) {
	case *sqlparser.Literal:
		switch v.Type {
		case sqlparser.IntVal:
			i, err := strconv.ParseInt(v.Val, 10, 64)
			if err != nil {
				return rtValue{}, err
			}
			return rtIntValue(i), nil
		case sqlparser.StrVal:
			return rtStringValue(v.Val), nil
		case sqlparser.FloatVal:
			f, err := strconv.ParseFloat(v.Val, 64)
			if err != nil {
				return rtValue{}, err
			}
			return rtFloatValue(f), nil
		}
	case *sqlparser.NullVal:
		return rtNullValue(), nil
	}
	return rtValue{}, errors.New("SQL 字面量类型不支持")
}

// runtimeObservationFor 找到覆盖查询的 observation（唯一；多个冲突→unknown）。

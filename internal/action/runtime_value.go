package action

// todo25 §13：运行时快照的值模型与比较（静态常量/快照行值/参数值共用）。
// 只用于查询内分析；不是 Go 类型系统——未知一律 unknown，不猜零值。

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
)

type rtKind int

const (
	rtUnknown rtKind = iota
	rtNull
	rtInt
	rtFloat
	rtString
	rtBool
	rtSlice
	rtMap
	rtError // 非 nil error（运行时错误边界，不区分具体类型）
	rtRow   // 一行业务数据（列名 → 值）
	rtRows  // 查询返回的多行（元素为 rtRow）
	rtTuple // 多返回值（函数调用结果，elems 按返回槽）
	rtDB    // 数据库句柄（s = target symbol canonical ID）
	// U03：reflect 类型链（TypeOf/Field/Tag.Get）——只承载源码可证明的
	// 类型元信息（s=类型名/标签文本；cols=字段名/tag）。
	rtTypeInfo
	rtFieldInfo
	rtTagInfo
)

type rtValue struct {
	kind     rtKind
	i        int64
	f        float64
	s        string
	b        bool
	elems    []rtValue
	cols     map[string]rtValue
	order    []string
	typeName string // 结构体实例的真实类型（接口变量持有具体类型时用于方法解析）
}

func rtUnknownValue() rtValue          { return rtValue{kind: rtUnknown} }
func rtNullValue() rtValue             { return rtValue{kind: rtNull} }
func rtIntValue(v int64) rtValue       { return rtValue{kind: rtInt, i: v} }
func rtFloatValue(v float64) rtValue   { return rtValue{kind: rtFloat, f: v} }
func rtStringValue(v string) rtValue   { return rtValue{kind: rtString, s: v} }
func rtBoolValue(v bool) rtValue       { return rtValue{kind: rtBool, b: v} }
func rtErrorValue() rtValue            { return rtValue{kind: rtError} }
func rtDBValue(symbol string) rtValue  { return rtValue{kind: rtDB, s: symbol} }
func rtSliceValue(v []rtValue) rtValue { return rtValue{kind: rtSlice, elems: v} }
func rtMapValue(m map[string]rtValue) rtValue {
	order := make([]string, 0, len(m))
	for k := range m {
		order = append(order, k)
	}
	sort.Strings(order)
	return rtValue{kind: rtMap, cols: m, order: order}
}
func rtRowValue(cols map[string]rtValue, order []string) rtValue {
	return rtValue{kind: rtRow, cols: cols, order: order}
}

func (v rtValue) known() bool { return v.kind != rtUnknown }

func (v rtValue) isNull() bool { return v.kind == rtNull }

// rtEqual 值相等（不同数值类型按 Go 比较语义提升；unknown 不相等）。
func rtEqual(a, b rtValue) (eq bool, known bool) {
	if a.kind == rtUnknown || b.kind == rtUnknown {
		return false, false
	}
	// error 是"非 nil"的运行时值：与 nil 比较可判定；具体错误类型不猜。
	if a.kind == rtError || b.kind == rtError {
		switch {
		case a.kind == rtError && b.kind == rtError:
			return true, true
		case a.kind == rtError && b.kind == rtNull:
			return false, true
		case a.kind == rtNull && b.kind == rtError:
			return false, true
		}
		return false, true
	}
	if a.kind == rtNull || b.kind == rtNull {
		return a.kind == rtNull && b.kind == rtNull, true
	}
	if (a.kind == rtInt || a.kind == rtFloat) && (b.kind == rtInt || b.kind == rtFloat) {
		af, bf := a.float64(), b.float64()
		return af == bf, true
	}
	if a.kind != b.kind {
		return false, true
	}
	switch a.kind {
	case rtString:
		return a.s == b.s, true
	case rtBool:
		return a.b == b.b, true
	case rtError:
		return true, true
	}
	return false, false
}

func (v rtValue) float64() float64 {
	if v.kind == rtInt {
		return float64(v.i)
	}
	return v.f
}

// compare 有序比较（仅数字/字符串；其它 unknown）。
func rtCompare(a, b rtValue) (int, bool) {
	if (a.kind == rtInt || a.kind == rtFloat) && (b.kind == rtInt || b.kind == rtFloat) {
		af, bf := a.float64(), b.float64()
		switch {
		case af < bf:
			return -1, true
		case af > bf:
			return 1, true
		default:
			return 0, true
		}
	}
	if a.kind == rtString && b.kind == rtString {
		return strings.Compare(a.s, b.s), true
	}
	return 0, false
}

// rtLen 集合长度（slice/map/rows；未知集合 unknown）。
func rtLen(v rtValue) (int, bool) {
	switch v.kind {
	case rtSlice, rtRows:
		return len(v.elems), true
	case rtMap:
		return len(v.cols), true
	case rtString:
		return len([]rune(v.s)), true
	}
	return 0, false
}

// rtTruth 条件真值（nil/bool/int/string 不隐式转换：Go 条件必须 bool）。
func rtTruth(v rtValue) (bool, bool) {
	if v.kind == rtBool {
		return v.b, true
	}
	return false, false
}

// rtFromJSON 任意 JSON 值 → rtValue（UseNumber 保留整数/浮点区别）。
func rtFromJSON(x any) (rtValue, error) {
	switch v := x.(type) {
	case nil:
		return rtNullValue(), nil
	case bool:
		return rtBoolValue(v), nil
	case string:
		return rtStringValue(v), nil
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return rtIntValue(i), nil
		}
		f, err := v.Float64()
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return rtValue{}, fmt.Errorf("非法数字 %q", v.String())
		}
		return rtFloatValue(f), nil
	case int:
		return rtIntValue(int64(v)), nil
	case int64:
		return rtIntValue(v), nil
	case float64:
		return rtFloatValue(v), nil
	case []any:
		out := make([]rtValue, 0, len(v))
		for _, e := range v {
			ev, err := rtFromJSON(e)
			if err != nil {
				return rtValue{}, err
			}
			out = append(out, ev)
		}
		return rtSliceValue(out), nil
	case map[string]any:
		return rtRowFromMap(v)
	default:
		return rtValue{}, fmt.Errorf("不支持的 JSON 值类型 %T", x)
	}
}

func rtRowFromMap(m map[string]any) (rtValue, error) {
	cols := make(map[string]rtValue, len(m))
	order := make([]string, 0, len(m))
	for k, raw := range m {
		v, err := rtFromJSON(raw)
		if err != nil {
			return rtValue{}, fmt.Errorf("列 %s: %w", k, err)
		}
		cols[k] = v
		order = append(order, k)
	}
	sort.Strings(order)
	return rtRowValue(cols, order), nil
}

// rtDisplay 证据/诊断用短文本（不序列化为业务契约）。
func (v rtValue) rtDisplay() string {
	switch v.kind {
	case rtNull:
		return "null"
	case rtInt:
		return fmt.Sprintf("%d", v.i)
	case rtFloat:
		return fmt.Sprintf("%g", v.f)
	case rtString:
		return strconvQuote(v.s)
	case rtBool:
		return fmt.Sprintf("%t", v.b)
	case rtError:
		return "error"
	case rtDB:
		return "db(" + v.s + ")"
	case rtTypeInfo:
		return "type(" + v.s + ")"
	case rtFieldInfo:
		return "field(" + v.cols["name"].s + ")"
	case rtTagInfo:
		return "tag(" + v.s + ")"
	case rtSlice:
		parts := make([]string, 0, len(v.elems))
		for _, e := range v.elems {
			parts = append(parts, e.rtDisplay())
		}
		return "[" + strings.Join(parts, ",") + "]"
	case rtRows:
		return fmt.Sprintf("rows(%d)", len(v.elems))
	case rtTuple:
		parts := make([]string, 0, len(v.elems))
		for _, e := range v.elems {
			parts = append(parts, e.rtDisplay())
		}
		return "(" + strings.Join(parts, ",") + ")"
	case rtRow:
		parts := make([]string, 0, len(v.cols))
		for _, k := range v.order {
			parts = append(parts, k+"="+v.cols[k].rtDisplay())
		}
		return "{" + strings.Join(parts, ",") + "}"
	case rtMap:
		return fmt.Sprintf("map(%d)", len(v.cols))
	default:
		return "unknown"
	}
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

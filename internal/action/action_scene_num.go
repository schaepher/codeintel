package action

// todo23 §5：场景判定里的数值转换——只把**内建**整数类型转换当作可求值
// 转换，并做范围校验（int8(256)/uint8(-1)/int64 溢出不得因截断后匹配）；
// 同名函数/局部变量/导入别名遮蔽时不按内建转换求解。

import (
	"go/ast"
	"math/big"
	"strconv"
	"strings"
)

// sceneIntType 内建整数类型 → (位数, 是否有符号)。
func sceneIntType(name string) (bits int, signed bool, ok bool) {
	switch name {
	case "int", "int64":
		return 64, true, true
	case "int32", "rune":
		return 32, true, true
	case "int16":
		return 16, true, true
	case "int8":
		return 8, true, true
	case "uint", "uint64", "uintptr":
		return 64, false, true
	case "uint32":
		return 32, false, true
	case "uint16":
		return 16, false, true
	case "uint8", "byte":
		return 8, false, true
	}
	return 0, false, false
}

// sceneBuiltinConversionCall 是否**未被遮蔽**的内建整数转换调用。
// `ast.Ident.Obj` 非空表示该名字在文件/函数作用域里另有声明（函数/变量/
// 导入名），不能当内建类型。
func sceneBuiltinConversionCall(call *ast.CallExpr) (string, bool) {
	if call == nil || len(call.Args) != 1 {
		return "", false
	}
	id, ok := call.Fun.(*ast.Ident)
	if !ok || id.Obj != nil {
		return "", false
	}
	if _, _, ok := sceneIntType(id.Name); !ok {
		return "", false
	}
	return id.Name, true
}

// sceneConvertInt 按目标类型做范围校验后的十进制值；越界/非法 → false。
func sceneConvertInt(typ, raw string) (string, bool) {
	bits, signed, ok := sceneIntType(typ)
	if !ok {
		return "", false
	}
	s := strings.ReplaceAll(raw, "_", "")
	s = strings.ReplaceAll(s, "'", "")
	n := new(big.Int)
	if _, ok := n.SetString(s, 0); !ok {
		// 允许十进制带符号（SetString 基数 0 已处理 0x/0b；再兜底一次）
		if _, ok2 := n.SetString(s, 10); !ok2 {
			return "", false
		}
	}
	if signed {
		min := new(big.Int).Lsh(big.NewInt(1), uint(bits-1))
		max := new(big.Int).Sub(min, big.NewInt(1))
		min.Neg(min)
		if n.Cmp(min) < 0 || n.Cmp(max) > 0 {
			return "", false
		}
		return n.String(), true
	}
	max := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), uint(bits)), big.NewInt(1))
	if n.Sign() < 0 || n.Cmp(max) > 0 {
		return "", false
	}
	return n.String(), true
}

// sceneValueEqual 值比较：两侧都能解析为整数/浮点数时按数值比较
// （Source=7 与常量 7、int32 转换后的值一致）；否则按文本比较。
// sceneNormalizeNumberLiteral 整数字面量 → 十进制文本（十六/八/二进制、
// 下划线分隔、字符字面量都归一化；无法解析时原样返回）。浮点保持原样
// （归一化会改变显示精度语义）。
func sceneNormalizeNumberLiteral(raw string) string {
	s := strings.ReplaceAll(raw, "_", "")
	if strings.HasPrefix(s, "'") && strings.HasSuffix(s, "'") && len(s) >= 3 {
		if r, _, _, err := strconv.UnquoteChar(s[1:len(s)-1], '\''); err == nil {
			return strconv.Itoa(int(r))
		}
		return raw
	}
	if strings.ContainsAny(s, ".eE") && !strings.HasPrefix(strings.ToLower(s), "0x") {
		return raw
	}
	n, ok := new(big.Int).SetString(s, 0)
	if !ok {
		return raw
	}
	return n.String()
}

func sceneValueEqual(a, b string) bool {
	if na, ok := sceneNumber(a); ok {
		if nb, ok2 := sceneNumber(b); ok2 {
			return na == nb
		}
	}
	return a == b
}

// sceneNumber 整数/浮点字面量 → float64（去掉类型后缀与进制下划线）。
func sceneNumber(s string) (float64, bool) {
	v := strings.ReplaceAll(strings.TrimSuffix(strings.TrimSuffix(s, "i"), "_"), "_", "")
	v = strings.ReplaceAll(v, "'", "") // 数字分隔符 1_000 / 1'000
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		if n, ok := new(big.Int).SetString(v, 0); ok {
			fv, _ := new(big.Float).SetInt(n).Float64()
			return fv, true
		}
		return 0, false
	}
	return f, true
}

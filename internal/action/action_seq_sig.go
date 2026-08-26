package action

// R95 迁移：时序图停止包判定 + 签名解析（原 cli/query_sequence_stop.go
// 逻辑部分——配置读取 loadSeqDepth/loadSeqStopPkgs 留 cli，StopPackages
// 经 CodeSequenceRequest 传入；SigTypeKeyword 导出——cli 渲染用）。

import (
	"strings"
)

// seqStopPkgHit 符号 ID 所在包是否命中停止列表（完整路径或短名匹配）。
func seqStopPkgHit(symID string, stops []string) bool {
	if len(stops) == 0 {
		return false
	}
	pkg := pkgOfEntityID(symID)
	short := pkg
	if i := strings.LastIndex(short, "/"); i >= 0 {
		short = short[i+1:]
	}
	for _, s := range stops {
		if pkg == s || short == s || strings.HasSuffix(pkg, "/"+s) {
			return true
		}
	}
	return false
}

// implTypeShort 从被调符号 canonical ID 提取短类型名（R83：参与者第
// 二行——包最后路径段.类型名）：
//   symbol:go:example.com/m/domain/order:(orderManagerImpl).SubmitOrder
//     → order.orderManagerImpl（方法形态取 (T) 的 T）
//   symbol:go:example.com/m/repo:orderRepo → repo.orderRepo（类型形态）
//   函数形态（无类型）→ 空。
func implTypeShort(symID string) string {
	rest := strings.TrimPrefix(symID, "symbol:go:")
	// 方法形态：(Type).Method——类型在括号里
	if i := strings.Index(rest, ":("); i >= 0 {
		pkg := rest[:i]
		t := rest[i+2:]
		if j := strings.Index(t, ")."); j >= 0 {
			t = t[:j]
		}
		return pkgShort(pkg) + "." + t
	}
	// 类型/函数形态：pkg:name
	if i := strings.LastIndex(rest, ":"); i >= 0 {
		pkg := rest[:i]
		name := rest[i+1:]
		if name != "" {
			return pkgShort(pkg) + "." + name
		}
	}
	return ""
}

// pkgShort 包路径最后一段（example.com/m/domain/order → order）。
func pkgShort(pkg string) string {
	if i := strings.LastIndex(pkg, "/"); i >= 0 {
		return pkg[i+1:]
	}
	return pkg
}

// sigTypesOf 被调符号的参数/返回类型短名（R83：消息线参数 + return 线）
// ——从索引节点 signature 属性解析（func (R).M(args) (rets) 形态，
// 完整路径类型 → 短名）。无签名返回 ok=false。
func sigTypesOf(a *Actions, symID string) ([]string, []string, bool) {
	n, err := a.ResolveSymbol(symID)
	if err != nil {
		return nil, nil, false
	}
	sig, ok := n.Properties["signature"].(string)
	if !ok || sig == "" {
		return nil, nil, false
	}
	return parseSigTypes(sig)
}

// parseSigTypes 解析 Go 函数签名的参数/返回类型（短名化）。
// func (R).M(a pkg.T, b int) (pkg.U, error)
//   → args=[T, int] rets=[U, error]（短名 = 包最后路径段.类型名）。
func parseSigTypes(sig string) ([]string, []string, bool) {
	// 找第一个 '('（receiver 起点）与匹配的 ')'，函数名在中间
	depth := 0
	start := -1
	var parens []int
	for i := 0; i < len(sig); i++ {
		switch sig[i] {
		case '(':
			if depth == 0 {
				start = i
			}
			depth++
		case ')':
			depth--
			if depth == 0 && start >= 0 {
				parens = append(parens, start, i)
				start = -1
			}
		}
	}
	// 第一对括号是 receiver（单 token：*cartRepo / T——无空格）还是参数
	first := strings.TrimSpace(sig[parens[0]+1 : parens[1]])
	isReceiver := first != "" && !strings.ContainsAny(first, " \t,")
	switch {
	case len(parens) >= 6:
		// receiver + 参数 + 返回
		args := splitSigParams(sig[parens[2]+1 : parens[3]])
		rets := splitSigParams(sig[parens[4]+1 : parens[5]])
		return args, rets, true
	case len(parens) == 4 && isReceiver:
		// receiver + 参数 + 单返回（无括号——参数闭括号后到签名尾）
		args := splitSigParams(sig[parens[2]+1 : parens[3]])
		var rets []string
		if rest := strings.TrimSpace(sig[parens[3]+1:]); rest != "" {
			rets = []string{shortSigType(rest)}
		}
		return args, rets, true
	case len(parens) == 4:
		// 无 receiver：参数 + 返回
		args := splitSigParams(sig[parens[0]+1 : parens[1]])
		rets := splitSigParams(sig[parens[2]+1 : parens[3]])
		return args, rets, true
	}
	return nil, nil, false
}

// splitSigParams 括号内参数列表分割（顶层逗号）→ 类型短名列表。
// 处理：`a pkg.T` / `a, b int`（共享类型） / `*pkg.T` / `[]byte` /
// 嵌套括号（func()）。
func splitSigParams(inner string) []string {
	var items []string
	depth := 0
	last := 0
	for i := 0; i < len(inner); i++ {
		switch inner[i] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case ',':
			if depth == 0 {
				items = append(items, strings.TrimSpace(inner[last:i]))
				last = i + 1
			}
		}
	}
	if t := strings.TrimSpace(inner[last:]); t != "" {
		items = append(items, t)
	}
	var out []string
	for i, it := range items {
		if it == "" {
			continue
		}
		fields := strings.Fields(it)
		if len(fields) >= 2 {
			// `name type` → type
			out = append(out, shortSigType(fields[len(fields)-1]))
			continue
		}
		// 单 token：类型关键字 → 类型；裸标识符（`a, b int` 的 a）→
		// 共享下一项类型（a 也是 int——补一个）
		if SigTypeKeyword(fields[0]) {
			out = append(out, shortSigType(fields[0]))
			continue
		}
		if i+1 < len(items) && strings.Contains(items[i+1], " ") {
			nf := strings.Fields(items[i+1])
			out = append(out, shortSigType(nf[len(nf)-1]))
			continue
		}
		out = append(out, shortSigType(fields[0]))
	}
	return out
}

// SigTypeKeyword Go 内置类型关键字（单 token 且是类型而非变量名）。
// 导出——cli 渲染（seqBaseTypeActor 类型转换参与者判定）同源调用。
func SigTypeKeyword(t string) bool {
	switch t {
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64", "uintptr",
		"float32", "float64", "complex64", "complex128",
		"string", "bool", "byte", "rune", "error", "any":
		return true
	}
	// 复合形态（[]byte / map[..].. / func(..) / chan .. / interface{..}）
	return strings.HasPrefix(t, "[]") || strings.HasPrefix(t, "map[") ||
		strings.HasPrefix(t, "func(") || strings.HasPrefix(t, "chan ") ||
		strings.HasPrefix(t, "interface{") || strings.HasPrefix(t, "struct{")
}

// shortSigType 类型短名化：github.com/x/pkg.T → pkg.T；*github.com/x/pkg.T
// → *pkg.T；error/[]byte/int 等无路径原样。
func shortSigType(t string) string {
	ptr := ""
	for strings.HasPrefix(t, "*") {
		ptr += "*"
		t = t[1:]
	}
	if i := strings.LastIndex(t, "/"); i >= 0 {
		t = t[i+1:]
	}
	return ptr + t
}

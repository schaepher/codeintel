package ast

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
)

// extractStringArg 从实参提取字符串值（字面量 / 同函数 methodVars /
// types.Const / 常量字符串拼接 P1-3），非字符串返回空。
func extractStringArg(pkg *packages.Package, methodVars map[string]string, arg ast.Expr) string {
	switch a := arg.(type) {
	case *ast.BasicLit:
		if a.Kind == token.STRING {
			if v, err := strconv.Unquote(a.Value); err == nil {
				return v
			}
		}
	case *ast.Ident:
		if v, ok := methodVars[a.Name]; ok {
			return v
		}
		if obj := pkg.TypesInfo.ObjectOf(a); obj != nil {
			if c, ok := obj.(*types.Const); ok && c.Val() != nil && c.Val().Kind() == constant.String {
				return constant.StringVal(c.Val())
			}
		}
	case *ast.BinaryExpr:

		if a.Op == token.ADD {
			l := extractStringArg(pkg, methodVars, a.X)
			r := extractStringArg(pkg, methodVars, a.Y)
			if l != "" && r != "" {
				return l + r
			}
			// R71：部分解析——一端可解析返回该端（动态 URL 拼接的
			// host/path 前缀：go2o sms/alipay/geo 形态 `"https://..."
			// + 变量`——只提取字面量部分，出站调用不再漏检）
			if l != "" {
				return l
			}
			return r
		}
	case *ast.CallExpr:
		// R78：fmt.Sprintf 静态前缀求值（go2o cl253 形态：
		// `fmt.Sprintf("%s?un=%s...", url常量, 动态...)`）——格式串
		// 字面量 + 可解析参数展开，首个不可解析参数截断
		if isFmtSprintf(pkg, a) {
			return sprintfStaticPrefix(pkg, methodVars, a)
		}
	}
	return ""
}

// isFmtSprintf 调用是否为 fmt.Sprintf（pkg 路径判定——防同名函数误伤）。
func isFmtSprintf(pkg *packages.Package, call *ast.CallExpr) bool {
	if len(call.Args) == 0 {
		return false
	}
	sel, isSel := call.Fun.(*ast.SelectorExpr)
	if !isSel || sel.Sel.Name != "Sprintf" {
		return false
	}
	obj := pkg.TypesInfo.ObjectOf(sel.Sel)
	if fn, isFn := obj.(*types.Func); isFn && fn.Pkg() != nil {
		return fn.Pkg().Path() == "fmt"
	}
	return false
}

// sprintfStaticPrefix fmt.Sprintf 可静态求值前缀：格式串按 % 动词逐
// 段处理——字面量段直接取；动词段对应实参可解析（字面量/常量/
// methodVars）则展开拼接，不可解析停（返回已拼接前缀）。%% 转义为
// 字面 %。无格式串（非字面量）返回空。
func sprintfStaticPrefix(pkg *packages.Package, methodVars map[string]string, call *ast.CallExpr) string {
	format := extractStringArg(pkg, methodVars, call.Args[0])
	if format == "" {
		return ""
	}
	var b strings.Builder
	argIdx := 1
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			b.WriteByte(format[i])
			continue
		}
		if i+1 >= len(format) {
			break
		}
		if format[i+1] == '%' {
			b.WriteByte('%')
			i++
			continue
		}
		// 动词（%s/%d/%v/%x…）：对应实参可解析则展开，否则截断。
		// verbEnd = 动词字母位置——展开后跳过（外层 i++ 到 verbEnd+1，
		// 否则动词字母会被当普通字符重复写入）
		verbEnd := i + 1
		for verbEnd < len(format) && !isFormatVerbEnd(format[verbEnd]) {
			verbEnd++
		}
		if verbEnd >= len(format) {
			break
		}
		if argIdx >= len(call.Args) {
			break
		}
		v := extractStringArg(pkg, methodVars, call.Args[argIdx])
		argIdx++
		if v == "" {
			break
		}
		b.WriteString(v)
		i = verbEnd
	}
	return b.String()
}

// isFormatVerbEnd 格式动词结尾字符（宽度/精度/动词标志后的字母）。
func isFormatVerbEnd(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// httpURLString 从 http.Get/NewRequest/NewRequestWithContext 调用提取
// URL 字符串（§18.7，P1-3 补 NewRequestWithContext——与 NewRequest 同
// 参数位，ctx 在 0 位）：Get 第 1 参 / NewRequest 第 2 参 /
// NewRequestWithContext 第 3 参；URL 须含 scheme 或以 / 开头（防
// 误伤同名方法）。动态变量返回 ok=false（盲区）。
func httpURLString(pkg *packages.Package, methodVars map[string]string,
	call *ast.CallExpr, callee *types.Func) (string, bool) {
	if callee == nil {
		return "", false
	}
	idx := 0
	switch callee.Name() {
	case "Get":
	case "NewRequest":
		idx = 1
	case "NewRequestWithContext":
		idx = 2
	default:
		return "", false
	}
	if len(call.Args) <= idx {
		return "", false
	}
	u := extractStringArg(pkg, methodVars, call.Args[idx])
	if u == "" {
		return "", false
	}
	if !strings.Contains(u, "://") && !strings.HasPrefix(u, "/") {
		return "", false
	}
	return u, true
}

// httpBodyType HTTP 请求体实参类型（R100 待办11——http_call 边
// req_type，与 grpc req_type 对齐，供外部接口请求对象判定）：
// NewRequest → Args[2]；NewRequestWithContext → Args[3]；Get/Do 无
// body → 空。
func httpBodyType(pkg *packages.Package, call *ast.CallExpr, callee *types.Func) string {
	if callee == nil {
		return ""
	}
	idx := -1
	switch callee.Name() {
	case "NewRequest":
		idx = 2
	case "NewRequestWithContext":
		idx = 3
	default:
		return ""
	}
	if len(call.Args) <= idx {
		return ""
	}
	return typePath(pkg.TypesInfo.TypeOf(call.Args[idx]))
}

// httpMethodOf Q205d：按调用形态取 HTTP method 实参（常量传播）：
// http.Get → "GET"；NewRequest → Args[0]；NewRequestWithContext →
// Args[1]；未知/非常量 → ""（emitHTTP 默认 GET）。此前 emitHTTP 在
// 内部猜 Args[0]，http.Get(字面量 URL) 会把 URL 当 method。
func httpMethodOf(pkg *packages.Package, methodVars map[string]string, call *ast.CallExpr, callee *types.Func) string {
	if callee == nil {
		return ""
	}
	idx := -1
	switch callee.Name() {
	case "Get":
		return "GET"
	case "NewRequest":
		idx = 0
	case "NewRequestWithContext":
		idx = 1
	default:
		return ""
	}
	if len(call.Args) <= idx {
		return ""
	}
	return extractStringArg(pkg, methodVars, call.Args[idx])
}

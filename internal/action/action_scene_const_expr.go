package action

import "go/ast"
import "strings"

// sceneValueOf 表达式的"可比较值"：字面量（字符串/整数/浮点/字符）或包内
// 常量（按 const 声明解析），中间的内建转换/括号被剥离。
func sceneValueOf(e ast.Expr, consts sceneConstLookup) (string, bool) {

	for {
		call, ok := e.(*ast.CallExpr)
		if !ok {
			break
		}
		typ, isConv := sceneBuiltinConversionCall(call)
		if !isConv {
			break
		}
		inner, ok := sceneValueOf(call.Args[0], consts)
		if !ok {
			return "", false
		}
		return sceneConvertInt(typ, inner)
	}
	e = unwrapSceneExpr(e)
	switch x := e.(type) {
	case *ast.BinaryExpr:

		lv, okL := sceneValueOf(x.X, consts)
		rv, okR := sceneValueOf(x.Y, consts)
		if !okL || !okR {
			return "", false
		}
		if x.Op.String() == "+" {
			if _, _, ok := sceneIntType("int"); ok {
				if _, isNumL := sceneParseInt(lv); !isNumL {
					return lv + rv, true
				}
			}
		}
		return sceneIntBinary(x.Op.String(), lv, rv)
	case *ast.BasicLit:
		switch x.Kind.String() {
		case "STRING":
			return strings.Trim(x.Value, "\"`"), true
		case "INT", "FLOAT":

			return sceneNormalizeNumberLiteral(x.Value), true
		case "CHAR":

			return sceneNormalizeNumberLiteral(x.Value), true
		}
	case *ast.Ident:
		if v, ok := sceneConstValue(consts, x.Name); ok {
			return v, true
		}

		if x.Obj == nil && (x.Name == "true" || x.Name == "false") {
			return x.Name, true
		}
	case *ast.SelectorExpr:

		if id, ok := x.X.(*ast.Ident); ok && id.Obj != nil && id.Obj.Kind != ast.Pkg {
			return "", false
		}
		if v, ok := sceneConstValue(consts, exprTextShallow(x)); ok {
			return v, true
		}
	}
	return "", false
}

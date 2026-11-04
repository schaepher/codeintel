package action

// U04c/T12b：字面量来源与局部场景实参。
import (
	"go/ast"
	"go/token"
	"strconv"
	"strings"
)

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

// seqSceneArgsWithLocals 条件里的局部变量若已有确定字面量来源（含续查后
// 绑定的返回槽），作为场景实参参与比较——T12b"续查结果可被调用方当前赋值/
// 后续语句使用"。不覆盖已有形参实参值。
func seqSceneArgsWithLocals(req CodeSequenceRequest, cond ast.Expr) (map[string]string, map[string]string) {
	args := req.sceneArgs
	srcs := req.sceneSrc
	if req.env == nil || cond == nil {
		return args, srcs
	}
	var addArgs, addSrcs map[string]string
	ast.Inspect(cond, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok || id.Obj == nil {
			return true
		}
		origin, found := req.env.scalarOriginAt(id.Obj, id.Pos())
		if !found || origin == nil || origin.Kind != SeqOriginLiteral || origin.Literal == "" {
			return true
		}
		val := origin.Literal
		if origin.ScalarType == "string" {
			unq, err := strconv.Unquote(origin.Literal)
			if err != nil {
				return true
			}
			val = unq
		}
		if addArgs == nil {
			addArgs = map[string]string{}
			addSrcs = map[string]string{}
		}
		addArgs[id.Name] = val
		addSrcs[id.Name] = "local:" + origin.Kind
		return true
	})
	if addArgs == nil {
		return args, srcs
	}
	merged := make(map[string]string, len(args)+len(addArgs))
	for k, v := range args {
		merged[k] = v
	}
	mergedSrc := make(map[string]string, len(srcs)+len(addSrcs))
	for k, v := range srcs {
		mergedSrc[k] = v
	}
	for k, v := range addArgs {
		if _, exists := merged[k]; !exists {
			merged[k] = v
		}
	}
	for k, v := range addSrcs {
		if _, exists := mergedSrc[k]; !exists {
			mergedSrc[k] = v
		}
	}
	return merged, mergedSrc
}

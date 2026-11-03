package action

// U04g：ORM 实参的准确模型声明与字段源码（evidence.orm.models）。
//
// 只从**静态声明类型**关联模型：显式类型形参/变量、字面量、未遮蔽的
// new(T)、单结果包级构造器的返回签名，以及上述表达式的局部赋值。
// 接口/any 返回体即使只返回一个具体对象也不借该对象建立关联。

import "go/ast"

// 模型解析边界。
const (
	SeqORMModelUnproven = "model_mapping_unproven"
	seqORMModelMaxDepth = 8
)

// seqORMStruct 已解析的模型声明（请求内缓存值）。
type seqORMStruct struct {
	SymbolID string
	Package  string
	File     string
	Fields   []SeqORMField
}

// seqORMTypeRef 静态类型引用（含声明所在文件上下文）。
type seqORMTypeRef struct {
	pkgPath string
	name    string
}

// seqORMModelsFor 终端调用的每个实参 → 模型（无法证明则跳过该实参）。
func seqORMModelsFor(call *ast.CallExpr, c *seqPersistCtx) []SeqORMModel {
	if call == nil || c == nil {
		return nil
	}
	var out []SeqORMModel
	for i, arg := range call.Args {
		ref, ok := seqORMArgTypeRef(c, arg, 0)
		if !ok {
			continue
		}
		decl := c.resolveORMModel(ref)
		if decl == nil {
			continue
		}
		model := SeqORMModel{Argument: i, SymbolID: decl.SymbolID, Fields: decl.Fields,
			Boundary: SeqORMModelUnproven}
		if c.actions != nil && decl.SymbolID != "" {
			if src, err := c.actions.SymbolSource(SymbolSourceRequest{
				RepoAbs: c.repoAbs, SymbolID: decl.SymbolID}); err == nil && src != nil {
				model.Source = src
			}
		}
		out = append(out, model)
	}
	return out
}

package action

import (
	"context"
	"go/ast"
	"go/token"
)

// focusActive 是否启用焦点过滤。
func (r CodeSequenceRequest) focusActive() bool {
	return r.focus != nil && (r.focus.returns || len(r.focus.fields) > 0)
}

// ctx 取消上下文（nil 安全）。
func (r CodeSequenceRequest) ctx() context.Context {
	if r.Ctx != nil {
		return r.Ctx
	}
	return context.Background()
}

// seqParsedFile 一次分析内复用的源码解析结果（todo13-17：同文件多函数
// 展开不重复 ReadFile/ParseFile；缓存仅存活于本次查询，不存在陈旧问题）。
type seqParsedFile struct {
	fset *token.FileSet
	file *ast.File
	src  []byte
}

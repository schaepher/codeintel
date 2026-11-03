package action

// U04b–U04g：持久化/SQL/模型证据的调用点上下文（源码/文件/包/缓存）。

import (
	"go/ast"
	"go/token"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// seqPersistCtx 持久化注释所需的调用点上下文（源码/文件/包）。
type seqPersistCtx struct {
	file    *ast.File
	pkgPath string
	src     []byte
	fset    *token.FileSet
	guard   string
	// argOrigins U04c：调用实参的标量来源（按 call.Args 下标；SQL 之后的值
	// 实参用 [sqlArg+1:] 对齐 argument 编号）。
	argOrigins []*SeqStorageOrigin
	// runtime U04d：运行时快照分析（写入资源绑定结论）。
	runtime *runtimeAnalysis
	// originOf U04c/U04f：在**当前求值点**解析表达式标量来源（Where 参数用）。
	originOf func(ast.Expr) *SeqStorageOrigin
	// U04g：模型声明解析（动作/仓库/当前文件 + 请求内缓存）。
	actions   *Actions
	repoAbs   string
	fileRel   string
	req       *CodeSequenceRequest
	ormModels map[string]*seqORMStruct
}

// fileCtxFor 当前仓库文件的解析上下文（同包跨文件）。
func (c *seqPersistCtx) fileCtxFor(rel string) *seqFileCtx {
	if c == nil || c.req == nil {
		return nil
	}
	return seqFileCtxFor(*c.req, rel)
}

// externalFileCtx 按 canonical ID 定位声明文件（外部包/其它包）。
func (c *seqPersistCtx) externalFileCtx(symID string) *seqFileCtx {
	if c == nil || c.actions == nil || c.req == nil {
		return nil
	}
	n, err := c.actions.repo.GetSymbol(domain.CanonicalID(symID))
	if err != nil || n == nil || n.FilePath == "" {
		return nil
	}
	return seqFileCtxFor(*c.req, n.FilePath)
}

// seqExprText 表达式在源码中的原文（缺失上下文返回空）。
func (c *seqPersistCtx) exprText(e ast.Expr) string {
	if c == nil || e == nil || c.fset == nil || len(c.src) == 0 {
		return ""
	}
	start := c.fset.Position(e.Pos()).Offset
	end := c.fset.Position(e.End()).Offset
	if start < 0 || end > len(c.src) || start >= end {
		return ""
	}
	return strings.TrimSpace(string(c.src[start:end]))
}

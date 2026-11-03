package action

// U04i：XORM 写入的 (int64,error) 签名源码证明与错误返回槽。
//
// 不能只凭框架名/方法短名/短签名认定错误槽 1：必须从**准确声明源码**核对
// 两个结果槽是内置 int64,error，且选中文件里没有对这两个名字的包级遮蔽。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// seqXormErrorSignature 核对 node 的准确方法声明是否为 (int64,error)；
// 返回声明源码（signature_source）；不满足则返回 nil。
func (c *seqPersistCtx) seqXormErrorSignature(node *CodeSeqNode) *SymbolSource {
	if c == nil || c.actions == nil || node == nil || node.Evidence == nil {
		return nil
	}
	id := node.Evidence.SymbolID
	src, err := c.actions.SymbolSource(SymbolSourceRequest{RepoAbs: c.repoAbs, SymbolID: id})
	if err != nil || src == nil || strings.TrimSpace(src.Text) == "" {
		return nil
	}
	fd := parseSeqSingleFunc(src.Text)
	if fd == nil || fd.Type == nil || fd.Type.Results == nil || len(fd.Type.Results.List) != 2 {
		return nil
	}
	if !seqResultIsBuiltin(fd.Type.Results.List[0], "int64") ||
		!seqResultIsBuiltin(fd.Type.Results.List[1], "error") {
		return nil
	}
	importPath := pkgPathOf(id)
	if importPath == "" || c.seqPackageShadowsBuiltin(importPath, "int64", "error") {
		return nil
	}
	return src
}

// parseSeqSingleFunc 解析声明文本（单函数；带 package 前缀）。
func parseSeqSingleFunc(text string) *ast.FuncDecl {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "decl.go", "package p\n"+text, 0)
	if err != nil {
		return nil
	}
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok {
			return fd
		}
	}
	return nil
}

// seqResultIsBuiltin 结果槽类型是否为未限定的内置名（int64/error）。
func seqResultIsBuiltin(field *ast.Field, name string) bool {
	if field == nil || len(field.Names) > 1 {
		return false
	}
	id, ok := field.Type.(*ast.Ident)
	return ok && id.Name == name && id.Obj == nil
}

// seqPackageShadowsBuiltin 选中文件里是否有同名包级类型声明（遮蔽内置名）。
// 构建标签排除的文件不作为遮蔽依据（无法证明其被选中）。
func (c *seqPersistCtx) seqPackageShadowsBuiltin(importPath string, names ...string) bool {
	if c == nil || c.actions == nil || c.req == nil {
		return true
	}
	pkg := seqExternalPackage(c.actions, *c.req, importPath)
	if pkg == nil || pkg.err != nil {
		// 无法核对选中文件集 → 保守放弃证明。
		return true
	}
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	for _, ctx := range pkg.files {
		if ctx == nil || ctx.file == nil || seqFileHasBuildConstraint(ctx) {
			continue
		}
		for _, d := range ctx.file.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if ok && ts.Name != nil && want[ts.Name.Name] {
					return true
				}
			}
		}
	}
	return false
}

// seqFileHasBuildConstraint 文件是否带构建约束（保守跳过——被排除的变体
// 不能遮蔽当前签名）。
func seqFileHasBuildConstraint(ctx *seqFileCtx) bool {
	if ctx == nil || len(ctx.src) == 0 {
		return false
	}
	head := string(ctx.src)
	if i := strings.Index(head, "package "); i >= 0 {
		head = head[:i]
	}
	return strings.Contains(head, "//go:build") || strings.Contains(head, "// +build")
}

// seqAttachORMErrorResult XORM 终端调用的错误槽 + 签名源码（U04i）。
func seqAttachORMErrorResult(req CodeSequenceRequest, node *CodeSeqNode, call *ast.CallExpr,
	c *seqPersistCtx) {
	if node == nil || node.Evidence == nil || node.Evidence.ORM == nil || c == nil {
		return
	}
	sig := c.seqXormErrorSignature(node)
	if sig == nil {
		return
	}
	base := seqWriteErrorResult(req, node, call, c)
	if base == nil {
		return
	}
	orm := node.Evidence.ORM
	orm.ErrorResult = &SeqORMErrorResult{SeqWriteErrorResult: *base, SignatureSource: sig}
	if c.req != nil && c.req.writeCalls != nil {
		c.req.writeCalls[call] = base
	}
}

// seqSymbolOfORM 测试/渲染辅助：ORM 签名符号（无则空）。
func seqSymbolOfORM(orm *SeqORM) string {
	if orm == nil || orm.ErrorResult == nil || orm.ErrorResult.SignatureSource == nil {
		return ""
	}
	return orm.ErrorResult.SignatureSource.SymbolID
}

var _ = domain.CanonicalID("")

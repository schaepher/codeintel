package action

import "go/ast"
import "go/parser"
import "go/token"
import "os"
import "path/filepath"
import "github.com/schaepher/codeintel/internal/domain"

// recvTypeName receiver 类型 AST → 类型名（*T / T → T；跨包形态
// pkg.T → T——同包字段写入常见）。
func recvTypeName(t ast.Expr) string {
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}
	if sel, ok := t.(*ast.SelectorExpr); ok {
		return sel.Sel.Name
	}
	if id, ok := t.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// receiverFieldSel 调用 fun（s.manager.SubmitOrder）是否为 receiver
// 字段方法调用——s 是当前方法 receiver 名（recvType 对应）、X 是
// SelectorExpr（s.manager）→ 返回字段名 manager。
func receiverFieldSel(sel *ast.SelectorExpr, recvType string) (string, bool) {
	x, ok := sel.X.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}

	if _, isID := x.X.(*ast.Ident); !isID {
		return "", false
	}
	return x.Sel.Name, true
}

// receiverFieldImpl 数据流具体化（R97-2）：receiver 字段（s.manager）
// 的赋值来源类型——查摘要 direct_write 写入函数 → 读源码 AST 找
// 赋值表达式（x.manager = &orderManagerImpl{}）的具体类型 → 构造
// (Impl).Method。找不到（无写入摘要/赋值形态不支持）返回空——
// 调用方 fallback 接口类型匹配（InterfaceMethodImpl）。
func (a *Actions) receiverFieldImpl(req CodeSequenceRequest, recvType, field, method string) string {

	writers, err := a.repo.GetFieldWriters("." + recvType + "." + field)
	if err != nil || len(writers) == 0 {
		return ""
	}

	for _, w := range writers {
		implType, pkg := fieldWriteType(a, req, w, field)
		if implType == "" {
			continue
		}
		return GrpcMethodEntryID("symbol:go:"+pkg+":"+implType, method)
	}
	return ""
}

// fieldWriteType 读写入函数源码，找 "x.<field> = <X>" 赋值中 X 的
// 类型名（&orderManagerImpl{} → orderManagerImpl；构造器调用
// newOrderManager() → newOrderManager 的返回？MVP：字面量与同包
// Ident）。返回 (类型名, 包路径)。
func fieldWriteType(a *Actions, req CodeSequenceRequest, writerID, field string) (string, string) {
	n, err := a.repo.GetSymbol(domain.CanonicalID(writerID))
	if err != nil || n.FilePath == "" {
		return "", ""
	}
	src, err := os.ReadFile(filepath.Join(req.RepoAbs, n.FilePath))
	if err != nil {
		return "", ""
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, n.FilePath, src, 0)
	if err != nil {
		return "", ""
	}

	pkg := pkgPathOf(writerID)
	var found string
	ast.Inspect(f, func(node ast.Node) bool {
		if found != "" {
			return false
		}
		as, ok := node.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
			return true
		}
		sel, ok := as.Lhs[0].(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != field {
			return true
		}

		rhs := as.Rhs[0]
		if un, ok := rhs.(*ast.UnaryExpr); ok {
			rhs = un.X
		}
		if cl, ok := rhs.(*ast.CompositeLit); ok {
			if id, ok2 := cl.Type.(*ast.Ident); ok2 {
				found = id.Name
			}
		}
		return true
	})
	return found, pkg
}

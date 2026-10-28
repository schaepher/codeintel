package action

// todo25 §11.1：包级单例（跨包 init 写入）的字段来源追踪。
//
// 形态：`func Get() API { return current }` + `func Inject(v API) { current = v }`
// + `impl.init(){ api.Inject(&Impl{}) }`。函数体 return 包级变量时，沿该包
// 所有文件里对它的赋值找到 writer；writer 的 RHS 是形参时，再沿 writer 的
// **调用点实参**（只认 init，避免未执行函数）取具体类型。来源预算与展示
// depth 分离（这里独立计层，最多 8 层）。

import (
	"go/ast"
	"go/token"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

const seqGlobalSourceBudget = 8

// seqGlobalReturnTypes 函数体 `return <pkgVar>` 的包级单例写入类型。
func seqGlobalReturnTypes(a *Actions, req CodeSequenceRequest, f *ast.File,
	fd *ast.FuncDecl, pkgPath string, depth int) []writeSource {
	if fd == nil || fd.Body == nil || depth <= 0 {
		return nil
	}
	var out []writeSource
	for _, st := range fd.Body.List {
		ret, ok := st.(*ast.ReturnStmt)
		if !ok {
			continue
		}
		for _, r := range ret.Results {
			id, ok := r.(*ast.Ident)
			if !ok {
				continue
			}
			out = appendUnique(out, seqGlobalVarWriteTypes(a, req, f, pkgPath, id.Name,
				depth, seqGlobalSourceBudget)...)
		}
	}
	return out
}

// seqGlobalVarWriteTypes 包内所有对 name 的赋值来源（含 writer 形参→init 实参）。
func seqGlobalVarWriteTypes(a *Actions, req CodeSequenceRequest, f *ast.File,
	pkgPath, name string, depth, budget int) []writeSource {
	if budget <= 0 || depth <= 0 {
		return nil
	}
	var out []writeSource
	seen := map[string]bool{}
	scan := func(rel string, ff *ast.File) {
		imports := importAliases(ff)
		ast.Inspect(ff, func(nd ast.Node) bool {
			as, ok := nd.(*ast.AssignStmt)
			if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
				return true
			}
			lhs, ok := as.Lhs[0].(*ast.Ident)
			if !ok || lhs.Name != name {
				return true
			}
			out = appendUnique(out, rhsTypes(a, req, ff, imports, as.Rhs[0], depth, pkgPath)...)
			// RHS 是 writer 的形参：沿 init 调用实参取类型。
			if id, ok := as.Rhs[0].(*ast.Ident); ok {
				out = appendUnique(out, seqParamCallTypes(a, req, rel, ff, pkgPath, id, depth, budget)...)
			}
			return true
		})
	}
	self := seqFileRelOf(req, f)
	if self != "" && !strings.HasPrefix(self, "<external>/") {
		scan(self, f)
	}
	for _, rel := range seqPackageFilesOf(req, self) {
		if seen[rel] || rel == self {
			continue
		}
		seen[rel] = true
		ctx := seqFileCtxFor(req, rel)
		if ctx != nil && ctx.file != nil {
			scan(rel, ctx.file)
		}
	}
	return out
}

// seqParamCallTypes writer 形参 → 调用点实参类型（只认 init/包级初始化）。
func seqParamCallTypes(a *Actions, req CodeSequenceRequest, rel string, f *ast.File,
	pkgPath string, param *ast.Ident, depth, budget int) []writeSource {
	fn := seqEnclosingFunc(f, param.Pos())
	if fn == nil || fn.Name == nil {
		return nil
	}
	idx := seqFuncParamIndex(fn, param.Name)
	if idx < 0 {
		return nil
	}
	writerID := domain.CanonicalID("symbol:go:" + pkgPath + ":" + fn.Name.Name)
	callers, err := a.repo.GetCallers(writerID, 1, MinConfidence)
	if err != nil || len(callers) > 32 {
		return nil
	}
	var out []writeSource
	for _, c := range callers {
		line := seqFactLine(c)
		if line <= 0 {
			continue
		}
		caller, err := a.repo.GetSymbol(c.SourceID)
		if err != nil || caller.FilePath == "" {
			continue
		}
		cc := seqFileCtxFor(req, caller.FilePath)
		if cc == nil {
			continue
		}
		call := seqCallAtLine(cc.file, cc.fset, line)
		if call == nil || len(call.Args) <= idx {
			continue
		}
		cfn := seqEnclosingFunc(cc.file, call.Pos())
		if cfn == nil || cfn.Name == nil || cfn.Name.Name != "init" {
			continue // 未证明实际执行（§11：跨包 init 写入）
		}
		out = appendUnique(out, rhsTypes(a, req, cc.file, importAliases(cc.file),
			call.Args[idx], depth-1, pkgOfID(caller.ID))...)
	}
	return out
}

// seqFactLine 调用边 metadata 行号（int/float64）。
func seqFactLine(f *domain.Fact) int {
	if f == nil || f.Metadata == nil {
		return 0
	}
	switch v := f.Metadata["line_num"].(type) {
	case int:
		return v
	case float64:
		return int(v)
	}
	return 0
}

// seqCallAtLine 文件内该行号的 CallExpr（取最内层一个）。
func seqCallAtLine(f *ast.File, fset *token.FileSet, line int) *ast.CallExpr {
	var hit *ast.CallExpr
	ast.Inspect(f, func(nd ast.Node) bool {
		call, ok := nd.(*ast.CallExpr)
		if ok && fset.Position(call.Pos()).Line == line {
			hit = call
		}
		return true
	})
	return hit
}

// seqEnclosingFunc 包含 pos 的函数声明。
func seqEnclosingFunc(f *ast.File, pos token.Pos) *ast.FuncDecl {
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if ok && fd.Body != nil && fd.Pos() <= pos && pos <= fd.End() {
			return fd
		}
	}
	return nil
}

// seqFuncParamIndex 形参名 → 位置（普通参数）。
func seqFuncParamIndex(fn *ast.FuncDecl, name string) int {
	i := 0
	for _, fl := range fn.Type.Params.List {
		for _, nm := range fl.Names {
			if nm.Name == name {
				return i
			}
			i++
		}
	}
	return -1
}

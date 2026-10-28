package action

// todo24 §11.2：同包函数/工厂返回类型解析（从 action_seq_dataflow.go 拆出）。
//
// 规则：当前文件有同名顶层函数声明 → 只分析该声明；没有才按包目录定位兄弟
// 文件；多重/冲突声明与缺失工厂保守返回 unknown（不挑第一个）。AST 对象证明
// 是局部函数值/类型时不得回退同名包函数（由 callReturnTypes 判断）。

import (
	"go/ast"
	"strings"
)

// funcReturnTypes 同包函数声明 → 返回类型来源。当前文件有同名声明时**只**
// 分析该声明；没有时按包身份定位同包兄弟文件的工厂（todo24 §11.2：裸函数名
// 跨文件；多重/冲突声明保守返回 unknown，不猜）。
func funcReturnTypes(a *Actions, req CodeSequenceRequest, f *ast.File, imports map[string]string, name string, depth int, curPkg string) []writeSource {
	if fd, count := findFuncDeclInFile(f, name); count > 0 {
		if count > 1 {
			return nil
		}
		return funcDeclReturnTypes(a, req, f, imports, fd, depth, curPkg)
	}
	// 外部包没有"同包兄弟文件"的仓库路径（只解析到定义文件），保持 unknown。
	if strings.HasPrefix(req.curFile, "<external>/") {
		return nil
	}
	self := seqFileRelOf(req, f)
	var hitCtx *seqFileCtx
	var hitDecl *ast.FuncDecl
	hits := 0
	for _, rel := range seqPackageFilesOf(req, self) {
		if rel == self {
			continue
		}
		fctx := seqFileCtxFor(req, rel)
		if fctx == nil || fctx.file == nil {
			continue
		}
		fd, count := findFuncDeclInFile(fctx.file, name)
		if count == 0 {
			continue
		}
		hits += count
		if hitDecl == nil {
			hitCtx, hitDecl = fctx, fd
		}
	}
	if hits == 0 || hits > 1 {
		// 缺失工厂 / 兄弟文件同名冲突（非法 Go）：保守返回 unknown，不挑第一个。
		return nil
	}
	return funcDeclReturnTypes(a, req, hitCtx.file, importAliases(hitCtx.file), hitDecl, depth, curPkg)
}

// findFuncDeclInFile 文件里的顶层函数声明（方法不算裸函数名；>1 即非法/冲突）。
func findFuncDeclInFile(f *ast.File, name string) (*ast.FuncDecl, int) {
	if f == nil {
		return nil, 0
	}
	var fd *ast.FuncDecl
	count := 0
	for _, decl := range f.Decls {
		d, ok := decl.(*ast.FuncDecl)
		if !ok || d.Name == nil || d.Name.Name != name || d.Recv != nil {
			continue
		}
		count++
		if fd == nil {
			fd = d
		}
	}
	return fd, count
}

// seqFileRelOf AST 文件 → 仓库相对路径（解析缓存里反查；找不到用 curFile）。
func seqFileRelOf(req CodeSequenceRequest, f *ast.File) string {
	for rel, fc := range req.fileCache {
		if fc != nil && fc.file == f {
			return rel
		}
	}
	if req.curCtx != nil && req.curCtx.file == f {
		return req.curCtx.rel
	}
	return req.curFile
}

// funcDeclReturnTypes 函数体 return RHS 优先（"声明返回接口、函数体 return
// 具体实现"比返回类型声明更具体）；无 return 再退回返回类型声明。
func funcDeclReturnTypes(a *Actions, req CodeSequenceRequest, f *ast.File, imports map[string]string, fd *ast.FuncDecl, depth int, curPkg string) []writeSource {
	if fd == nil || fd.Body == nil {
		return nil
	}
	var out []writeSource
	for _, st := range fd.Body.List {
		if ret, ok := st.(*ast.ReturnStmt); ok {
			for _, r := range ret.Results {
				out = appendUnique(out, rhsTypes(a, req, f, imports, r, depth, curPkg)...)
			}
		}
	}
	if len(out) > 0 {
		return out
	}
	if fd.Type.Results != nil {
		for _, r := range fd.Type.Results.List {
			out = appendUnique(out, typeSources(r.Type, imports, curPkg)...)
		}
	}
	return out
}

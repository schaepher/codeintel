package action

import (
	"go/ast"
	"os"
	"path/filepath"
	"strings"
)

// seqExternalPackageFrom 同 seqExternalPackage，可指定 go list 的 cwd：
// 从**外部包内部**继续解析它自己的导入时，用该外部包的模块根（否则主模块
// go.mod 里没有该依赖的 require 时会解析失败——"外部包内部跨包调用"）。
func seqExternalPackageFrom(a *Actions, req CodeSequenceRequest, importPath, workDir string) *seqExternalPkg {
	if workDir == "" {
		workDir = req.RepoAbs
	}
	key := importPath
	if workDir != req.RepoAbs {
		key = workDir + "\x00" + importPath
	}
	if req.extPkgs != nil {
		if p, ok := req.extPkgs[key]; ok {
			return p
		}
	}
	p := &seqExternalPkg{rel: "<external>/" + importPath}
	if req.acc != nil {
		req.acc.extLoads++
	}
	p.dir, p.std, p.err = seqGoListPkg(req.ctx(), workDir, importPath)
	if p.err == nil && p.dir != "" && !p.std {
		p.files = parseSeqDir(req.ctx(), p.dir, p.rel)
	}
	if req.extPkgs != nil {
		req.extPkgs[key] = p
	}
	return p
}

// seqExternalWorkDir 当前展开位置的 go list cwd：外部包内部用该包的模块根。
func seqExternalWorkDir(req CodeSequenceRequest) string {
	if req.curCtx != nil && req.curCtx.dir != "" &&
		strings.HasPrefix(req.curCtx.rel, "<external>/") {
		return seqModuleRoot(req.curCtx.dir)
	}
	return req.RepoAbs
}

// seqModuleRoot 从目录向上找最近的 go.mod（找不到返回 dir 本身）。
func seqModuleRoot(dir string) string {
	cur := dir
	for {
		if _, err := os.Stat(filepath.Join(cur, "go.mod")); err == nil {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return dir
		}
		cur = parent
	}
}

// seqExpandExternalMethodCall 按需展开外部依赖**方法**调用（`extT.Method()`）：
// 接收者类型来自本函数内的类型数据流（声明/复合字面量/构造器返回）或当前
// receiver 字段声明；只对**仓库外**包生效。
func seqExpandExternalMethodCall(a *Actions, req CodeSequenceRequest, node *CodeSeqNode,
	call *ast.CallExpr, depth int, curFile string, line int) bool {
	if call == nil {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel == nil {
		return false
	}
	ref, ok := seqReceiverTypeRef(a, req, req.curCtx, sel)
	if !ok || ref.pkgPath == "" || req.pkgSet[ref.pkgPath] {
		return false
	}
	extID := "symbol:go:" + ref.pkgPath + ":(" + ref.typeName + ")." + sel.Sel.Name
	return seqExpandExternalDecl(a, req, node, ref.pkgPath, "("+ref.typeName+")."+sel.Sel.Name,
		extID, depth, call, curFile, line, ref.typeName)
}

// seqStructFieldTypes 外部包某结构体的字段类型名（receiver 字段调用的
// `x.Field.Method()` 具体化用；只到类型名，不解析完整类型）。
func seqStructFieldTypes(req CodeSequenceRequest, ctx *seqFileCtx, typeName string) map[string]string {
	if ctx == nil || ctx.file == nil || typeName == "" {
		return nil
	}
	for _, decl := range ctx.file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, sp := range gd.Specs {
			tsp, ok := sp.(*ast.TypeSpec)
			if !ok || tsp.Name == nil || tsp.Name.Name != typeName {
				continue
			}
			st, ok := tsp.Type.(*ast.StructType)
			if !ok || st.Fields == nil {
				return nil
			}
			out := map[string]string{}
			for _, fl := range st.Fields.List {
				for _, nm := range fl.Names {
					out[nm.Name] = exprTypeName(fl.Type)
				}
			}
			return out
		}
	}
	return nil
}

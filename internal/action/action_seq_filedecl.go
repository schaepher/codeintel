package action

import "go/ast"
import "go/parser"
import "go/token"
import "os"
import "path/filepath"
import "github.com/schaepher/codeintel/internal/domain"

// loadSeqFuncDecl 按 canonical ID 读源码并定位函数声明（名字 + receiver 精确
// 匹配；不按可能陈旧的行号切片）。
func loadSeqFuncDecl(a *Actions, req CodeSequenceRequest, symID string) (*seqFileCtx, *ast.FuncDecl, bool) {
	n, err := a.repo.GetSymbol(domain.CanonicalID(symID))
	if err != nil || n == nil || n.FilePath == "" {
		return nil, nil, false
	}
	ctx := seqFileCtxFor(req, n.FilePath)
	if ctx == nil {
		return nil, nil, false
	}
	recvType, funcName, isMethod := splitDeclName(seqDeclNameOf(symID))
	for _, decl := range ctx.file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Name == nil || fd.Name.Name != funcName {
			continue
		}
		if isMethod {
			if fd.Recv == nil || len(fd.Recv.List) == 0 ||
				recvTypeName(fd.Recv.List[0].Type) != recvType {
				continue
			}
		} else if fd.Recv != nil {
			continue
		}
		return ctx, fd, true
	}
	return nil, nil, false
}

// seqFileCtxFor 取（并缓存）仓库文件的解析上下文。
func seqFileCtxFor(req CodeSequenceRequest, rel string) *seqFileCtx {
	if fc := req.fileCache[rel]; fc != nil {
		if req.acc != nil {
			req.acc.cacheHits++
		}
		return &seqFileCtx{fset: fc.fset, file: fc.file, src: fc.src, rel: rel}
	}
	src, err := os.ReadFile(filepath.Join(req.RepoAbs, rel))
	if err != nil {
		return nil
	}
	if req.acc != nil {
		req.acc.cacheMisses++
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, src, parser.ParseComments)
	if err != nil {
		return nil
	}

	if req.fileCache != nil {
		req.fileCache[rel] = &seqParsedFile{fset: fset, file: f, src: src}
	}
	return &seqFileCtx{fset: fset, file: f, src: src, rel: rel}
}

// seqDeclNameOf canonical ID → 声明名（`(T).m` / `Func`）。
func seqDeclNameOf(symID string) string {
	if i := lastColon(symID); i >= 0 {
		return symID[i+1:]
	}
	return symID
}

// seqTargetsCached 按函数缓存"调用点 → 目标"映射（字面量内联展开会反复用到
// 同一个外层函数的映射）。
func seqTargetsCached(a *Actions, req CodeSequenceRequest, symID string) *seqTargets {
	if req.tgtsCache != nil {
		if t, ok := req.tgtsCache[symID]; ok {
			return t
		}
	}
	t := seqTargetsFor(a, req, symID)
	if req.tgtsCache != nil {
		req.tgtsCache[symID] = t
	}
	return t
}

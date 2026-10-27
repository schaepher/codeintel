package action

// todo22 §2.3/§2.4：工厂返回函数值 + 函数列表预算。
//
// 「工厂」= 任何被调用的函数（具名函数/局部函数值/字面量）：它的 return 表达式
// 决定调用结果是什么函数值。解析只按**准确符号 + return 语句**，不从函数名
// 猜返回类型；多条 return（条件返回）保留为未消歧的多候选。
//
// 工厂本身的副作用由调用节点的正常展开体现（与"返回函数被调用"分开）。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"

	"github.com/schaepher/codeintel/internal/domain"
)

// 预算（todo22 §2.4：路径 ≤16、每条 ≤32 项）。
const (
	seqMaxListItems    = 32
	seqMaxRangeIters   = 16
	seqMaxFactoryDepth = 3
)

// seqCalleeReturnValues 解析一次调用的返回值：函数值候选 + 是否解析成功。
func seqCalleeReturnValues(a *Actions, req CodeSequenceRequest, ctx *seqFileCtx,
	call *ast.CallExpr, depth int) ([]seqValue, bool) {
	// ① 被调者本身是函数值（局部变量/形参/字面量）——按值的身份解析
	if fv, ok := seqCallFuncValue(a, req, call); ok {
		return seqValueReturnValues(a, req, ctx, call, fv, depth)
	}
	// ② 包级函数：AST 对象身份/import 路径 → 准确符号
	curPkg := pkgPathOf(req.curSym)
	symID, ok := seqASTCallTarget(call.Fun, ctx.file, curPkg)
	if !ok || !seqTargetIndexed(req, curPkg, symID) {
		return nil, false
	}
	fileCtx, fn, ok := loadSeqFuncDecl(a, req, symID)
	if !ok {
		return nil, false
	}
	env := newSeqEnv(nil)
	bindSeqParams(env, fn.Type.Params, captureSeqClosureArgs(a, req, ctx, call))
	return seqReturnValuesOf(a, req, fileCtx, fn, env, depth)
}

// seqValueReturnValues 函数值（字面量/具名/列表/多候选）的返回候选。
func seqValueReturnValues(a *Actions, req CodeSequenceRequest, ctx *seqFileCtx,
	call *ast.CallExpr, fv seqValue, depth int) ([]seqValue, bool) {
	switch fv.kind {
	case seqValueLit:
		if fv.lit == nil || fv.lit.Body == nil {
			return nil, false
		}
		env := newSeqEnv(fv.env)
		bindSeqParams(env, fv.lit.Type.Params, captureSeqClosureArgs(a, req, ctx, call))
		sub := req
		sub.curSym = fv.enclosing
		sub.env = env
		return seqReturnValuesOf(a, sub, fv.ctx, &ast.FuncDecl{Type: fv.lit.Type, Body: fv.lit.Body}, env, depth)
	case seqValueNamed:
		fileCtx, fn, ok := loadSeqFuncDecl(a, req, fv.symID)
		if !ok {
			return nil, false
		}
		env := newSeqEnv(nil)
		bindSeqParams(env, fn.Type.Params, captureSeqClosureArgs(a, req, ctx, call))
		sub := req
		sub.curSym = fv.symID
		sub.env = env
		return seqReturnValuesOf(a, sub, fileCtx, fn, env, depth)
	case seqValueMulti:
		// 条件返回的多候选（上一次折叠的结果）——逐候选解析返回值
		var out []seqValue
		okAny := false
		for _, cand := range fv.cands {
			vals, ok := seqValueReturnValues(a, req, ctx, call, cand, depth)
			if ok {
				okAny = true
				out = append(out, vals...)
			}
		}
		return out, okAny
	}
	return nil, false
}

// seqReturnValuesOf 收集函数体（不含嵌套字面量体）里每个 return 表达式的
// 函数值——条件返回 → 多候选（不任选一条）。
func seqReturnValuesOf(a *Actions, req CodeSequenceRequest, fileCtx *seqFileCtx,
	fn *ast.FuncDecl, env *seqEnv, depth int) ([]seqValue, bool) {
	if fn == nil || fn.Body == nil || depth >= seqMaxFactoryDepth {
		return nil, false
	}
	var vals []seqValue
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if _, isLit := n.(*ast.FuncLit); isLit {
			return false // 字面量体内的 return 属于该字面量，不是工厂本身
		}
		rs, ok := n.(*ast.ReturnStmt)
		if !ok {
			return true
		}
		for _, r := range rs.Results {
			sub := req
			sub.env = env
			vals = append(vals, captureSeqClosureValueDepth(a, sub, fileCtx, r, depth+1))
		}
		return true
	})
	return vals, len(vals) > 0
}

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
		return &seqFileCtx{fset: fc.fset, file: fc.file, src: fc.src, rel: rel}
	}
	src, err := os.ReadFile(filepath.Join(req.RepoAbs, rel))
	if err != nil {
		return nil
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, src, 0)
	if err != nil {
		return nil
	}
	req.fileCache[rel] = &seqParsedFile{fset: fset, file: f, src: src}
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

// seqRangeElems range 的对象是否是已知函数列表（统一走 captureSeqCallbackLists
// ——只认确定的列表；多候选不套用"按顺序展开"）。
func seqRangeElems(a *Actions, req CodeSequenceRequest, x ast.Expr) (seqValue, bool) {
	if req.env == nil {
		return seqValue{}, false
	}
	return captureSeqCallbackLists(a, req, req.curCtx, x)
}

// seqRangeOverFuncList 按列表元素顺序展开循环体（每次迭代绑定元素值；
// 循环体里的赋值对下一次迭代生效 = 折叠）。超过迭代预算 → 明确诊断，
// 不截取前几项冒充完整列表。
func seqRangeOverFuncList(a *Actions, req CodeSequenceRequest, fset *token.FileSet, src []byte,
	s *ast.RangeStmt, list seqValue, tgts *seqTargets, depth int, recvType string,
	recvDecl map[string]string, f *ast.File, curPkg, curFile string) *CodeSeqNode {
	node := &CodeSeqNode{Kind: "loop", Label: "range " + seqExprText(fset, src, s.X),
		Line: fset.Position(s.Pos()).Line}
	if list.why != "" {
		node.Stop = &SeqStop{Code: SeqStopFuncValueUnbound, Detail: list.why}
		return node
	}
	elems := list.elems
	if len(elems) > seqMaxRangeIters {
		node.Stop = &SeqStop{Code: SeqStopFuncValueUnbound,
			Detail: "函数列表迭代超过预算 " + itoaSeq(seqMaxRangeIters) + "（todo22 §2.4）"}
		return node
	}
	for _, elem := range elems {
		if req.env != nil {
			if id, ok := s.Value.(*ast.Ident); ok && id.Obj != nil {
				req.env.set(id.Obj, elem)
			}
			if id, ok := s.Key.(*ast.Ident); ok && id.Obj != nil {
				req.env.set(id.Obj, unknownSeqValue("range 索引（与函数值执行无关）"))
			}
		}
		node.Nodes = append(node.Nodes, walkStmts(a, req, fset, src, s.Body.List, tgts,
			depth, recvType, recvDecl, f, curPkg, curFile)...)
	}
	return node
}

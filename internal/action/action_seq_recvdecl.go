package action

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"time"

	"github.com/schaepher/codeintel/internal/domain"
)

// codeSeqForSymbol 按符号 ID 解析函数体（递归入口——嵌套展开用
// canonical ID 直接解析；depth = 当前剩余嵌套层级）。
//
// todo10-3：返回 (节点, 终点诊断)——无法展开时不再返回"含义不明的 nil"，
// 而是带 code 的诊断（索引缺失/源码缺失/定位失败/解析失败/循环）。
func codeSeqForSymbol(a *Actions, req CodeSequenceRequest, symID string, depth int) (*CodeSeqNode, *SeqStop) {
	if req.acc == nil {
		req.acc = &seqTimingAcc{}
	}
	// todo10-9：取消检查（递归入口）——被取消的展开不报告为分析完成
	if req.ctx().Err() != nil {
		req.acc.cancel = true
		return nil, nil
	}
	visitKey := seqVisitKey(req, symID)
	if req.visited[visitKey] {
		return nil, &SeqStop{Code: SeqStopCycle, Detail: "已在展开路径上（防环）"}
	}
	req.visited[visitKey] = true
	defer delete(req.visited, visitKey)
	symStart := time.Now()
	n, err := a.repo.GetSymbol(domain.CanonicalID(symID))
	req.acc.symLookups++
	req.acc.sqlQueries++
	req.acc.symDur += time.Since(symStart)
	if err != nil {
		return nil, &SeqStop{Code: SeqStopIndexGap, Detail: symID + "（索引中没有该符号）"}
	}
	if n.FilePath == "" {
		return nil, &SeqStop{Code: SeqStopIndexGap, Detail: symID + "（节点无 file_path——生成/虚拟符号）"}
	}
	// 阶段 2：源码读取 + 语法解析（todo10-9 计时）
	readStart := time.Now()
	var src []byte
	var fset *token.FileSet
	var f *ast.File
	if fc := req.fileCache[n.FilePath]; fc != nil {
		req.acc.cacheHits++
		src, fset, f = fc.src, fc.fset, fc.file
	} else {
		req.acc.cacheMisses++
		src, err = os.ReadFile(filepath.Join(req.RepoAbs, n.FilePath))
		if err != nil {
			req.acc.read += time.Since(readStart)
			return nil, &SeqStop{Code: SeqStopSourceMiss, Detail: n.FilePath}
		}
		fset = token.NewFileSet()
		// 解析注释：DI 字段注入注解（`// @inject store`）等证据在 Doc 上，
		// 不带 ParseComments 时注释不可见（§3.3 注释形式会静默失效）。
		f, err = parser.ParseFile(fset, n.FilePath, src, parser.ParseComments)
		if err != nil {
			req.acc.read += time.Since(readStart)
			return nil, &SeqStop{Code: SeqStopParseError, Detail: n.FilePath + ": " + err.Error()}
		}
		req.fileCache[n.FilePath] = &seqParsedFile{fset: fset, file: f, src: src}
	}
	req.acc.read += time.Since(readStart)
	// 阶段 3：定位函数 + 取调用点 + 遍历展开（walk 墙钟由 CodeSequence
	// 外层统一推导——这里只累加 read，避免递归时重复计数）
	// 定位目标函数（LineStart 在函数体范围内）
	var fn *ast.FuncDecl
	ast.Inspect(f, func(node ast.Node) bool {
		if fd, ok := node.(*ast.FuncDecl); ok {
			start := fset.Position(fd.Pos()).Line
			end := fset.Position(fd.End()).Line
			if n.LineStart >= start && n.LineStart <= end {
				fn = fd
				return false
			}
		}
		return true
	})
	if fn == nil {
		return nil, &SeqStop{Code: SeqStopFuncNotFound,
			Detail: n.FilePath + ":" + itoaSeq(n.LineStart) + "（索引行号处无函数声明）"}
	}

	// todo10-3：调用点映射用**原始 calls 边**（具体化留给 walk 层，才能
	// 记录候选集合与 ambiguous）。todo22 §2：抽成 seqTargetsFor——函数字面量
	// 内联展开时也要用**字面量所在函数**的调用边（索引把闭包内的调用记在
	// 外层函数上）。
	tgts := seqTargetsCached(a, req, symID)

	recvType := ""

	recvDecl := map[string]string{}
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		recvType = recvTypeName(fn.Recv.List[0].Type)
		ast.Inspect(f, func(node ast.Node) bool {
			ts, ok := node.(*ast.TypeSpec)
			if !ok || ts.Name.Name != recvType {
				return true
			}
			if st, ok := ts.Type.(*ast.StructType); ok {
				for _, fl := range st.Fields.List {
					if len(fl.Names) == 1 {
						recvDecl[fl.Names[0].Name] = exprTypeName(fl.Type)
					}
				}
				return false
			}
			return true
		})
	}
	// todo10-2：具名结果槽（return 节点的 slots 用）+ 本函数焦点写入行
	// （按值传给递归——子函数各自覆盖，不会串用父函数的行号）
	req.resultNames = resultNamesOf(fn.Type.Results)
	// todo11-2：本次调用的入站实参（形参 → 值只沿当前展开路径取，不跨分支）
	req.incomingArgs = req.callArgLits
	req.incomingSrc = req.callArgSrcs
	req.curBody = fn.Body
	req.curParams = fn.Type.Params
	req.curFile = n.FilePath
	// todo22 §2：函数身份/文件上下文/值环境（形参值来自调用点实参绑定；
	// 入口函数或外部调用者没有绑定 → 形参是明确的 unknown，而不是猜）。
	req.curSym = symID
	req.curTgts = tgts
	req.curRecvType = recvType
	req.curRecvDecl = recvDecl
	req.curCtx = &seqFileCtx{fset: fset, file: f, src: src, rel: n.FilePath}
	req.sceneConsts = seqConstValues(a, req, n.FilePath)
	env := newSeqEnv(nil)
	bindSeqParams(env, fn.Type.Params, req.paramValues)
	seqRecordParamTypes(req, env, fn.Type.Params)
	req.env = env
	req.paramValues = nil
	// todo10-5：一层条件传播——调用点实参字面量按形参名对齐（下次展开覆盖）
	req.sceneArgs = paramLiteralArgs(fn.Type.Params, req.callArgLits)
	req.sceneSrc = paramSourceArgs(fn.Type.Params, req.callArgLits, req.callArgSrcs)
	req.sceneWhy = paramReasonArgs(fn.Type.Params, req.callArgLits, req.callArgWhy)
	// todo22 P1：结构体字段值沿实参传播（`Submit(req)` → 被调函数的
	// `req.Source` 条件可按调用点的已知字段值判定）
	if len(req.callArgFields) > 0 {
		if req.sceneArgs == nil {
			req.sceneArgs = map[string]string{}
		}
		if req.sceneSrc == nil {
			req.sceneSrc = map[string]string{}
		}
		if req.sceneWhy == nil {
			req.sceneWhy = map[string]string{}
		}
	}
	applySceneFieldArgs(req.sceneArgs, req.sceneSrc, req.sceneWhy, fn.Type.Params, req.callArgFields)
	if req.focusActive() {
		// todo12-9：焦点行 = SSA 字段写入 + 目标值/字段的到达定义闭包
		var unknown int
		req.focusLines, unknown = a.focusLinesForFunc(domain.CanonicalID(symID), fn, fset, src, req.focus)
		req.acc.focusUnresolved += unknown
	} else {
		req.focusLines = nil
	}
	root := &CodeSeqNode{Kind: "call", Label: fn.Name.Name, Line: n.LineStart,
		Evidence: &SeqEvidence{Source: SeqEvidenceAST, SymbolID: symID,
			File: n.FilePath, Line: n.LineStart},
	}
	unresolvedBefore := req.acc.unresolved
	root.Nodes = walkStmts(a, req, fset, src, fn.Body.List, tgts, depth, recvType, recvDecl, f, pkgPathOf(symID), n.FilePath)
	// todo10-3：没有展开任何内部调用时，区分"业务流程结束"与"调用点未解析"
	if len(root.Nodes) == 0 {
		if req.acc.unresolved > unresolvedBefore {
			root.Stop = &SeqStop{Code: SeqStopUnresolved,
				Detail: itoaSeq(req.acc.unresolved-unresolvedBefore) + " 个调用点未在索引中（外部库或未索引）"}
		} else {
			root.Stop = &SeqStop{Code: SeqStopNoCalls}
		}
	}
	return root, nil
}

// resultNamesOf 函数签名的具名结果（无具名/无结果返回空——此时 return
// 节点用 #序号 作槽名）。
func resultNamesOf(fl *ast.FieldList) []string {
	if fl == nil {
		return nil
	}
	var out []string
	for _, f := range fl.List {
		// 同组多名 `(state, reason string)` 必须逐名展开——曾误用
		// f.Names[0] 复制，两个槽都叫 state（todo12-12）。
		for _, nm := range f.Names {
			out = append(out, nm.Name)
		}
		if len(f.Names) == 0 {
			out = append(out, "")
		}
	}
	return out
}

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
	if req.visited[symID] {
		return nil, &SeqStop{Code: SeqStopCycle, Detail: "已在展开路径上（防环）"}
	}
	req.visited[symID] = true
	defer delete(req.visited, symID)
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
		f, err = parser.ParseFile(fset, n.FilePath, src, 0)
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

	// todo10-3：调用点映射用**原始 calls 边**（不预先做接口具体化）——
	// 具体化留给 walk 层，才能记录候选集合与 ambiguous；否则
	// CalleesConcrete 会静默把接口调用替换成"第一个实现"。
	tgts := &seqTargets{}
	sqlStart := time.Now()
	// todo21-14.3：请求上下文下的调用边走查（一跳 = 直接邻接查询；取消
	// 要能终止实际 SQL，而不是只在外层返回）——阈值沿用 MinConfidence。
	facts, calleeErr := a.repo.GetCalleesContext(req.ctx(), domain.CanonicalID(symID), 1, MinConfidence)
	req.acc.sqlQueries++
	req.acc.sqlDur += time.Since(sqlStart)
	if calleeErr == nil {
		for _, f := range facts {
			if ln, ok := f.Metadata["line_num"].(float64); ok {
				if tgts.byLine == nil {
					tgts.byLine = map[int]seqTarget{}
				}
				tgts.byLine[int(ln)] = seqTarget{tid: string(f.TargetID), confidence: f.Confidence}
			}
			if p, ok := f.Metadata["pos"].(float64); ok {
				if tgts.byPos == nil {
					tgts.byPos = map[int]seqTarget{}
				}
				tgts.byPos[int(p)] = seqTarget{tid: string(f.TargetID), confidence: f.Confidence}
			}
			// todo20-9：边按 (caller,target,kind) 汇总——重复调用只有首个
			// 位置的 metadata。保留目标集合供未命中位置按调用名回退。
			if name := seqTargetName(string(f.TargetID)); name != "" {
				if tgts.byName == nil {
					tgts.byName = map[string][]seqTarget{}
				}
				dup := false
				for _, e := range tgts.byName[name] {
					if e.tid == string(f.TargetID) {
						dup = true
						break
					}
				}
				if !dup {
					tgts.byName[name] = append(tgts.byName[name], seqTarget{tid: string(f.TargetID), confidence: f.Confidence})
				}
			}
		}
	}

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
	// todo10-5：一层条件传播——调用点实参字面量按形参名对齐（下次展开覆盖）
	req.sceneArgs = paramLiteralArgs(fn.Type.Params, req.callArgLits)
	req.sceneSrc = paramSourceArgs(fn.Type.Params, req.callArgLits, req.callArgSrcs)
	req.sceneWhy = paramReasonArgs(fn.Type.Params, req.callArgLits, req.callArgWhy)
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

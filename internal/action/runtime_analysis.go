package action

// todo25 §13：请求级运行时快照分析（入口函数符号执行 → 分支裁剪 + 值证据）。
//
// 与分析树共用同一份 AST 缓存（fileCache），分支/调用节点用 *ast 指针身份
// 关联；静态常量/字面量优先，快照只补充静态无法确定的值。

import (
	"go/ast"

	"github.com/schaepher/codeintel/internal/domain"
)

type runtimeAnalysis struct {
	snap         *RuntimeSnapshot
	req          CodeSequenceRequest
	branch       map[*ast.IfStmt]*SceneFit
	facts        map[*ast.CallExpr]*SeqRuntimeEvidence
	queries      map[*ast.CallExpr]*runtimeQuery
	rowQueries   map[*ast.Object]*runtimeQuery
	cursor       map[*ast.Object]rtValue
	decisions    map[*ast.IfStmt]*rtDecision
	globals      map[*ast.Object]*rtGlobal
	globalByName map[string]*rtGlobal
	pkgVars      map[*ast.Object]bool
	// U04d：包级变量名（跨文件对象解析用）与写入资源结论。
	pkgVarNames map[string]bool
	// dbHandleNames 包级 *sql.DB 句柄名（U04d：未知效果只失效这些绑定）。
	dbHandleNames map[string]bool
	writeRes      map[*ast.CallExpr][]*SeqStorageResource
	resource      map[*ast.CallExpr]*SeqStorageResource
}

type rtGlobal struct {
	value   rtValue
	mutated bool
}

type rtDecision struct {
	trueN    int
	falseN   int
	unknownN int
}

// runRuntimeAnalysis 从入口符号执行；错误属于输入/结构问题 → 查询失败。
func (a *Actions) runRuntimeAnalysis(req CodeSequenceRequest, entryID string) (*runtimeAnalysis, error) {
	snap, err := req.ResolveRuntimeSnapshot()
	if err != nil {
		return nil, err
	}
	if snap == nil {
		return nil, nil
	}
	an := &runtimeAnalysis{snap: snap, req: req,
		branch: map[*ast.IfStmt]*SceneFit{}, facts: map[*ast.CallExpr]*SeqRuntimeEvidence{},
		queries: map[*ast.CallExpr]*runtimeQuery{}, rowQueries: map[*ast.Object]*runtimeQuery{},
		cursor:    map[*ast.Object]rtValue{},
		decisions: map[*ast.IfStmt]*rtDecision{}, globals: map[*ast.Object]*rtGlobal{},
		globalByName: map[string]*rtGlobal{}, pkgVars: map[*ast.Object]bool{},
		pkgVarNames: map[string]bool{}, dbHandleNames: map[string]bool{},
		writeRes: map[*ast.CallExpr][]*SeqStorageResource{},
		resource: map[*ast.CallExpr]*SeqStorageResource{}}
	if fn, ctx, ok := seqFuncSource(a, req, entryID); ok && ctx != nil && ctx.file != nil {
		an.collectPackageVars(req, ctx.file)
		_ = fn
	}
	e := &rtEval{a: a, an: an, req: req, ctx: req.Ctx, maxDepth: runtimeEvalMaxDepth, maxStates: 64}
	if _, err := e.evalFunc(domain.CanonicalID(entryID), nil, 0); err != nil {
		return nil, err
	}
	an.finalize(req)
	return an, nil
}

func (an *runtimeAnalysis) finalize(req CodeSequenceRequest) {
	an.finalizeResources()
	for ifStmt, d := range an.decisions {
		if d == nil {
			continue
		}
		status := ""
		switch {
		case d.unknownN > 0:
			status = SceneUnknown
		case d.trueN > 0 && d.falseN == 0:
			status = SceneTaken
		case d.falseN > 0 && d.trueN == 0:
			status = SceneExcluded
		}
		if status == "" {
			continue
		}
		an.branch[ifStmt] = &SceneFit{Status: status, Reason: "runtime-snapshot",
			Source: "runtime-data:" + an.snap.Hash[:12],
			Key:    an.snap.Hash[:12]}
	}
}

func (an *runtimeAnalysis) recordBranch(ifStmt *ast.IfStmt, truth, known bool) {
	d := an.decisions[ifStmt]
	if d == nil {
		d = &rtDecision{}
		an.decisions[ifStmt] = d
	}
	if !known {
		d.unknownN++
		return
	}
	if truth {
		d.trueN++
	} else {
		d.falseN++
	}
}

func (an *runtimeAnalysis) fit(ifStmt *ast.IfStmt) (*SceneFit, bool) {
	if an == nil {
		return nil, false
	}
	f, ok := an.branch[ifStmt]
	return f, ok
}

func (an *runtimeAnalysis) fact(call *ast.CallExpr) *SeqRuntimeEvidence {
	if an == nil || call == nil {
		return nil
	}
	return an.facts[call]
}

func (an *runtimeAnalysis) globalByNameOf(id *ast.Ident) *rtGlobal {
	if id == nil {
		return nil
	}
	// U04d：包级变量按**名字**统一（同一包不同文件的对象解析结果不同：
	// 跨文件引用 Obj 为 nil，按对象键会让"另一文件写入、本文件读取"漏掉）。
	if g, ok := an.globalByName[id.Name]; ok {
		if id.Obj != nil {
			an.globals[id.Obj] = g
		}
		return g
	}
	g := &rtGlobal{value: rtUnknownValue()}
	an.globalByName[id.Name] = g
	if id.Obj != nil {
		an.globals[id.Obj] = g
	}
	return g
}

// mutateAllGlobals 未知调用/解释截断：全部包级绑定保守失效。
func (an *runtimeAnalysis) mutateAllGlobals() {
	if an == nil {
		return
	}
	for name := range an.dbHandleNames {
		g := an.globalByName[name]
		if g == nil {
			g = &rtGlobal{value: rtUnknownValue()}
			an.globalByName[name] = g
		}
		g.mutated = true
	}
}

// finalizeResources 同一 AST 调用在不同上下文结论冲突 → ambiguous_resource。
func (an *runtimeAnalysis) finalizeResources() {
	for call, list := range an.writeRes {
		if len(list) == 0 {
			continue
		}
		first := list[0]
		same := true
		for _, r := range list[1:] {
			if r.Status != first.Status || r.SourceID != first.SourceID || r.Symbol != first.Symbol {
				same = false
				break
			}
		}
		if same {
			an.resource[call] = first
			continue
		}
		an.resource[call] = &SeqStorageResource{Status: SeqResourceAmbiguous}
	}
}

// writeResource 该写入调用的资源绑定结论（nil = 未访问）。
func (an *runtimeAnalysis) writeResource(call *ast.CallExpr) *SeqStorageResource {
	if an == nil || call == nil {
		return nil
	}
	return an.resource[call]
}

// collectPackageVars 记录包级 Var/Const 的声明对象（区分函数内局部 var）。
func (an *runtimeAnalysis) collectPackageVars(req CodeSequenceRequest, f *ast.File) {
	rel := seqFileRelOf(req, f)
	for _, file := range seqPackageFilesOf(req, rel) {
		ctx := seqFileCtxFor(req, file)
		if ctx == nil || ctx.file == nil {
			continue
		}
		for _, decl := range ctx.file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, n := range vs.Names {
					if n.Obj != nil {
						an.pkgVars[n.Obj] = true
					}
					an.pkgVarNames[n.Name] = true
					if vs.Type != nil && len(vs.Values) == 0 &&
						runtimeIsSQLDBTypeExpr(vs.Type, ctx.file) {
						an.dbHandleNames[n.Name] = true
					}
				}
			}
		}
	}
}

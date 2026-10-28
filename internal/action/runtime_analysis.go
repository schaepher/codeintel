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
	snap, err := LoadRuntimeSnapshot(req.RuntimeDataPath)
	if err != nil {
		return nil, err
	}
	an := &runtimeAnalysis{snap: snap, req: req,
		branch: map[*ast.IfStmt]*SceneFit{}, facts: map[*ast.CallExpr]*SeqRuntimeEvidence{},
		queries: map[*ast.CallExpr]*runtimeQuery{}, rowQueries: map[*ast.Object]*runtimeQuery{},
		cursor:    map[*ast.Object]rtValue{},
		decisions: map[*ast.IfStmt]*rtDecision{}, globals: map[*ast.Object]*rtGlobal{},
		globalByName: map[string]*rtGlobal{}, pkgVars: map[*ast.Object]bool{}}
	if fn, ctx, ok := seqFuncSource(a, req, entryID); ok && ctx != nil && ctx.file != nil {
		an.collectPackageVars(req, ctx.file)
		_ = fn
	}
	e := &rtEval{a: a, an: an, req: req, ctx: req.Ctx, maxDepth: 8, maxStates: 64}
	if _, err := e.evalFunc(domain.CanonicalID(entryID), nil, 0); err != nil {
		return nil, err
	}
	an.finalize(req)
	return an, nil
}

func (an *runtimeAnalysis) finalize(req CodeSequenceRequest) {
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
	if id.Obj != nil {
		if g, ok := an.globals[id.Obj]; ok {
			return g
		}
		g := &rtGlobal{value: rtUnknownValue()}
		an.globals[id.Obj] = g
		return g
	}
	if g, ok := an.globalByName[id.Name]; ok {
		return g
	}
	g := &rtGlobal{value: rtUnknownValue()}
	an.globalByName[id.Name] = g
	return g
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
				}
			}
		}
	}
}

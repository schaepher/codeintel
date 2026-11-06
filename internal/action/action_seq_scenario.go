package action

// B10b：逐合法组合实际分析并输出场景树（workflow.scenarios）。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// SeqWorkflowScenario 一个合法组合的实际分析结果。
type SeqWorkflowScenario struct {
	Parameters map[string]string `json:"parameters"`
	Sequence   *CodeSeqNode      `json:"sequence,omitempty"`
	Partial    bool              `json:"partial"`
	Reasons    []string          `json:"reasons,omitempty"`
	// Workflow B10c：该组合的完整关联入口分析结果（entries/connections/instances）。
	Workflow *SeqWorkflow `json:"workflow,omitempty"`
}

// seqBuildScenarios 逐组合重分析根入口（组合参数按 Supported 调用实参关系映射）。
func (a *Actions) seqBuildScenarios(base CodeSequenceRequest, wf *SeqWorkflow) []SeqWorkflowScenario {
	if a == nil || wf == nil || base.CombinationSelector == "" || len(wf.Entries) == 0 {
		return nil
	}
	root := wf.Entries[0]
	combos, ok := a.seqSelectorCombosDetail(base, base.CombinationSelector)
	if !ok {
		return nil
	}
	entryParams, entryDecl := seqFuncParamsOf(a, root.Repo, root.Symbol)
	mapping := seqSelectorParamMapping(a, root.Repo, root.Symbol, base.CombinationSelector,
		entryParams)
	if len(mapping) == 0 {
		return nil
	}
	var out []SeqWorkflowScenario
	for _, combo := range combos {
		seed := make([]*SeqStorageOrigin, len(entryParams))
		for selParam, idx := range mapping {
			v, ok := combo[selParam]
			if !ok || idx < 0 || idx >= len(seed) {
				continue
			}
			seed[idx] = &SeqStorageOrigin{Kind: SeqOriginLiteral, ScalarType: "string",
				Literal: strconv.Quote(v)}
		}
		sub := base
		sub.Target = root.Symbol
		// B10c：组合场景包含同一组合的完整关联入口流程（复用单流程分析）。
		sub.RelatedEntries = base.RelatedEntries
		sub.CombinationSelector = ""
		sub.FrontendRoots = base.FrontendRoots
		sub.acc = base.acc
		sub.pkgSet = base.pkgSet
		sub.visited = map[string]bool{}
		sub.entryID = ""
		sub.runtime = nil
		sub.seedParamOrigs = seed
		sub.seedVarLits = nil
		sub.scanSeeds = nil
		tree, err := a.CodeSequence(sub)
		sc := SeqWorkflowScenario{Parameters: combo}
		if err != nil || tree == nil {
			sc.Partial = true
			sc.Reasons = appendUniqueStr(sc.Reasons, "scenario_analysis_failed")
			out = append(out, sc)
			continue
		}
		sc.Sequence = tree
		sc.Workflow = tree.Workflow
		sc.Partial, sc.Reasons = seqScenarioPartial(tree)
		if sc.Workflow != nil && sc.Workflow.Partial {
			sc.Partial = true
			sc.Reasons = appendUniqueStr(sc.Reasons, "scenario_workflow_partial")
		}
		out = append(out, sc)
	}
	_ = entryDecl
	return out
}

// seqScenarioPartial 场景树是否带 depth/candidate 等缺口。
func seqScenarioPartial(tree *CodeSeqNode) (bool, []string) {
	if tree == nil {
		return true, []string{"scenario_missing"}
	}
	var reasons []string
	var walk func(nodes []*CodeSeqNode)
	walk = func(nodes []*CodeSeqNode) {
		for _, n := range nodes {
			if n == nil {
				continue
			}
			if n.Stop != nil {
				switch n.Stop.Code {
				case SeqStopDepthLimit, SeqStopIfaceAmbiguous, SeqStopCandidateBudget,
					SeqStopFuncValueUnbound, SeqStopUnresolved, SeqStopIndexGap:
					reasons = appendUniqueStr(reasons, n.Stop.Code)
				}
			}
			if n.Candidate {
				reasons = appendUniqueStr(reasons, "candidate")
			}
			walk(n.Nodes)
			walk(n.Else)
		}
	}
	walk([]*CodeSeqNode{tree})
	if tree.Meta != nil {
		for _, d := range tree.Meta.Diagnostics {
			if strings.Contains(d.Code, "depth") || strings.Contains(d.Code, "budget") {
				reasons = appendUniqueStr(reasons, d.Code)
			}
		}
	}
	return len(reasons) > 0, reasons
}

// seqFuncParamsOf 入口函数参数名列表（不含 receiver）。
func seqFuncParamsOf(a *Actions, repo, entry string) ([]string, *ast.FuncDecl) {
	n, err := a.repo.GetSymbol(domain.CanonicalID(entry))
	if err != nil || n.FilePath == "" {
		return nil, nil
	}
	src, err := os.ReadFile(filepath.Join(repo, n.FilePath))
	if err != nil {
		return nil, nil
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, n.FilePath, src, 0)
	if err != nil {
		return nil, nil
	}
	fn := findFuncByLine(f, fset, n.LineStart)
	if fn == nil {
		return nil, nil
	}
	var out []string
	for _, fld := range fn.Type.Params.List {
		if len(fld.Names) == 0 {
			out = append(out, "")
			continue
		}
		for _, nm := range fld.Names {
			out = append(out, nm.Name)
		}
	}
	return out, fn
}

// seqSelectorParamMapping 选择器形参名 → 入口形参下标（按 Supported(...) 实参）。
func seqSelectorParamMapping(a *Actions, repo, entry, selector string, entryParams []string) map[string]int {
	n, err := a.repo.GetSymbol(domain.CanonicalID(selector))
	if err != nil || n.FilePath == "" {
		return nil
	}
	selParams, selFn := seqFuncParamsOf(a, repo, selector)
	if selFn == nil {
		return nil
	}
	selName := pkgNameOfID(selector)
	en, err := a.repo.GetSymbol(domain.CanonicalID(entry))
	if err != nil || en.FilePath == "" {
		return nil
	}
	src, err := os.ReadFile(filepath.Join(repo, en.FilePath))
	if err != nil {
		return nil
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, en.FilePath, src, 0)
	if err != nil {
		return nil
	}
	fn := findFuncByLine(f, fset, en.LineStart)
	if fn == nil || fn.Body == nil {
		return nil
	}
	entryIdx := map[string]int{}
	for i, p := range entryParams {
		entryIdx[p] = i
	}
	mapping := map[string]int{}
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(mapping) > 0 {
			return true
		}
		funName := ""
		switch fun := call.Fun.(type) {
		case *ast.Ident:
			funName = fun.Name
		case *ast.SelectorExpr:
			funName = fun.Sel.Name
		}
		if funName != selName {
			return true
		}
		for i, arg := range call.Args {
			if i >= len(selParams) {
				break
			}
			id, ok := runtimeUnparen(arg).(*ast.Ident)
			if !ok {
				continue
			}
			if idx, ok := entryIdx[id.Name]; ok {
				mapping[selParams[i]] = idx
			}
		}
		return false
	})
	return mapping
}

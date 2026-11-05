package action

// B10：合法组合自动选择与覆盖报告（选择器 = 源码函数；不在配置里抄写矩阵）。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"

	"github.com/schaepher/codeintel/internal/domain"
)

// SeqWorkflowCoverage 覆盖统计（合法组合数 / 实际分析数 / 完整数 / partial）。
type SeqWorkflowCoverage struct {
	Supported int      `json:"supported"`
	Analyzed  int      `json:"analyzed"`
	Complete  int      `json:"complete"`
	Partial   int      `json:"partial"`
	Reasons   []string `json:"reasons,omitempty"`
}

// seqAnalyzeCoverage 组合覆盖（选择器存在时才输出）。
func seqAnalyzeCoverage(a *Actions, base CodeSequenceRequest, wf *SeqWorkflow) *SeqWorkflowCoverage {
	if a == nil || base.CombinationSelector == "" || wf == nil {
		return nil
	}
	combos, ok := a.seqSelectorCombos(base, base.CombinationSelector)
	if !ok {
		return &SeqWorkflowCoverage{Reasons: []string{"combination_selector_unproven"}}
	}
	cov := &SeqWorkflowCoverage{Supported: combos, Analyzed: combos}
	partial := false
	if wf.Partial {
		cov.Reasons = append(cov.Reasons, "analysis_partial")
		partial = true
	}
	if wf.Frontend == nil || len(wf.Frontend.Actions) == 0 {
		cov.Reasons = append(cov.Reasons, "frontend_missing")
		partial = true
	} else {
		for _, act := range wf.Frontend.Actions {
			if act.Entry == "" {
				cov.Reasons = append(cov.Reasons, "frontend_action_unproven")
				partial = true
				break
			}
		}
	}
	if len(wf.Instances) == 0 {
		cov.Reasons = append(cov.Reasons, "workflow_not_instantiated")
		partial = true
	}
	if partial {
		cov.Complete = 0
		cov.Partial = combos
	} else {
		cov.Complete = combos
		cov.Partial = 0
	}
	return cov
}

// seqSelectorCombos 枚举选择器为真的组合数（≤64；不可判定 → false）。
func (a *Actions) seqSelectorCombos(base CodeSequenceRequest, selector string) (int, bool) {
	if !seqIsCanonicalFuncID(selector) {
		return 0, false
	}
	n, err := a.repo.GetSymbol(domain.CanonicalID(selector))
	if err != nil || n.FilePath == "" {
		return 0, false
	}
	src, err := os.ReadFile(filepath.Join(base.RepoAbs, n.FilePath))
	if err != nil {
		return 0, false
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, n.FilePath, src, 0)
	if err != nil {
		return 0, false
	}
	fn := findFuncByLine(f, fset, n.LineStart)
	if fn == nil || fn.Body == nil || len(fn.Body.List) != 1 {
		return 0, false
	}
	ret, ok := fn.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return 0, false
	}
	consts := map[string]string{}
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if i < len(vs.Values) {
					if lit, ok := seqLiteralString(vs.Values[i], consts); ok {
						consts[name.Name] = lit
					}
				}
			}
		}
	}
	params := []string{}
	for _, fld := range fn.Type.Params.List {
		for _, nm := range fld.Names {
			params = append(params, nm.Name)
		}
	}
	if len(params) == 0 {
		return 0, false
	}
	vals := map[string][]string{}
	seqCollectSelectorValues(ret.Results[0], params, consts, vals)
	for _, p := range params {
		if len(vals[p]) == 0 {
			return 0, false
		}
		vals[p] = uniqueStrings(vals[p])
	}
	var combos [][]string
	var build func(idx int, cur []string)
	build = func(idx int, cur []string) {
		if len(combos) > 64 {
			return
		}
		if idx == len(params) {
			combos = append(combos, append([]string{}, cur...))
			return
		}
		for _, v := range vals[params[idx]] {
			build(idx+1, append(cur, v))
		}
	}
	build(0, nil)
	if len(combos) == 0 || len(combos) > 64 {
		return 0, false
	}
	count := 0
	for _, c := range combos {
		env := map[string]string{}
		for i, p := range params {
			env[p] = c[i]
		}
		if v, ok := seqEvalSelectorExpr(ret.Results[0], consts, env); ok && v == "true" {
			count++
		}
	}
	if count == 0 {
		return 0, false
	}
	return count, true
}

// findFuncByLine 索引行号所在函数声明。
func findFuncByLine(f *ast.File, fset *token.FileSet, line int) *ast.FuncDecl {
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		start := fset.Position(fd.Pos()).Line
		end := fset.Position(fd.End()).Line
		if line >= start && line <= end {
			return fd
		}
	}
	return nil
}

// seqLiteralString 字面量/常量 → 文本。
func seqLiteralString(e ast.Expr, consts map[string]string) (string, bool) {
	switch v := runtimeUnparen(e).(type) {
	case *ast.BasicLit:
		switch v.Kind {
		case token.STRING:
			if s, err := strconv.Unquote(v.Value); err == nil {
				return s, true
			}
		case token.INT:
			if n, err := strconv.ParseInt(v.Value, 0, 64); err == nil {
				return strconv.FormatInt(n, 10), true
			}
		}
	case *ast.Ident:
		if s, ok := consts[v.Name]; ok {
			return s, true
		}
	}
	return "", false
}

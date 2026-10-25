package action

// todo10-2 part3：`--focus` 关注目标——只保留影响指定状态字段/返回值的
// 赋值、条件与调用，避免把无关赋值全部输出。
//
// 判据（复用 SSA 摘要，不做新的值流分析）：
//   - 字段写入点：该函数 GetFunctionFields 的 direct_write / indirect_write
//     行（FieldPath 后缀匹配 --focus 目标）→ 该行上的赋值/调用节点命中
//   - 写者调用：被调函数在 GetFieldWriters(字段) 名单里 → 该调用点命中
//   - return 目标：返回节点 + 返回表达式内的调用（求值顺序）命中
//   - 条件/循环：只要子树里有命中节点就保留（条件依据随之保留）
// 无命中时不输出空图：保留全量并在 meta.focus.applied=false 标记。

import (
	"go/ast"
	"go/token"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// newSeqFocus 解析 --focus 目标并预取字段写者名单。
// 目标形态："return"（或 result/results）表示关注返回值；其余含 '.' 的
// 视为字段路径（pkg.T.F / T.F / F——后缀匹配）。
func (a *Actions) newSeqFocus(targets []string) *seqFocus {
	f := &seqFocus{}
	for _, t := range targets {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		switch strings.ToLower(t) {
		case "return", "result", "results":
			f.returns = true
			continue
		}
		f.fields = append(f.fields, t)
	}
	if len(f.fields) > 0 {
		f.writers = map[string]bool{}
		for _, field := range f.fields {
			if ws, err := a.repo.GetFieldWriters(field); err == nil {
				for _, w := range ws {
					f.writers[w] = true
				}
			}
		}
	}
	return f
}

// focusLinesFor 该函数内命中焦点字段的源码行（SSA 摘要的写入行）。
func (a *Actions) focusLinesFor(funcID domain.CanonicalID, f *seqFocus) map[int]bool {
	if f == nil || len(f.fields) == 0 {
		return nil
	}
	rows, err := a.repo.GetFunctionFields(funcID)
	if err != nil {
		return nil
	}
	out := map[int]bool{}
	for _, r := range rows {
		if r.AccessKind != domain.SummaryDirectWrite && r.AccessKind != domain.SummaryIndirectWrite {
			continue
		}
		if !focusFieldHit(r.FieldPath, f.fields) {
			continue
		}
		if r.LineStart > 0 {
			out[r.LineStart] = true
		}
	}
	return out
}

// focusFieldHit 字段路径是否命中任一焦点目标（后缀 + 点边界匹配，大小写
// 不敏感——Go 字段名与列名常大小写不同）。
func focusFieldHit(fieldPath string, targets []string) bool {
	fp := strings.ToLower(fieldPath)
	for _, t := range targets {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" {
			continue
		}
		if fp == t || strings.HasSuffix(fp, "."+t) {
			return true
		}
	}
	return false
}

// pruneToFocus 过滤节点树：保留命中节点 + 含命中后代的分支/循环。
// 命中数累加到 f.matched（供调用方判断是否生效）。
func pruneToFocus(nodes []*CodeSeqNode, f *seqFocus) []*CodeSeqNode {
	var out []*CodeSeqNode
	for _, n := range nodes {
		keep := n.Focus
		if !keep && f.returns && (n.Kind == "return" || n.InReturn) {
			keep = true
		}
		// 子节点先过滤（分支/循环的保留取决于后代是否命中）
		childNodes := pruneToFocus(n.Nodes, f)
		var childElse []*CodeSeqNode
		if len(n.Else) > 0 {
			childElse = pruneToFocus(n.Else, f)
		}
		if keep {
			f.matched++
		}
		switch n.Kind {
		case "branch", "loop":
			// 分支/循环本身不算命中，但要保留"条件是依据"——子树有命中即保留
			if len(childNodes) == 0 && len(childElse) == 0 {
				continue
			}
		case "call":
			// 调用点保留条件：自身命中，或展开的子节点有命中（被调者内部写焦点字段）
			if !keep && len(childNodes) == 0 {
				continue
			}
		default:
			if !keep {
				continue
			}
		}
		cp := *n
		cp.Nodes = childNodes
		cp.Else = childElse
		out = append(out, &cp)
	}
	return out
}

// assignLabel 赋值语句文本（`x = expr` / `a, b := f()`——保持源码形态）。
func assignLabel(fset *token.FileSet, src []byte, as *ast.AssignStmt) string {
	var lhs []string
	for _, e := range as.Lhs {
		lhs = append(lhs, seqExprText(fset, src, e))
	}
	var rhs []string
	for _, e := range as.Rhs {
		rhs = append(rhs, seqExprText(fset, src, e))
	}
	return strings.Join(lhs, ", ") + " " + as.Tok.String() + " " + strings.Join(rhs, ", ")
}

// assignsFocusField 赋值左侧是否直接是焦点字段（源码文本后缀匹配——
// 无 SSA 行命中时的兜底，如 `s.status = ...` 命中 status）。
func assignsFocusField(as *ast.AssignStmt, fields []string) bool {
	if len(fields) == 0 {
		return false
	}
	for _, lhs := range as.Lhs {
		var text string
		switch e := lhs.(type) {
		case *ast.SelectorExpr:
			text = "." + e.Sel.Name
		case *ast.Ident:
			text = e.Name
		}
		if text == "" {
			continue
		}
		for _, f := range fields {
			f = strings.ToLower(strings.TrimSpace(f))
			if f == "" {
				continue
			}
			last := f
			if i := strings.LastIndex(f, "."); i >= 0 {
				last = f[i+1:]
			}
			if strings.ToLower(text) == last || strings.ToLower(text) == "."+last {
				return true
			}
		}
	}
	return false
}

// seqResultSlot 返回表达式 → 结果槽名：具名结果按位对应，否则 #序号
// （多返回值且表达式个数与签名不一致时也只给序号——不做猜测）。
func seqResultSlot(i, exprs int, names []string) string {
	if exprs == len(names) && i < len(names) && names[i] != "" {
		return names[i]
	}
	return "#" + itoaSeq(i)
}

// itoaSeq 小整数转字符串（避免再引 strconv 依赖）。
func itoaSeq(i int) string {
	if i == 0 {
		return "0"
	}
	digits := ""
	for i > 0 {
		digits = string(rune('0'+i%10)) + digits
		i /= 10
	}
	return digits
}

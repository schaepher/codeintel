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

// itoaSeq 小整数转字符串（避免再引 strconv 依赖）。

// ---- todo12-9：焦点依赖闭包（到达定义 + 值来源） ----

// focusAssign 一条赋值/声明（依赖闭包用）。
type focusAssign struct {
	line  int
	pos   token.Pos
	ident string // 局部变量名（LHS Ident）
	field string // 字段浅文本（LHS Selector）
	rhs   []ast.Expr
}

// focusIf 一个 if（条件依赖用）。
type focusIf struct {
	pos    token.Pos
	cond   ast.Expr
	ranges [][2]int
}

// focusDep 焦点依赖收集器。
type focusDep struct {
	fset     *token.FileSet
	body     *ast.BlockStmt
	assigns  []focusAssign
	ifs      []focusIf
	returns  []ast.Expr
	resolved []string          // 已解析的字段身份（pkg.T.F）或字段名
	varTypes map[string]string // 局部变量/形参 → 类型短名（类型身份匹配）
	pkgPath  string
	unknown  int // 类型无法判定而保守保留的写入数
	lines    map[int]bool
	names    map[string]bool
	seen     map[string]bool
}

// validateFocusTargets 焦点目标存在性/歧义校验（todo12-9 R3）：
//   - "return"/"result" → 合法
//   - 局部变量/形参名（入口函数内）→ 合法（可能无写入，正常返回）
//   - 字段名/类型限定字段 → 必须存在且唯一解析为类型身份（歧义报错）

// entryStructFieldIdentity 入口 receiver 结构体的字段身份（无写入的合法字段）。

// resolveFocusFieldPath 类型限定字段引用 → 唯一字段身份；摘要里没有该
// 字段时回退到结构体字段元数据（合法但无写入的字段），都没有 → 报错。

// resolveFocusFieldPaths 类型限定字段引用 → 唯一字段身份（不存在/歧义报错）。

// entryLocalNames 入口函数的局部变量/形参名（焦点合法性判定）。

// filterFieldLeaf 字段叶子名匹配（区分大小写——Go 字段名）。

// focusLeafHit AST 字段文本是否命中解析后的字段身份（按叶子名）。

// focusNameHit 焦点目标是否命中局部名（含完整路径末段）。
func focusNameHit(name string, fields []string) bool {
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f == "" || strings.Contains(f, "/") {
			continue
		}
		if i := strings.LastIndex(f, "."); i >= 0 {
			f = f[i+1:]
		}
		if f == name {
			return true
		}
	}
	return false
}

// focusLinesForFunc 焦点模式下的关注行：SSA 字段写入行 + 目标值的到达定义
// （初始化/条件改写/必要调用），排除与目标无值依赖的调用（todo12-9 R1）。

// markReturnAliasDeps 返回焦点的别名/被调依赖（todo13-9）：
//   - tracked = 返回表达式引用的变量 + 由它们派生的别名（alias := first）
//   - 跟踪变量的定义行 → 保留（实例定义/别名依据）
//   - 实参引用 tracked 的调用行 → 保留（被调可能改写该实例）
//   - 无返回值的被调展开（void callee）→ 保留其字段写入行；是否展开由
//     调用点是否命中决定，因此不会把无关实例的调用带进来。

// fieldSelected AST 字段文本是否命中焦点目标（todo12-9 R2：类型身份优先，
// 同名不同类型不串线；类型未知保守保留并计 partial）。

// collect 收集赋值/条件/返回语句。

// addExpr 表达式依赖：变量/字段的到达定义 + 表达式内调用。

// addName 局部变量/形参的到达定义行（沿当前函数体，不跨函数）。

// addField 字段写入的到达定义行（用于返回投影/条件读）。

// reachingLines 目标位置之前可到达的赋值行（分支并集；循环保守）。

// fixpoint 字段写入/条件依赖补全（被返回变量影响的状态写入、条件里的调用）。

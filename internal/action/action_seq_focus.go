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
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
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
	resolved []string // 已解析的字段身份（pkg.T.F）或字段名
	lines    map[int]bool
	names    map[string]bool
	seen     map[string]bool
}

// validateFocusTargets 焦点目标存在性/歧义校验（todo12-9 R3）：
//   - "return"/"result" → 合法
//   - 局部变量/形参名（入口函数内）→ 合法（可能无写入，正常返回）
//   - 字段名/类型限定字段 → 必须存在且唯一解析为类型身份（歧义报错）
func (a *Actions) validateFocusTargets(n *domain.CodeEntity, f *seqFocus) error {
	if f == nil || len(f.fields) == 0 {
		return nil
	}
	paths, err := a.repo.GetAllFieldPaths()
	if err != nil {
		paths = nil // 无法校验字段存在性时按局部名处理（不阻断）
	}
	locals := a.entryLocalNames(n)
	var resolved []string
	for _, raw := range f.fields {
		t := strings.TrimSpace(raw)
		if t == "" {
			continue
		}
		if strings.Contains(t, "/") || strings.Count(t, ".") >= 1 {
			ids, rerr := a.resolveFocusFieldPath(t, paths)
			if rerr != nil {
				return rerr
			}
			resolved = append(resolved, ids...)
			continue
		}
		// 裸名：局部变量优先（合法但可无写入）
		if locals[t] {
			continue
		}
		matches := uniqueStrings(filterFieldLeaf(paths, t))
		if len(matches) == 0 {
			// 无写入的合法字段（摘要里没有路径）：按入口 receiver 类型校验
			if id, ok := a.entryStructFieldIdentity(n, t); ok {
				resolved = append(resolved, id)
				continue
			}
		}
		switch len(matches) {
		case 0:
			return fmt.Errorf("焦点目标 %q 不存在（不是当前函数的局部变量，也不是索引中的字段）", raw)
		case 1:
			resolved = append(resolved, matches[0])
		default:
			return fmt.Errorf("焦点目标 %q 有 %d 个字段身份，存在歧义，请用类型限定路径：%s",
				raw, len(matches), strings.Join(matches, " / "))
		}
	}
	f.fieldIDs = uniqueStrings(resolved)
	for _, id := range f.fieldIDs {
		if ws, werr := a.repo.GetFieldWriters(id); werr == nil {
			for _, w := range ws {
				f.writers[w] = true
			}
		}
	}
	return nil
}

// entryStructFieldIdentity 入口 receiver 结构体的字段身份（无写入的合法字段）。
func (a *Actions) entryStructFieldIdentity(n *domain.CodeEntity, field string) (string, bool) {
	if n.FilePath == "" {
		return "", false
	}
	src, err := os.ReadFile(filepath.Join(a.repo.RepoPath(), n.FilePath))
	if err != nil {
		return "", false
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, n.FilePath, src, 0)
	if err != nil {
		return "", false
	}
	_, decl := findFuncDecl(fset, f, n.LineStart)
	if decl == nil || decl.Recv == nil || len(decl.Recv.List) == 0 {
		return "", false
	}
	recv := starTypeName(decl.Recv.List[0].Type)
	if recv == "" {
		return "", false
	}
	node, err := a.resolveStructNode(pkgPathOf(string(n.ID)) + "." + recv)
	if err != nil {
		return "", false
	}
	exists, verifiable := structHasField(node, field)
	if !verifiable || !exists {
		return "", false
	}
	return typeIdentity(node) + "." + field, true
}

// resolveFocusFieldPath 类型限定字段引用 → 唯一字段身份；摘要里没有该
// 字段时回退到结构体字段元数据（合法但无写入的字段），都没有 → 报错。
func (a *Actions) resolveFocusFieldPath(ref string, paths []string) ([]string, error) {
	ids, err := resolveFocusFieldPaths(ref, paths)
	if err == nil {
		return ids, nil
	}
	i := strings.LastIndex(ref, ".")
	if i > 0 {
		typeRef, field := ref[:i], ref[i+1:]
		if node, nerr := a.resolveStructNode(typeRef); nerr == nil {
			if exists, verifiable := structHasField(node, field); verifiable && exists {
				return []string{typeIdentity(node) + "." + field}, nil
			}
		}
	}
	return nil, err
}

// resolveFocusFieldPaths 类型限定字段引用 → 唯一字段身份（不存在/歧义报错）。
func resolveFocusFieldPaths(ref string, paths []string) ([]string, error) {
	i := strings.LastIndex(ref, ".")
	if i <= 0 {
		return nil, fmt.Errorf("焦点目标 %q 不是合法字段路径", ref)
	}
	typeRef, field := ref[:i], ref[i+1:]
	var want string
	if strings.Contains(typeRef, "/") {
		j := strings.LastIndex(typeRef, ".")
		if j < 0 {
			return nil, fmt.Errorf("焦点目标 %q 缺少类型名", ref)
		}
		want = typeRef[:j] + "." + typeRef[j+1:] + "." + field
	} else {
		name := typeRef
		if j := strings.LastIndex(typeRef, "."); j >= 0 {
			name = typeRef[j+1:]
		}
		for _, p := range paths {
			if strings.HasSuffix(p, "."+name+"."+field) {
				if want != "" && want != p {
					return nil, fmt.Errorf("焦点目标 %q 有多个类型身份，存在歧义", ref)
				}
				want = p
			}
		}
		if want == "" {
			return nil, fmt.Errorf("焦点目标 %q 不存在（未找到类型 %s 的字段 %s）", ref, name, field)
		}
		return []string{want}, nil
	}
	for _, p := range paths {
		if p == want {
			return []string{want}, nil
		}
	}
	return nil, fmt.Errorf("焦点目标 %q 不存在（索引中无字段 %s）", ref, want)
}

// entryLocalNames 入口函数的局部变量/形参名（焦点合法性判定）。
func (a *Actions) entryLocalNames(n *domain.CodeEntity) map[string]bool {
	out := map[string]bool{}
	if n.FilePath == "" {
		return out
	}
	abs := a.repo.RepoPath()
	src, err := os.ReadFile(filepath.Join(abs, n.FilePath))
	if err != nil {
		return out
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, n.FilePath, src, 0)
	if err != nil {
		return out
	}
	_, decl := findFuncDecl(fset, f, n.LineStart)
	if decl == nil {
		return out
	}
	for _, fl := range []*ast.FieldList{decl.Recv, decl.Type.Params, decl.Type.Results} {
		if fl == nil {
			continue
		}
		for _, fd := range fl.List {
			for _, name := range fd.Names {
				out[name.Name] = true
			}
		}
	}
	ast.Inspect(decl.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignStmt:
			for _, lhs := range x.Lhs {
				if id, ok := lhs.(*ast.Ident); ok {
					out[id.Name] = true
				}
			}
		case *ast.DeclStmt:
			if gd, ok := x.Decl.(*ast.GenDecl); ok {
				for _, sp := range gd.Specs {
					if vs, ok := sp.(*ast.ValueSpec); ok {
						for _, name := range vs.Names {
							out[name.Name] = true
						}
					}
				}
			}
		}
		return true
	})
	return out
}

// filterFieldLeaf 字段叶子名匹配（区分大小写——Go 字段名）。
func filterFieldLeaf(paths []string, leaf string) []string {
	var out []string
	for _, p := range paths {
		if strings.HasSuffix(p, "."+leaf) {
			out = append(out, p)
		}
	}
	return out
}

// focusLeafHit AST 字段文本是否命中解析后的字段身份（按叶子名）。
func focusLeafHit(leaf string, ids []string) bool {
	for _, id := range ids {
		if i := strings.LastIndex(id, "."); i >= 0 {
			id = id[i+1:]
		}
		if id == leaf {
			return true
		}
	}
	return false
}

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
func (a *Actions) focusLinesForFunc(funcID domain.CanonicalID, fn *ast.FuncDecl,
	fset *token.FileSet, src []byte, f *seqFocus) map[int]bool {
	if f == nil || fn == nil || fn.Body == nil {
		return nil
	}
	d := &focusDep{fset: fset, body: fn.Body, lines: map[int]bool{},
		names: map[string]bool{}, seen: map[string]bool{}, resolved: f.fields}
	if len(f.fields) > 0 {
		d.resolved = append(d.resolved, f.fieldIDs...)
	}
	d.collect()
	// 种子 0：焦点目标是局部变量/形参（如 --focus status）——该变量的赋值
	// 即依赖入口（R1：初始化/条件改写）
	for _, as := range d.assigns {
		if as.ident != "" && focusNameHit(as.ident, f.fields) {
			d.lines[as.line] = true
			d.names[as.ident] = true
			d.addExprs(as.rhs, as.pos, 0)
		}
	}
	// 种子 1：SSA 字段写入行（字段焦点）
	if len(f.fields) > 0 {
		if rows, err := a.repo.GetFunctionFields(funcID); err == nil {
			for _, r := range rows {
				if r.AccessKind != domain.SummaryDirectWrite && r.AccessKind != domain.SummaryIndirectWrite {
					continue
				}
				if !focusFieldHit(r.FieldPath, f.fieldIDs) {
					continue
				}
				if r.LineStart > 0 {
					d.lines[r.LineStart] = true
				}
			}
		}
		for _, as := range d.assigns {
			if as.field == "" {
				continue
			}
			leaf := as.field
			if i := strings.LastIndex(leaf, "."); i >= 0 {
				leaf = leaf[i+1:]
			}
			if !focusLeafHit(leaf, f.fields) && !focusFieldHit(as.field, f.fieldIDs) {
				continue
			}
			d.lines[as.line] = true
			d.addExprs(as.rhs, as.pos, 0)
		}
	}
	// 种子 2：返回焦点——返回值表达式的依赖
	if f.returns {
		for _, r := range d.returns {
			d.addExpr(r, r.Pos(), 0)
		}
	}
	d.fixpoint()
	if len(d.lines) == 0 {
		return nil
	}
	return d.lines
}

// collect 收集赋值/条件/返回语句。
func (d *focusDep) collect() {
	ast.Inspect(d.body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignStmt:
			if len(x.Lhs) == len(x.Rhs) {
				for i, lhs := range x.Lhs {
					d.addAssign(x, lhs, []ast.Expr{x.Rhs[i]})
				}
			} else {
				for _, lhs := range x.Lhs {
					d.addAssign(x, lhs, x.Rhs)
				}
			}
		case *ast.DeclStmt:
			gd, ok := x.Decl.(*ast.GenDecl)
			if !ok {
				return true
			}
			for _, sp := range gd.Specs {
				vs, ok := sp.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range vs.Names {
					var rhs []ast.Expr
					if len(vs.Values) == len(vs.Names) {
						rhs = []ast.Expr{vs.Values[i]}
					}
					d.assigns = append(d.assigns, focusAssign{
						line: d.fset.Position(name.Pos()).Line, pos: name.Pos(),
						ident: name.Name, rhs: rhs})
				}
			}
		case *ast.IfStmt:
			d.ifs = append(d.ifs, focusIf{pos: x.Pos(), cond: x.Cond,
				ranges: [][2]int{{
					d.fset.Position(x.Body.Lbrace).Line,
					d.fset.Position(x.Body.Rbrace).Line}}})
		case *ast.ReturnStmt:
			for _, r := range x.Results {
				d.returns = append(d.returns, r)
			}
		}
		return true
	})
	// if/else 的区间合并（else 也要算进"条件为依赖"的范围）
	ast.Inspect(d.body, func(n ast.Node) bool {
		ifs, ok := n.(*ast.IfStmt)
		if !ok || ifs.Else == nil {
			return true
		}
		start := d.fset.Position(ifs.Body.Lbrace).Line
		end := d.fset.Position(ifs.Else.End()).Line
		for i := range d.ifs {
			if d.ifs[i].pos == ifs.Pos() {
				d.ifs[i].ranges = [][2]int{{start, end}}
			}
		}
		return true
	})
}

func (d *focusDep) addAssign(stmt *ast.AssignStmt, lhs ast.Expr, rhs []ast.Expr) {
	line := d.fset.Position(stmt.Pos()).Line
	switch l := lhs.(type) {
	case *ast.Ident:
		if l.Name != "_" {
			d.assigns = append(d.assigns, focusAssign{line: line, pos: stmt.Pos(),
				ident: l.Name, rhs: rhs})
		}
	case *ast.SelectorExpr:
		d.assigns = append(d.assigns, focusAssign{line: line, pos: stmt.Pos(),
			field: exprTextShallow(l), rhs: rhs})
	}
}

func (d *focusDep) addExprs(exprs []ast.Expr, before token.Pos, depth int) {
	for _, e := range exprs {
		d.addExpr(e, before, depth)
	}
}

// addExpr 表达式依赖：变量/字段的到达定义 + 表达式内调用。
func (d *focusDep) addExpr(e ast.Expr, before token.Pos, depth int) {
	if e == nil || depth > 6 {
		return
	}
	ast.Inspect(e, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Ident:
			if x.Name != "_" && x.Name != "nil" && x.Name != "true" && x.Name != "false" {
				d.addName(x.Name, before, depth+1)
			}
		case *ast.SelectorExpr:
			text := exprTextShallow(x)
			if focusLeafHit(x.Sel.Name, d.resolved) && x.Pos() < before {
				d.addField(text, before, depth+1)
			}
		case *ast.CallExpr:
			d.lines[d.fset.Position(x.Lparen).Line] = true
		}
		return true
	})
}

// addName 局部变量/形参的到达定义行（沿当前函数体，不跨函数）。
func (d *focusDep) addName(name string, before token.Pos, depth int) {
	if name == "" || depth > 6 || before == token.NoPos {
		return
	}
	key := "n:" + name
	if d.seen[key] {
		return
	}
	d.seen[key] = true
	d.names[name] = true
	lines := d.reachingLines(before, func(as focusAssign) bool { return as.ident == name })
	for _, line := range lines {
		d.lines[line] = true
		for _, as := range d.assigns {
			if as.line == line && as.ident == name {
				d.addExprs(as.rhs, as.pos, depth+1)
				break
			}
		}
	}
}

// addField 字段写入的到达定义行（用于返回投影/条件读）。
func (d *focusDep) addField(field string, before token.Pos, depth int) {
	if field == "" || depth > 6 {
		return
	}
	key := "f:" + field
	if d.seen[key] {
		return
	}
	d.seen[key] = true
	for _, line := range d.reachingLines(before, func(as focusAssign) bool { return as.field == field }) {
		d.lines[line] = true
	}
}

// reachingLines 目标位置之前可到达的赋值行（分支并集；循环保守）。
func (d *focusDep) reachingLines(before token.Pos, match func(focusAssign) bool) []int {
	st := &defLineState{}
	walkDefLines(d.fset, d.body.List, before, st, match, 0)
	return st.lines
}

type defLineState struct{ lines []int }

func (s *defLineState) clone() *defLineState {
	out := &defLineState{lines: make([]int, len(s.lines))}
	copy(out.lines, s.lines)
	return out
}

func walkDefLines(fset *token.FileSet, stmts []ast.Stmt, target token.Pos, st *defLineState,
	match func(focusAssign) bool, depth int) bool {
	if depth > 12 {
		return false
	}
	for _, s := range stmts {
		if target != token.NoPos {
			if target < s.Pos() {
				return false
			}
			if target <= s.End() {
				return walkDefLineStmt(fset, s, target, st, match, depth)
			}
		}
		applyDefLineStmt(fset, s, st, match, depth)
	}
	return false
}

func walkDefLineStmt(fset *token.FileSet, stmt ast.Stmt, target token.Pos, st *defLineState,
	match func(focusAssign) bool, depth int) bool {
	switch x := stmt.(type) {
	case *ast.BlockStmt:
		return walkDefLines(fset, x.List, target, st, match, depth+1)
	case *ast.IfStmt:
		if target >= x.Body.Pos() && target <= x.Body.End() {
			return walkDefLines(fset, x.Body.List, target, st, match, depth+1)
		}
		if x.Else != nil && target >= x.Else.Pos() && target <= x.Else.End() {
			return walkDefLineStmt(fset, x.Else, target, st, match, depth)
		}
		return true
	case *ast.ForStmt:
		return walkDefLines(fset, x.Body.List, target, st, match, depth+1)
	case *ast.RangeStmt:
		return walkDefLines(fset, x.Body.List, target, st, match, depth+1)
	case *ast.SwitchStmt:
		return walkDefLines(fset, x.Body.List, target, st, match, depth+1)
	case *ast.TypeSwitchStmt:
		return walkDefLines(fset, x.Body.List, target, st, match, depth+1)
	case *ast.SelectStmt:
		return walkDefLines(fset, x.Body.List, target, st, match, depth+1)
	}
	return true
}

func applyDefLineStmt(fset *token.FileSet, stmt ast.Stmt, st *defLineState,
	match func(focusAssign) bool, depth int) {
	if depth > 12 {
		return
	}
	setAt := func(as focusAssign) {
		if match(as) {
			st.lines = []int{as.line}
		}
	}
	switch x := stmt.(type) {
	case *ast.AssignStmt:
		line := fset.Position(x.Pos()).Line
		if len(x.Lhs) == len(x.Rhs) {
			for i, lhs := range x.Lhs {
				setAt(focusAssign{line: line, ident: identName(lhs), field: fieldText(lhs),
					rhs: []ast.Expr{x.Rhs[i]}})
			}
		}
	case *ast.DeclStmt:
		gd, ok := x.Decl.(*ast.GenDecl)
		if !ok {
			return
		}
		for _, sp := range gd.Specs {
			vs, ok := sp.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, name := range vs.Names {
				setAt(focusAssign{line: fset.Position(name.Pos()).Line, ident: name.Name})
			}
		}
	case *ast.BlockStmt:
		walkDefLines(fset, x.List, token.NoPos, st, match, depth+1)
	case *ast.IfStmt:
		thenE := st.clone()
		walkDefLines(fset, x.Body.List, token.NoPos, thenE, match, depth+1)
		elseE := st.clone()
		if x.Else != nil {
			applyDefLineStmt(fset, x.Else, elseE, match, depth+1)
		}
		st.lines = dedupInts(append(thenE.lines, elseE.lines...))
	case *ast.ForStmt:
		loopE := st.clone()
		walkDefLines(fset, x.Body.List, token.NoPos, loopE, match, depth+1)
		st.lines = dedupInts(append(st.lines, loopE.lines...))
	case *ast.RangeStmt:
		loopE := st.clone()
		walkDefLines(fset, x.Body.List, token.NoPos, loopE, match, depth+1)
		st.lines = dedupInts(append(st.lines, loopE.lines...))
	case *ast.SwitchStmt:
		st.lines = defLineCases(fset, x.Body, st, match, depth)
	case *ast.TypeSwitchStmt:
		st.lines = defLineCases(fset, x.Body, st, match, depth)
	case *ast.SelectStmt:
		st.lines = defLineCases(fset, x.Body, st, match, depth)
	}
}

func defLineCases(fset *token.FileSet, body *ast.BlockStmt, st *defLineState,
	match func(focusAssign) bool, depth int) []int {
	out := append([]int{}, st.lines...)
	for _, c := range body.List {
		cl, ok := c.(*ast.CaseClause)
		if !ok {
			continue
		}
		e := st.clone()
		walkDefLines(fset, cl.Body, token.NoPos, e, match, depth+1)
		out = append(out, e.lines...)
	}
	return dedupInts(out)
}

func identName(e ast.Expr) string {
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

func fieldText(e ast.Expr) string {
	if s, ok := e.(*ast.SelectorExpr); ok {
		return exprTextShallow(s)
	}
	return ""
}

func dedupInts(xs []int) []int {
	seen := map[int]bool{}
	out := make([]int, 0, len(xs))
	for _, x := range xs {
		if x == 0 || seen[x] {
			continue
		}
		seen[x] = true
		out = append(out, x)
	}
	return out
}

// fixpoint 字段写入/条件依赖补全（被返回变量影响的状态写入、条件里的调用）。
func (d *focusDep) fixpoint() {
	for round := 0; round < 5; round++ {
		changed := false
		for _, as := range d.assigns {
			if as.field == "" || d.lines[as.line] {
				continue
			}
			if exprUsesNames(as.rhs, d.names) {
				d.lines[as.line] = true
				d.addExprs(as.rhs, as.pos, 1)
				changed = true
			}
		}
		for _, iff := range d.ifs {
			inRange := false
			for _, rg := range iff.ranges {
				for line := range d.lines {
					if line >= rg[0] && line <= rg[1] {
						inRange = true
						break
					}
				}
				if inRange {
					break
				}
			}
			if inRange {
				before := len(d.lines)
				d.addExpr(iff.cond, iff.pos, 1)
				if len(d.lines) != before {
					changed = true
				}
			}
		}
		if !changed {
			return
		}
	}
}

func exprUsesNames(exprs []ast.Expr, names map[string]bool) bool {
	used := false
	for _, e := range exprs {
		ast.Inspect(e, func(n ast.Node) bool {
			if used {
				return false
			}
			if id, ok := n.(*ast.Ident); ok && names[id.Name] {
				used = true
				return false
			}
			return true
		})
	}
	return used
}

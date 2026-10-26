package action

// todo12-1：`query effects` 的目标筛选（按字段类型身份 / 返回槽）。
//
// 用户确认的契约（R1–R6）：
//   - --target-field <类型限定字段路径> / --target-return <名字|#槽位> 互斥
//   - 字段按**类型身份**解析（pkg.Type.Field），同名不同结构体不混用；
//     返回槽按签名解析（#0 起），筛选后不重编号
//   - 目标筛选只裁剪写入/返回与调用，不改变路径可达性；预算/未知/截断
//     进 analysis.status=partial，不静默 complete
//
// 依赖口径：值来源只沿当前路径（局部变量赋值 + 返回表达式 + 字段写入的
// 右值），不跨函数找同名变量；未知类型保守保留并标 partial。

import (
	"fmt"
	"go/ast"
	"go/token"
	"strconv"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// EffectsTarget 目标筛选的结果（JSON `target`）。
type EffectsTarget struct {
	Kind  string `json:"kind"`            // field | return
	Field string `json:"field,omitempty"` // 类型限定字段路径（pkg.Type.Field）
	Name  string `json:"name,omitempty"`  // 返回槽声明名（匿名结果为空）
	Slot  string `json:"slot,omitempty"`  // 返回槽规范身份 #0..#N-1
}

// EffectsAnalysis 目标模式的分析完整性（R5；未知/截断 → partial）。
type EffectsAnalysis struct {
	Status      string         `json:"status"` // complete | partial
	Depth       int            `json:"depth"`
	Diagnostics []SeqDiagCount `json:"diagnostics"`
}

// 目标模式诊断代码。
const (
	EffectDiagPathBudget     = "path_budget"            // 路径预算截断
	EffectDiagPathNesting    = "path_nesting_truncated" // 分支嵌套过深
	EffectDiagLoopUnrefined  = "path_loop_unrefined"    // 循环未逐次展开
	EffectDiagSwitchUnrefine = "path_switch_unrefined"  // switch/select 未细化
	EffectDiagTargetUnknown  = "target_dependency_unresolved"
	EffectDiagTargetNoWrite  = "target_no_write"
	EffectDiagSlotUnresolved = "target_slot_unresolved"
)

// effectsTarget 已解析的目标（内部）。
type effectsTarget struct {
	output     EffectsTarget
	kind       string // field | return
	fieldID    string // pkg.Type.Field（字段模式）
	fieldName  string
	slot       int
	slotName   string
	resultName []string
	varTypes   map[string]string
	pkgPath    string
	unknown    int // 类型无法判定而保守保留的写入数
	slotMiss   int // 路径 return 未提供目标槽的次数
}

// resolveEffectsTarget 解析目标参数（空值/不存在/歧义/越界均报错）。
func (a *Actions) resolveEffectsTarget(rawField, rawReturn string, fn *ast.FuncDecl,
	fset *token.FileSet, src []byte, varTypes map[string]string, pkgPath string) (*effectsTarget, error) {
	if strings.TrimSpace(rawField) != "" && strings.TrimSpace(rawReturn) != "" {
		return nil, fmt.Errorf("--target-field 与 --target-return 互斥，只能选择一个目标")
	}
	if strings.TrimSpace(rawField) == "" && strings.TrimSpace(rawReturn) == "" {
		return nil, fmt.Errorf("目标为空：--target-field / --target-return 需要一个非空值")
	}
	t := &effectsTarget{varTypes: varTypes, pkgPath: pkgPath}
	if strings.TrimSpace(rawField) != "" {
		ref := strings.TrimSpace(rawField)
		typeRef, field, err := splitFieldTargetRef(ref)
		if err != nil {
			return nil, err
		}
		node, err := a.resolveStructNode(typeRef)
		if err != nil {
			return nil, err
		}
		exists, verifiable := structHasField(node, field)
		if verifiable && !exists {
			return nil, fmt.Errorf("目标字段 %q 不存在于类型 %s（字段：%s）",
				field, node.ID, structFieldNames(node))
		}
		if !verifiable {
			return nil, fmt.Errorf("目标字段 %q 无法确认：类型 %s 没有字段元数据（先 reindex 或改用完整字段路径）",
				field, node.ID)
		}
		t.kind = "field"
		t.fieldName = field
		t.fieldID = typeIdentity(node) + "." + field
		t.output = EffectsTarget{Kind: "field", Field: t.fieldID}
		return t, nil
	}
	// 返回槽
	if fn == nil || fn.Type.Results == nil || len(fn.Type.Results.List) == 0 {
		return nil, fmt.Errorf("目标函数没有返回值，不能用 --target-return")
	}
	names := resultNamesOf(fn.Type.Results)
	slot, err := parseReturnSlot(strings.TrimSpace(rawReturn), names)
	if err != nil {
		return nil, err
	}
	t.kind = "return"
	t.slot = slot
	t.slotName = ""
	if slot < len(names) {
		t.slotName = names[slot]
	}
	t.resultName = names
	t.output = EffectsTarget{Kind: "return", Name: t.slotName, Slot: "#" + strconv.Itoa(slot)}
	return t, nil
}

// splitFieldTargetRef "pkg/path.Type.Field" / "Type.Field" → (类型引用, 字段)。
func splitFieldTargetRef(ref string) (string, string, error) {
	i := strings.LastIndex(ref, ".")
	if i <= 0 || i == len(ref)-1 {
		return "", "", fmt.Errorf("目标字段 %q 不是类型限定字段路径（期望 Type.Field 或 pkg/path.Type.Field）", ref)
	}
	field := ref[i+1:]
	if strings.Contains(field, " ") || strings.Contains(field, "/") {
		return "", "", fmt.Errorf("目标字段 %q 的字段名不合法：%q", ref, field)
	}
	return ref[:i], field, nil
}

// resolveStructNode 类型引用 → 结构体节点（完整路径优先；短名须唯一）。
func (a *Actions) resolveStructNode(typeRef string) (*domain.CodeEntity, error) {
	typeRef = strings.TrimSpace(typeRef)
	if strings.Contains(typeRef, "/") {
		i := strings.LastIndex(typeRef, ".")
		if i < 0 {
			return nil, fmt.Errorf("类型引用 %q 缺少类型名（期望 pkg/path.Type）", typeRef)
		}
		id := domain.CanonicalID("symbol:go:" + typeRef[:i] + ":" + typeRef[i+1:])
		n, err := a.repo.GetSymbol(id)
		if err != nil {
			return nil, fmt.Errorf("目标字段的类型 %q 不存在：%v", typeRef, err)
		}
		if n.Kind != domain.KindStruct {
			return nil, fmt.Errorf("目标字段的类型 %q 不是结构体（kind=%s）", typeRef, n.Kind)
		}
		return n, nil
	}
	name := typeRef
	if i := strings.LastIndex(typeRef, "."); i >= 0 {
		name = typeRef[i+1:]
	}
	matches, err := a.repo.GetSymbolByName(name)
	if err != nil {
		return nil, err
	}
	var structs []*domain.CodeEntity
	for _, m := range matches {
		if m.Kind == domain.KindStruct {
			structs = append(structs, m)
		}
	}
	switch len(structs) {
	case 0:
		return nil, fmt.Errorf("目标字段的类型 %q 不存在（未找到结构体）", typeRef)
	case 1:
		return structs[0], nil
	default:
		ids := make([]string, 0, len(structs))
		for _, s := range structs {
			ids = append(ids, string(s.ID))
		}
		return nil, fmt.Errorf("类型 %q 有 %d 个匹配，请使用完整字段路径：%s",
			typeRef, len(structs), strings.Join(ids, " / "))
	}
}

// typeIdentity 结构体节点 → pkg.Type（字段身份前缀）。
func typeIdentity(n *domain.CodeEntity) string {
	id := strings.TrimPrefix(string(n.ID), "symbol:go:")
	if i := strings.LastIndex(id, ":"); i >= 0 {
		return id[:i] + "." + id[i+1:]
	}
	return id
}

// structHasField 结构体是否有该字段（verifiable=false 表示无字段元数据）。
func structHasField(n *domain.CodeEntity, field string) (exists, verifiable bool) {
	raw, ok := n.Properties["fields"]
	if !ok || raw == nil {
		return false, false
	}
	switch fs := raw.(type) {
	case []map[string]any:
		for _, f := range fs {
			if fmt.Sprint(f["name"]) == field {
				return true, true
			}
		}
		return false, true
	case []any:
		for _, x := range fs {
			if m, ok := x.(map[string]any); ok && fmt.Sprint(m["name"]) == field {
				return true, true
			}
		}
		return false, true
	}
	return false, false
}

// structFieldNames 字段清单（错误提示用）。
func structFieldNames(n *domain.CodeEntity) string {
	var names []string
	switch fs := n.Properties["fields"].(type) {
	case []map[string]any:
		for _, f := range fs {
			names = append(names, fmt.Sprint(f["name"]))
		}
	case []any:
		for _, x := range fs {
			if m, ok := x.(map[string]any); ok {
				names = append(names, fmt.Sprint(m["name"]))
			}
		}
	}
	if len(names) == 0 {
		return "（无）"
	}
	return strings.Join(names, ", ")
}

// parseReturnSlot 名字或 #槽位 → 下标（空值/不存在/越界报错）。
func parseReturnSlot(raw string, names []string) (int, error) {
	if raw == "" {
		return 0, fmt.Errorf("目标返回槽为空")
	}
	if strings.HasPrefix(raw, "#") {
		n, err := strconv.Atoi(strings.TrimPrefix(raw, "#"))
		if err != nil || n < 0 {
			return 0, fmt.Errorf("目标返回槽 %q 不是合法槽位（期望 #0..#N）", raw)
		}
		if n >= len(names) {
			return 0, fmt.Errorf("目标返回槽 %q 越界：函数有 %d 个返回值（#0..#%d）", raw, len(names), len(names)-1)
		}
		return n, nil
	}
	for i, name := range names {
		if name != "" && name == raw {
			return i, nil
		}
	}
	declared := make([]string, 0, len(names))
	for i, name := range names {
		if name == "" {
			name = "#" + strconv.Itoa(i)
		}
		declared = append(declared, name)
	}
	return 0, fmt.Errorf("目标返回槽 %q 不存在（已声明：%s）", raw, strings.Join(declared, ", "))
}

// writeIdentity AST 字段表达式 → 类型身份（known=false 表示类型不可判定）。
func writeIdentity(fieldText string, varTypes map[string]string, pkgPath string) (string, bool) {
	i := strings.LastIndex(fieldText, ".")
	if i <= 0 {
		return "", false
	}
	base, leaf := fieldText[:i], fieldText[i+1:]
	if strings.Contains(base, ".") || strings.Contains(base, "[") {
		return "", false // 嵌套选择/索引：类型链未解析
	}
	typ := varTypes[base]
	if typ == "" {
		return "", false
	}
	return pkgPath + "." + typ + "." + leaf, true
}

// keepFieldWrite 字段目标：类型身份精确匹配；类型未知时同名保守保留。
func (t *effectsTarget) keepFieldWrite(id string, known bool, leaf string) (keep, unknown bool) {
	if known {
		return id == t.fieldID, false
	}
	if leaf == t.fieldName {
		t.unknown++
		return true, true
	}
	return false, false
}

// returnDeps 返回槽的值依赖（局部变量名 + 字段身份）。
type returnDeps struct {
	locals map[string]bool
	fields map[string]bool
}

// depsForReturn 计算该路径目标返回槽的依赖（不跨函数）。
func (t *effectsTarget) depsForReturn(sp pathSpec) returnDeps {
	d := returnDeps{locals: map[string]bool{}, fields: map[string]bool{}}
	var addExpr func(e ast.Expr, depth int)
	addExpr = func(e ast.Expr, depth int) {
		if e == nil || depth > 3 {
			return
		}
		switch x := e.(type) {
		case *ast.Ident:
			d.locals[x.Name] = true
			for _, la := range sp.locals {
				if la.name != x.Name {
					continue
				}
				for _, r := range la.rhs {
					addExpr(r, depth+1)
				}
			}
		case *ast.SelectorExpr:
			if id, known := writeIdentity(exprTextShallow(x), t.varTypes, t.pkgPath); known {
				d.fields[id] = true
			}
		case *ast.CallExpr:
			for _, a := range x.Args {
				addExpr(a, depth+1)
			}
		case *ast.BinaryExpr:
			addExpr(x.X, depth+1)
			addExpr(x.Y, depth+1)
		case *ast.UnaryExpr:
			addExpr(x.X, depth+1)
		case *ast.ParenExpr:
			addExpr(x.X, depth+1)
		case *ast.StarExpr:
			addExpr(x.X, depth+1)
		case *ast.IndexExpr:
			addExpr(x.X, depth+1)
			addExpr(x.Index, depth+1)
		}
	}
	if t.slot < len(sp.returnExprs) {
		addExpr(sp.returnExprs[t.slot], 0)
	} else if len(sp.returnExprs) == 0 && t.slot < len(t.resultName) && t.resultName[t.slot] != "" {
		// 裸 return：值是具名结果变量本身
		addExpr(&ast.Ident{Name: t.resultName[t.slot]}, 0)
	}
	return d
}

// keepWriteForReturn 返回槽目标：保留影响目标返回值的写入。
func (t *effectsTarget) keepWriteForReturn(ws dbWriteSite, sp pathSpec) bool {
	d := t.depsForReturn(sp)
	if len(d.locals) == 0 && len(d.fields) == 0 {
		return false
	}
	if id, known := writeIdentity(ws.field, t.varTypes, t.pkgPath); known && d.fields[id] {
		return true
	}
	for _, r := range ws.rhs {
		if exprUsesDeps(r, d) {
			return true
		}
	}
	return false
}

// exprUsesDeps 表达式是否引用依赖（局部变量/字段）。
func exprUsesDeps(e ast.Expr, d returnDeps) bool {
	if e == nil {
		return false
	}
	used := false
	ast.Inspect(e, func(n ast.Node) bool {
		if used {
			return false
		}
		switch x := n.(type) {
		case *ast.Ident:
			if d.locals[x.Name] {
				used = true
				return false
			}
		case *ast.SelectorExpr:
			// 只比较文本末段之外的身份由调用方保证；此处按浅文本匹配字段名
			text := exprTextShallow(x)
			for id := range d.fields {
				if strings.HasSuffix(id, "."+text) || strings.HasSuffix(id, text) {
					used = true
					return false
				}
			}
		}
		return true
	})
	return used
}

// filterPathResult 目标模式：只保留与目标相关的返回槽（不重编号）。
func (t *effectsTarget) filterPathResult(sp pathSpec, names []string) *ResultEffect {
	if sp.returnLine == 0 {
		return nil
	}
	if t.kind == "return" {
		res := &ResultEffect{Line: sp.returnLine, Verdict: SeqVerdictCorroborated,
			Slots: []string{"#" + strconv.Itoa(t.slot)}, Detail: "返回值来自该路径的 return"}
		if t.slot < len(sp.returnExprs) {
			res.Returns = []string{sp.returnVals[t.slot]}
			return res
		}
		if len(sp.returnExprs) == 0 && t.slot < len(t.resultName) && t.resultName[t.slot] != "" {
			res.Returns = []string{t.resultName[t.slot]}
			res.Detail = "裸 return：返回值为具名结果变量"
			return res
		}
		t.slotMiss++
		res.Verdict = SeqVerdictUnverified
		res.Detail = "该路径的 return 未提供目标槽（多值调用/表达式个数不足，未做猜测）"
		return res
	}
	// 字段目标：保留依赖目标字段的返回槽
	var returns, slots []string
	for i := range sp.returnExprs {
		if !t.exprDependsOnField(sp.returnExprs[i], sp, 0) {
			continue
		}
		returns = append(returns, sp.returnVals[i])
		slots = append(slots, "#"+strconv.Itoa(i))
	}
	if len(returns) == 0 {
		return nil
	}
	return &ResultEffect{Returns: returns, Slots: slots, Line: sp.returnLine,
		Verdict: SeqVerdictCorroborated, Detail: "返回表达式依赖目标字段"}
}

// exprDependsOnField 表达式是否依赖目标字段（沿当前路径的局部赋值/字段写入）。
func (t *effectsTarget) exprDependsOnField(e ast.Expr, sp pathSpec, depth int) bool {
	if e == nil || depth > 3 {
		return false
	}
	switch x := e.(type) {
	case *ast.SelectorExpr:
		if id, known := writeIdentity(exprTextShallow(x), t.varTypes, t.pkgPath); known {
			if id == t.fieldID {
				return true
			}
		}
		// 该字段被写入时右值依赖目标字段（如 h.State = status，status 来自目标）
		text := exprTextShallow(x)
		for _, ws := range sp.writes {
			if ws.field != text {
				continue
			}
			for _, r := range ws.rhs {
				if t.exprDependsOnField(r, sp, depth+1) {
					return true
				}
			}
		}
	case *ast.Ident:
		for _, la := range sp.locals {
			if la.name != x.Name {
				continue
			}
			for _, r := range la.rhs {
				if t.exprDependsOnField(r, sp, depth+1) {
					return true
				}
			}
		}
	case *ast.CallExpr:
		for _, a := range x.Args {
			if t.exprDependsOnField(a, sp, depth+1) {
				return true
			}
		}
	case *ast.BinaryExpr:
		return t.exprDependsOnField(x.X, sp, depth+1) || t.exprDependsOnField(x.Y, sp, depth+1)
	case *ast.UnaryExpr:
		return t.exprDependsOnField(x.X, sp, depth+1)
	case *ast.ParenExpr:
		return t.exprDependsOnField(x.X, sp, depth+1)
	case *ast.StarExpr:
		return t.exprDependsOnField(x.X, sp, depth+1)
	case *ast.IndexExpr:
		return t.exprDependsOnField(x.X, sp, depth+1) || t.exprDependsOnField(x.Index, sp, depth+1)
	}
	return false
}

package action

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
	// todo13-7：路径预算的精确口径（已用/上限/停止位置/范围/前沿）——
	// 与诊断 detail 同源，三种消费者口径一致。
	PathBudget *EffectsPathBudget `json:"path_budget,omitempty"`
}

// EffectsPathBudget 路径预算截断的可核对信息。
type EffectsPathBudget struct {
	Used     int    `json:"used"`
	Limit    int    `json:"limit"`
	StopFile string `json:"stop_file,omitempty"`
	StopLine int    `json:"stop_line,omitempty"`
	Scope    string `json:"scope,omitempty"` // 未展开语句/分支范围（源码文本）
	Frontier int    `json:"frontier"`        // 未展开前沿数
	// ExtraTerminals 由前沿可产生的额外终态总数：unknown（低预算下无法
	// 证明具体数量，不能用前沿数冒充）。
	ExtraTerminals string `json:"extra_terminals"`
}

// 目标模式诊断代码。
const (
	EffectDiagPathBudget       = "path_budget"            // 路径预算截断
	EffectDiagPathNesting      = "path_nesting_truncated" // 分支嵌套过深
	EffectDiagLoopUnrefined    = "path_loop_unrefined"    // 循环未逐次展开
	EffectDiagSwitchUnrefine   = "path_switch_unrefined"  // switch/select 未细化
	EffectDiagTargetUnknown    = "target_dependency_unresolved"
	EffectDiagTargetNoWrite    = "target_no_write"
	EffectDiagSlotUnresolved   = "target_slot_unresolved"
	EffectDiagCalleeUnresolved = "path_callee_unresolved"
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
		return "", false
	}
	typ := varTypes[base]
	if typ == "" {
		return "", false
	}
	return pkgPath + "." + typ + "." + leaf, true
}

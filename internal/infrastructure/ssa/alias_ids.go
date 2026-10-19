package ssa

import (
	"fmt"
	"go/token"
	"go/types"

	"github.com/schaepher/codeintel/internal/domain"
	"golang.org/x/tools/go/ssa"
)

// aliasSlot 值节点槽位名（**唯一口径**，Q252b）：fe 发射端（instancePath
// 的 Alloc 回退，Q235-7）与 alias 端必须对**同一 ssa.Value** 落同一 ID——
// 否则 alias 边（alias pass 发射）与 argument/returns 边（fe 发射）分裂到
// 两个节点：实测 pp fixture 的 `t := &T{}` 同时存在 `main#t0`（只有 alias
// 边）与 `main#*pp.T`（只有 argument 边），query path / value-trace 的跨
// 函数链在匿名分配处断开。
// 规则：源码变量名（SSA 名非 tN）保持不变；匿名 Alloc（tN）回退类型短名。
// 非 Alloc 值保持 v.Name()——fe 公共分支同样用 v.Name()（回退类型短名只
// 影响展示名 Name，不影响 ID）。
func aliasSlot(v ssa.Value) string {
	return aliasSlotWith(v, func(t types.Type) string { return t.String() })
}

// aliasSlotWith 同 aliasSlot，类型字符串由调用方缓存提供（todo8-3：
// 别名分析与字段提取共享构建级 typeString）。
func aliasSlotWith(v ssa.Value, typeString func(types.Type) string) string {
	slot := v.Name()
	if !isSSAName(slot) {
		return slot
	}
	if _, isAlloc := v.(*ssa.Alloc); !isAlloc {
		return slot
	}
	if tn := allocTypeShort(typeString(v.Type())); tn != "" {
		return tn
	}
	return slot
}

// aliasSlot 提取器侧（共享 typeString 缓存）。
func (ext *fieldExtractor) aliasSlot(v ssa.Value) string {
	return aliasSlotWith(v, ext.typeString)
}

// aliasSlot aliasPass 侧（共享 typeString 缓存）。
func (p *aliasPass) aliasSlot(v ssa.Value) string {
	return aliasSlotWith(v, p.typeString)
}

// valueNodeID 生成并发射值节点的 canonical ID（funcID#slot，与 emitValue
// 一致：shadowing 同名附加 @行号）。alias 边 source 用（B1：此前
// funcIDOfValue 返回函数 ID，alias 边全部错挂在函数节点上——值节点
// 看不到别名关系）。值节点可能未被 emitValue 发射（如 Field 指令的
// 基值），此处保证端点存在（FK 约束）。

func (p *aliasPass) valueNodeID(v ssa.Value) (domain.CanonicalID, bool) {
	// todo3-4：同一值的多次查询必须返回相同标识——不能因槽位访问状态给
	// 第二次查询追加 @行号（与 emitValue 路径不一致）。
	if id, ok := p.valueIDs[v]; ok {
		return id, true
	}
	// Q178：参数/接收者统一用签名参数节点（与 emitValue 一致）——此前
	// alias 对参数生成 ssa_value 副本（funcID#m），与签名参数节点
	// （#param.recv.m）双节点分裂，数据边挂两处。参数节点已由
	// emitSignatureNodes 发射，这里只返回 ID 不再发射。
	if prm, ok := v.(*ssa.Parameter); ok {
		fn := prm.Parent()
		if fn == nil || fn.Signature == nil {
			return "", false
		}
		funcID, ok := p.funcIDOf(fn)
		if !ok {
			return "", false
		}
		tag := valueScopeTag(p.prog, p.repo.Path, fn)
		if recv := fn.Signature.Recv(); recv != nil && len(fn.Params) > 0 && fn.Params[0] == prm {
			name := recv.Name()
			if name == "" {
				name = "recv"
			}
			id := domain.CanonicalID(string(funcID) + "#param.recv." + name + tag)
			p.valueIDs[v] = id
			return id, true
		}
		name := prm.Object().Name()
		if name == "" {
			idx := 0
			for i := 0; i < fn.Signature.Params().Len(); i++ {
				if fn.Signature.Params().At(i) == prm.Object() {
					idx = i
					break
				}
			}
			name = fmt.Sprintf("arg%d", idx)
		}
		id := domain.CanonicalID(string(funcID) + "#param." + name + tag)
		p.valueIDs[v] = id
		return id, true
	}
	fn := v.Parent()
	if fn == nil {
		return "", false
	}
	funcID, ok := p.funcIDOf(fn)
	if !ok {
		return "", false
	}
	tag := valueScopeTag(p.prog, p.repo.Path, fn)
	scopeKey := string(funcID) + tag
	slots := p.slotSeen[scopeKey]
	if slots == nil {
		slots = map[string]bool{}
		p.slotSeen[scopeKey] = slots
	}
	slot := p.aliasSlot(v)
	if slots[slot] {
		line := p.prog.Fset.PositionFor(v.Pos(), false).Line
		slot = fmt.Sprintf("%s@%d", slot, line)
	} else {
		slots[slot] = true
	}
	id := domain.CanonicalID(string(funcID) + "#" + slot + tag)
	p.valueIDs[v] = id
	name := slot
	// Q235-6/8/9：SSA 临时名回退类型短名（与 emitValue 一致）——Alloc
	// （Q235-6 匿名分配）扩展到全部指令；phi 有声明时已恢复变量名
	// （Q235-9），无 Pos 合成 phi 同样回退类型短名
	if tn := allocTypeShort(p.typeString(v.Type())); tn != "" {
		name = tn
	}
	if err := p.emit(domain.Item{Node: &domain.CodeEntity{
		ID:   id,
		Kind: domain.KindSSAValue,
		Name: name,
		Properties: map[string]any{
			"origin_kind": originKind(v),
			"ssa_op":      ssaOp(v),
			"type_string": p.typeString(v.Type()),
			"func_id":     string(funcID),
		},
	}}); err != nil {
		return "", false
	}
	return id, true
}

// objectIDOf 确保对象创建点（alloc / MakeMap / MakeSlice）的 ssa_value
// 节点发射（Q7：被别名引用的对象也发射）。

func (p *aliasPass) objectIDOf(obj ssa.Value) (domain.CanonicalID, bool) {
	if id, ok := p.allocIDs[obj]; ok {
		return id, true
	}
	fn := obj.Parent()
	if fn == nil {
		return "", false
	}
	funcID, ok := p.funcIDOf(fn)
	if !ok {
		return "", false
	}
	tag := valueScopeTag(p.prog, p.repo.Path, fn)
	scopeKey := string(funcID) + tag
	slots := p.slotSeen[scopeKey]
	if slots == nil {
		slots = map[string]bool{}
		p.slotSeen[scopeKey] = slots
	}
	slot := p.aliasSlot(obj)
	if slots[slot] {
		line := p.prog.Fset.PositionFor(obj.Pos(), false).Line
		slot = fmt.Sprintf("%s@%d", slot, line)
	} else {
		slots[slot] = true
	}
	id := domain.CanonicalID(string(funcID) + "#" + slot + tag)
	p.allocIDs[obj] = id
	kind := "alloc"
	if _, isMap := obj.(*ssa.MakeMap); isMap {
		kind = "make"
	}
	name := slot
	// Q235-6：匿名分配回退类型短名（与 emitValue 一致）
	if tn := allocTypeShort(p.typeString(obj.Type())); tn != "" {
		name = tn
	}
	if err := p.emit(domain.Item{Node: &domain.CodeEntity{
		ID:   id,
		Kind: domain.KindSSAValue,
		Name: name,
		Properties: map[string]any{
			"origin_kind": kind,
			"ssa_op":      ssaOp(obj),
			"type_string": p.typeString(obj.Type()),
			"func_id":     string(funcID),
		},
	}}); err != nil {
		return "", false
	}
	return id, true
}

// fieldInfoFor 计算写字段指令的类型限定路径（复用 fieldInfo 语义）。

func (p *aliasPass) fieldInfoFor(fa *ssa.FieldAddr) (fieldInfo, bool) {
	named, st := derefStruct(fa.X.Type())
	if named == nil {
		return fieldInfo{}, false
	}
	field := st.Field(fa.Field)
	fi := fieldInfo{
		fullPath:   named.Obj().Pkg().Path() + "." + named.Obj().Name() + "." + field.Name(),
		fieldName:  field.Name(),
		typeString: p.typeString(field.Type()),
	}
	pos := p.prog.Fset.PositionFor(fa.Pos(), false)
	fi.filePath = relPath(p.repo.Path, pos.Filename)
	fi.line = pos.Line
	if fi.line > 0 {
		fi.snippet = p.sourceLine(fi.filePath, fi.line)
	}
	return fi, true
}

// sourceLine Q248：与 extractor 共用 Index 级行缓存（原先各自一份）。
func (p *aliasPass) sourceLine(filePath string, line int) string {
	return p.lines.line(filePath, line)
}

func (p *aliasPass) funcIDOf(fn *ssa.Function) (domain.CanonicalID, bool) {
	if id, ok := p.funcIDs[fn]; ok {
		return id, true
	}
	obj, ok := fn.Object().(*types.Func)
	if !ok || obj == nil {
		return "", false
	}
	id, _, _ := funcIdentity(obj)
	if id == "" {
		return "", false
	}
	p.funcIDs[fn] = id
	return id, true
}

// elementWritePath 生成元素写路径（间接写条目用，Q5 记号）。

func (p *aliasPass) elementWritePath(container, key ssa.Value) (string, bool) {
	full := ""
	if un, ok := container.(*ssa.UnOp); ok && un.Op == token.MUL {
		container = un.X
	}
	if fa, ok := container.(*ssa.FieldAddr); ok {
		if info, ok2 := p.fieldInfoFor(fa); ok2 {
			full = info.fullPath
		}
	}
	if full == "" {
		full = namedContainerOf(container.Type())
	}
	if full == "" {
		return "", false
	}
	return full + elementMark(key), true
}

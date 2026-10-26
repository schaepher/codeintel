package ssa

import (
	"go/types"

	"github.com/schaepher/codeintel/internal/domain"
	"golang.org/x/tools/go/ssa"
)

// emitConst 发射常量值节点（origin=literal）。同一函数内按"类型+值"稳定
// 去重（同一常量的不同 SSA 实例合并；不同值/类型分开）。
func (ext *fieldExtractor) emitConst(c *ssa.Const) (domain.CanonicalID, error) {
	return ext.emitConstAt(c, 0, "")
}

// emitConstAt 同 emitConst，另带使用点行号提示（phi 分支来源——条件标注）。
func (ext *fieldExtractor) emitConstAt(c *ssa.Const, lineHint int, varName string) (domain.CanonicalID, error) {
	funcID := ext.funcID
	typeStr := "nil"
	val := "nil"
	if c.Type() != nil {
		typeStr = ext.typeString(c.Type())
	}
	if c.Value != nil {
		val = c.Value.String()
	}
	short := ext.allocTypeShort(typeStr)
	if short == "" {
		short = typeStr
	}
	id := domain.CanonicalID(string(funcID) + "#const:" + short + ":" + val)
	if old, ok := ext.values[c]; ok && old != "" {
		return old, nil
	}
	ext.values[c] = id
	line := lineOf(ext, c)
	if line == 0 {
		line = ext.constAssignLine(varName, val)
	}
	if line == 0 {
		line = lineHint
	}
	n := &domain.CodeEntity{
		ID:        id,
		Kind:      domain.KindSSAValue,
		Name:      val,
		FilePath:  ext.currentFile,
		LineStart: line,
		Properties: map[string]any{
			"origin_kind": "literal",
			"ssa_op":      "const",
			"type_string": typeStr,
			"const_value": val,
			"func_id":     string(funcID),
		},
	}
	return id, ext.emit(domain.Item{Node: n})
}

// constAssignLine 常量在源码里的赋值行：按 phi 变量名找赋值目标，再用
// 标识符名在包作用域解析常量值（StateQueued → "queued"）。命中不了返回 0。
func (ext *fieldExtractor) constAssignLine(varName, val string) int {
	if varName == "" || ext.fn == nil || ext.fn.Pkg == nil || ext.fn.Pkg.Pkg == nil {
		return 0
	}
	scope := ext.fn.Pkg.Pkg.Scope()
	for _, t := range ext.assignTargets {
		if t.name != varName {
			continue
		}
		name, ok := ext.idents.lookup(t.start)
		if !ok || name == "" {
			continue
		}
		obj := scope.Lookup(name)
		if obj == nil {
			continue
		}
		cnt, ok := obj.(*types.Const)
		if !ok || cnt.Val() == nil {
			continue
		}
		if cnt.Val().String() == val {
			return ext.prog.Fset.PositionFor(t.start, false).Line
		}
	}
	return 0
}

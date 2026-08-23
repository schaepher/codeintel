package ssa

import (
	"fmt"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
	"golang.org/x/tools/go/ssa"
)

// newFieldAccessValue 创建 Field 读对应的字段节点。
func (ext *fieldExtractor) newFieldAccessValue(f *ssa.Field) *fieldAccess {
	logger := zap.L()
	logger.Debug("enter (fieldExtractor).newFieldAccessValue")
	defer logger.Debug("exit (fieldExtractor).newFieldAccessValue")
	info, ok := ext.fieldInfo(f.X.Type(), f.Field, f.Pos())
	if !ok {
		return nil
	}
	instance := ext.instancePath(f.X) + "." + info.fieldName
	if info.fullPath == "" {
		info.fullPath = instance
		ext.fallbackCount++
	}
	ext.recordEntry("read", info, instance)
	return &fieldAccess{
		id:       ext.accessID(instance, "read", f.Pos()),
		access:   "read",
		instance: instance,
		info:     info,
		ext:      ext,
	}
}

// emitFlow 发射 字段节点 → ssa_value 的 data_flows_to 边（Field 读）。
func (ext *fieldExtractor) emitFlow(from domain.CanonicalID, v ssa.Value) error {
	logger := zap.L()
	logger.Debug("enter (fieldExtractor).emitFlow")
	defer logger.Debug("exit (fieldExtractor).emitFlow")
	to, err := ext.emitValue(v)
	if err != nil || to == "" {
		return err
	}
	return ext.emitEdge(from, to)
}

// emitFlowValue 发射 ssa_value → 字段节点 的 data_flows_to 边（FieldAddr 基地址 / Store 写入值）。
func (ext *fieldExtractor) emitFlowValue(v ssa.Value, to domain.CanonicalID) error {
	logger := zap.L()
	logger.Debug("enter (fieldExtractor).emitFlowValue")
	defer logger.Debug("exit (fieldExtractor).emitFlowValue")
	from, err := ext.emitValue(v)
	if err != nil || from == "" {
		return err
	}
	return ext.emitEdge(from, to)
}

// emitEdge 发射 data_flows_to 边（tool_source=ssa，conf 1.0，Q69）。
func (ext *fieldExtractor) emitEdge(from, to domain.CanonicalID) error {
	logger := zap.L()
	logger.Debug("enter (fieldExtractor).emitEdge")
	defer logger.Debug("exit (fieldExtractor).emitEdge")
	return ext.emitEdgeKind(from, to, domain.FactDataFlowsTo)
}

// valueIDByInstance instancePath 名字的节点 ID（Q235-7）：类型短名
// （含 * / [] / . 特征——匿名分配回退）同函数内同名附加 @行号消歧
// （phi 两分支同类型匿名分配等，防 Q155 去重误合并）；源码变量名
// （arr）保持合并（shadowing 语义，与 Q205 一致）。
func (ext *fieldExtractor) valueIDByInstance(funcID domain.CanonicalID, v ssa.Value, name string) (domain.CanonicalID, string) {
	display := name
	if strings.ContainsAny(name, "*[].") {
		slots := ext.slotsFor[funcID]
		if slots == nil {
			slots = map[string]bool{}
			ext.slotsFor[funcID] = slots
		}
		if slots[name] {
			line := ext.prog.Fset.PositionFor(v.Pos(), false).Line
			display = fmt.Sprintf("%s@%d", name, line)
		} else {
			slots[name] = true
		}
	}
	return domain.CanonicalID(string(funcID) + "#" + display), display
}

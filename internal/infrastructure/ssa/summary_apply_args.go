package ssa

import (
	"fmt"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
	"golang.org/x/tools/go/ssa"
)

// applyArgSummary 对单个实参应用摘要（all 模式递归展开字段）。
func (ext *fieldExtractor) applyArgSummary(cc *ssa.CallCommon, calleeID domain.CanonicalID,
	spec summarySpec, argIdx int, arg ssa.Value) error {
	logger := zap.L()
	logger.Debug("enter (fieldExtractor).applyArgSummary")
	defer logger.Debug("exit (fieldExtractor).applyArgSummary")

	elems := variadicElems(arg)
	for _, el := range elems {
		if err := ext.applyArgSummaryOne(cc, calleeID, spec, el); err != nil {
			return err
		}
	}
	return nil
}

// applyArgSummaryOne 对单个实参值应用摘要。
func (ext *fieldExtractor) applyArgSummaryOne(cc *ssa.CallCommon, calleeID domain.CanonicalID,
	spec summarySpec, arg ssa.Value) error {
	logger := zap.L()
	logger.Debug("enter (fieldExtractor).applyArgSummaryOne")
	defer logger.Debug("exit (fieldExtractor).applyArgSummaryOne")
	line := ext.prog.Fset.PositionFor(cc.Pos(), false).Line

	realArg := arg
	if mi, ok := arg.(*ssa.MakeInterface); ok {
		realArg = mi.X
	}
	fields := spec.Reads
	if spec.WritesAll {
		fields = expandAllFields(realArg.Type(), 0)
	}
	if spec.ReadArgsAll {
		fields = expandAllFields(realArg.Type(), 0)
	}
	argID, err := ext.emitValue(realArg)
	if err != nil {
		return err
	}
	base := ext.instancePath(realArg)
	for _, f := range fields {
		fullPath := f

		if !strings.Contains(f, "/") {
			if named := namedStructOf(realArg.Type()); named != nil {
				seg := f
				if i := strings.LastIndex(f, "."); i >= 0 {
					seg = f[i+1:]
				}
				fullPath = named.Obj().Pkg().Path() + "." + named.Obj().Name() + "." + seg
			}
		}
		access := "read"
		if spec.WritesAll || contains(spec.Writes, f) {
			access = "write"
		}
		instance := base + "." + lastPathSeg(f)
		id := domain.CanonicalID(string(ext.funcID) + "#ext." + fullPath + "." + access + "@" + fmt.Sprintf("%d", line))
		node := &domain.CodeEntity{
			ID:        id,
			Kind:      domain.KindFieldAccess,
			Name:      instance,
			FilePath:  ext.currentFile,
			LineStart: line,
			LineEnd:   line,
			Properties: map[string]any{
				"full_path":     fullPath,
				"instance_path": instance,
				"access_kind":   access,
				"type_string":   realArg.Type().String(),
				"func_id":       string(ext.funcID),
				"is_external":   "true",
				"code_snippet":  ext.sourceLine(ext.currentFile, line),
			},
		}
		if err := ext.emit(domain.Item{Node: node}); err != nil {
			return err
		}

		if err := ext.emitEdgeKind(calleeID, id, domain.FactSummaryIO); err != nil {
			return err
		}
		if access == "write" {

			if err := ext.emitEdgeKind(ext.funcID, id, domain.FactIndirectWrite); err != nil {
				return err
			}
			if argID != "" {
				if err := ext.emitEdge(argID, id); err != nil {
					return err
				}
			}

			if ext.funcData != nil {
				ext.funcData.indirectWrites = append(ext.funcData.indirectWrites, fieldEntry{
					fieldPath:    fullPath,
					instancePath: instance,
					line:         line,
					snippet:      ext.sourceLine(ext.currentFile, line),
				})
			}
		} else if argID != "" {

			if err := ext.emitEdge(id, argID); err != nil {
				return err
			}
		}
	}
	return nil
}

// applyScanOut 处理 Scan 写 out 实参（表关联链贯通）：
// row.Scan(&x) —— 接收者（row 值）→ 实参指向的局部变量节点
// （变量名 ID find#x，与 Load 归一节点一致）。数据流链：
// table_a.x.read → row → x → table_b.y.filter。
func (ext *fieldExtractor) applyScanOut(cc *ssa.CallCommon, calleeID domain.CanonicalID) error {
	logger := zap.L()
	logger.Debug("enter (fieldExtractor).applyScanOut")
	defer logger.Debug("exit (fieldExtractor).applyScanOut")
	if len(cc.Args) < 1 {
		return nil
	}
	recvID, err := ext.emitValue(cc.Args[0])
	if err != nil || recvID == "" {
		return nil
	}
	line := ext.prog.Fset.PositionFor(cc.Pos(), false).Line

	for i := 1; i < len(cc.Args); i++ {
		for _, arg := range variadicElems(cc.Args[i]) {

			real := arg
			if mi, ok := arg.(*ssa.MakeInterface); ok {
				real = mi.X
			}
			name := ext.instancePath(real)
			if isSSAName(name) {
				continue
			}
			fid, ok := ext.funcIDOf(real)
			if !ok {
				continue
			}
			id := domain.CanonicalID(string(fid) + "#" + name)
			if err := ext.emit(domain.Item{Node: &domain.CodeEntity{
				ID:        id,
				Kind:      domain.KindSSAValue,
				Name:      name,
				FilePath:  ext.currentFile,
				LineStart: line,
				Properties: map[string]any{
					"origin_kind": "local",
					"ssa_op":      "scan_out",
					"type_string": arg.Type().String(),
					"func_id":     string(fid),
				},
			}}); err != nil {
				return err
			}
			if err := ext.emitEdgeKindLine(recvID, id, domain.FactDataFlowsTo, line); err != nil {
				return err
			}
		}
	}
	return nil
}

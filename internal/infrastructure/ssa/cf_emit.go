package ssa

import (
	"go/token"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
	"golang.org/x/tools/go/ssa"
)

// emitCrossFlow 发射单个函数的跨过程边并记录摘要数据。
func (ext *fieldExtractor) emitCrossFlow() error {
	logger := zap.L()
	logger.Debug("enter (fieldExtractor).emitCrossFlow")
	defer logger.Debug("exit (fieldExtractor).emitCrossFlow")
	for _, b := range ext.fn.Blocks {
		for _, instr := range b.Instrs {
			switch v := instr.(type) {
			case *ssa.Phi:
				if err := ext.emitPhi(v); err != nil {
					return err
				}
			case *ssa.Call:
				if err := ext.emitCall(&v.Call, v); err != nil {
					return err
				}
			case *ssa.Go:
				if err := ext.emitCall(&v.Call, nil); err != nil {
					return err
				}
			case *ssa.Defer:
				if err := ext.emitCall(&v.Call, nil); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// emitPhi 发射 phi_operand 边（todo12-10：常量分支也发射——状态初始化/
// 条件改写来源必须能追到常量，否则 value-trace 只剩 phi 空壳）。
func (ext *fieldExtractor) emitPhi(phi *ssa.Phi) error {
	logger := zap.L()
	logger.Debug("enter (fieldExtractor).emitPhi")
	defer logger.Debug("exit (fieldExtractor).emitPhi")
	phiID, err := ext.emitValue(phi)
	if err != nil || phiID == "" {
		return err
	}
	varName := ext.instancePath(phi)
	if isSSAName(varName) {
		varName = ""
	}
	for i, op := range phi.Edges {
		lineHint := 0
		if i < len(phi.Block().Preds) {
			pred := phi.Block().Preds[i]
			{
				for j := len(pred.Instrs) - 1; j >= 0; j-- {
					if p := pred.Instrs[j].Pos(); p != token.NoPos {
						lineHint = ext.prog.Fset.PositionFor(p, false).Line
						break
					}
				}
			}
		}
		var opID domain.CanonicalID
		var err error
		if c, ok := op.(*ssa.Const); ok {
			opID, err = ext.emitConstAt(c, lineHint, varName)
		} else {
			opID, err = ext.emitValue(op)
		}
		if err != nil || opID == "" {
			continue
		}
		if err := ext.emitEdgeKind(opID, phiID, domain.FactPhiOperand); err != nil {
			return err
		}
	}
	return nil
}

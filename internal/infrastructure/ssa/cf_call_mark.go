package ssa

import (
	"go/types"

	"github.com/schaepher/codeintel/internal/domain"
	"golang.org/x/tools/go/ssa"
)

// markUnknownCall 记录未知回调调用（reason/detail/源码位置）。
func (ext *fieldExtractor) markUnknownCall(cc *ssa.CallCommon, callVal ssa.Value) {
	if ext.unknownCalls == nil {
		ext.unknownCalls = map[ssa.Value]unknownCallMark{}
	}
	reason, detail := "dynamic", "被调目标无法解析（动态调用/未知回调）"
	switch v := cc.Value.(type) {
	case *ssa.Parameter:
		reason = "callback"
		detail = "被调是函数形参 " + v.Name() + "（未知回调，返回值来源未建模）"
	case *ssa.FreeVar:
		reason = "closure"
		detail = "被调是闭包自由变量 " + v.Name() + "（未知回调，返回值来源未建模）"
	case *ssa.MakeClosure:
		reason = "closure"
		detail = "被调是闭包值（未知回调，返回值来源未建模）"
	}
	pos := ext.prog.Fset.PositionFor(cc.Pos(), false)
	line := pos.Line
	file := relPath(ext.repo.Path, pos.Filename)
	mark := unknownCallMark{reason: reason, detail: detail, line: line, file: file}

	if id, ok := ext.values[callVal]; ok && id != "" {
		_ = ext.emit(domain.Item{Node: &domain.CodeEntity{ID: id, Kind: domain.KindSSAValue,
			Name: callVal.Name(), Properties: map[string]any{
				"call_unknown": reason, "call_reason": detail, "call_line": line, "call_file": file}}})
		return
	}
	ext.unknownCalls[callVal] = mark
	if _, err := ext.emitValue(callVal); err != nil {
		return
	}
}

// dispatchOriginOf 判定候选实现的派发来源（Q161）：注册点命中
// （MakeInterface 具体值 → 接口，见 emitDispatches）→ register 0.9；
// 否则枚举兜底 enum 0.7。注册点收集一次缓存（全 prog 扫描开销大）。
// Q168：注册命中按 (iface, candidateKey) 预处理成 map——原逐调用点
// 线性扫描注册点（动态调用点多时 O(调用点×注册点)）→ O(1) 查找。
func (ext *fieldExtractor) dispatchOriginOf(iface *types.Named, method string, implFn *types.Func) (string, float64) {

	if ext.regHits[ext.typeString(iface)][ext.candidateKeyOf(implFn)] {
		return "register", 0.9
	}
	return "enum", 0.7
}

// recordCallInfo 记录调用摘要条目（间接写闭包消费：emitSummaries 沿
// fd.calls 传播被调函数写）。常量实参（nil、字面量）不产生实例传递，
// 不参与类型匹配；实参类型路径用于与被调函数写字段的声明类型匹配
// （Q36；Q157 展开嵌套字段 owner 链——OrderModel 含 Order 字段时
// 实现写 Order.FinalFee 也能匹配）。
func (ext *fieldExtractor) recordCallInfo(cc *ssa.CallCommon, calleeID domain.CanonicalID) {
	if ext.funcData == nil {
		return
	}
	var argPaths, argNames []string
	for _, arg := range cc.Args {
		if _, isConst := arg.(*ssa.Const); isConst {
			continue
		}
		for _, p := range ext.ownerTypesOfCached(arg.Type()) {
			argPaths = append(argPaths, p)
		}

		name := ext.instancePath(arg)
		if isSSAName(name) {
			name = arg.Name()
		}
		argNames = append(argNames, name)
	}

	if len(argPaths) == 0 {
		return
	}
	ext.funcData.calls = append(ext.funcData.calls, callInfo{
		calleeID:       calleeID,
		argStructPaths: argPaths,
		callLine:       ext.prog.Fset.PositionFor(cc.Pos(), false).Line,
		callPos:        int(cc.Pos()),
		argNames:       argNames,
	})
}

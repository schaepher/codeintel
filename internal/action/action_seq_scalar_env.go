package action

// U04c：seqEnv 的标量槽——来源绑定、持续逃逸、赋值/声明记录与分支合并。
//
// 失效规则（刻意保守）：
//   - 普通覆盖替换来源；compound/incdec 与循环写者不能保留循环前来源；
//   - 取址（`&x` / `p:=&x`）或闭包内写者使声明槽**持续逃逸**：之后的
//     赋值也不能恢复确定来源；
//   - 分支合并：来源相同可保留，不同/缺失 → 未知；
//   - 阴影按 AST 对象隔离（不污染外层同名变量）。

import (
	"go/ast"
	"go/token"
)

// seqScalarSlot 一个标量声明槽的来源状态。
type seqScalarSlot struct {
	origin *SeqStorageOrigin
	// escaped/escapedAt 取址或闭包写者 → 从该位置起持续失效（同一语句里
	// 之前已求值的实参仍保留来源：Go 求值顺序）。
	escaped   bool
	escapedAt token.Pos
}

// scalarSlot 读取槽状态（父环境 = 闭包捕获）。
func (e *seqEnv) scalarSlot(obj *ast.Object) (seqScalarSlot, bool) {
	if obj == nil {
		return seqScalarSlot{}, false
	}
	for env := e; env != nil; env = env.parent {
		if s, ok := env.scalars[obj]; ok {
			return s, true
		}
	}
	return seqScalarSlot{}, false
}

// setScalar 写槽：已逃逸的槽不能恢复来源（只更新为未知）。
func (e *seqEnv) setScalar(obj *ast.Object, origin *SeqStorageOrigin) {
	if e == nil || obj == nil {
		return
	}
	if e.scalars == nil {
		e.scalars = map[*ast.Object]seqScalarSlot{}
	}
	slot, _ := e.scalarSlot(obj)
	if slot.escaped {
		// 逃逸前已求值的读取仍可用旧来源（位置判定）；之后的读取被挡。
		e.scalars[obj] = seqScalarSlot{escaped: true, escapedAt: slot.escapedAt, origin: slot.origin}
		return
	}
	e.scalars[obj] = seqScalarSlot{origin: cloneSeqScalarOrigin(origin)}
}

// setScalarScanTarget U04e：标准 Scan 的地址传递不保留指针——允许在同一次
// 调用内绑定列来源；**此前的**取址逃逸（escapedAt 早于调用位置）仍然有效。
func (e *seqEnv) setScalarScanTarget(obj *ast.Object, origin *SeqStorageOrigin, callPos token.Pos) bool {
	if e == nil || obj == nil || origin == nil {
		return false
	}
	slot, ok := e.scalarSlot(obj)
	if ok && slot.escaped {
		// escapedAt == NoPos 视为"更早/不定位"的持续逃逸；只有本次语句里
		// 本次调用之后出现的取址（escapedAt >= callPos）可被这次 Scan 覆盖。
		if slot.escapedAt == token.NoPos || slot.escapedAt < callPos {
			return false
		}
	}
	if e.scalars == nil {
		e.scalars = map[*ast.Object]seqScalarSlot{}
	}
	e.scalars[obj] = seqScalarSlot{origin: cloneSeqScalarOrigin(origin)}
	return true
}

// seqScalarOriginAtAddr 取址操作数的来源：`&x` 自身造成的逃逸从 `&` 起生效，
// 不应挡住操作数在**同一地址表达式**里的读取；更早的逃逸仍然有效。
func seqScalarOriginAtAddr(req CodeSequenceRequest, id *ast.Ident, ampPos token.Pos) *SeqStorageOrigin {
	if req.env == nil || id == nil || id.Obj == nil {
		return nil
	}
	slot, ok := req.env.scalarSlot(id.Obj)
	if !ok || slot.origin == nil {
		return nil
	}
	if slot.escaped && (slot.escapedAt == token.NoPos || slot.escapedAt < ampPos) {
		return nil
	}
	return cloneSeqScalarOrigin(slot.origin)
}

// clearScanPassEscape 标准 Scan 的地址传递不保留指针：只清除本次调用
// （escapedAt >= callPos）的逃逸标记；更早的持续失效保留。
func (e *seqEnv) clearScanPassEscape(obj *ast.Object, callPos token.Pos) {
	if e == nil || obj == nil {
		return
	}
	slot, ok := e.scalarSlot(obj)
	if !ok || !slot.escaped {
		return
	}
	if slot.escapedAt == token.NoPos || slot.escapedAt < callPos {
		return
	}
	e.scalars[obj] = seqScalarSlot{origin: slot.origin}
}

// markScalarEscaped 标记持续逃逸（本地层写——阴影对象互不影响）。
func (e *seqEnv) markScalarEscaped(obj *ast.Object) {
	e.markScalarEscapedAt(obj, token.NoPos)
}

// markScalarEscapedAt 从 pos 起失效（同一语句中更早求值的实参不受影响）。
func (e *seqEnv) markScalarEscapedAt(obj *ast.Object, pos token.Pos) {
	if e == nil || obj == nil {
		return
	}
	if e.scalars == nil {
		e.scalars = map[*ast.Object]seqScalarSlot{}
	}
	slot, ok := e.scalarSlot(obj)
	if !ok {
		e.scalars[obj] = seqScalarSlot{escaped: true, escapedAt: pos}
		e.markRelatedEscaped(obj, pos)
		return
	}
	// 逃逸点之前的读取仍可用旧来源（位置判定）；之后的读取被 escapedAt 挡住。
	// 已有逃逸时保留**最早**的逃逸位置（持续失效：后续语句里的新取址不能
	// 把失效起点推后，否则标准 Scan 的本次地址传递会误认作首次逃逸）。
	switch {
	case !slot.escaped:
		slot.escaped = true
		slot.escapedAt = pos
	case pos == token.NoPos:
		slot.escapedAt = token.NoPos
	case slot.escapedAt == token.NoPos:
		// 保持"从一开始就失效"
	case pos < slot.escapedAt:
		slot.escapedAt = pos
	}
	e.scalars[obj] = slot
	e.markRelatedEscaped(obj, pos)
}

// markRelatedEscaped 取址/闭包写者同时使错误/行数/事务槽持续失效。
func (e *seqEnv) markRelatedEscaped(obj *ast.Object, pos token.Pos) {
	// U04n：先清事务错误（setTxErr 会删除同变量的写入错误槽），再标记
	// 写入错误/行数——否则刚写入的逃逸标记会被 setTxErr 的 delete 抹掉。
	e.markTxEscaped(obj)
	e.setTxErr(obj, nil)
	// U04h/U04j：取址/闭包写者同样使错误结果/行数变量失效。
	e.markErrEscaped(obj, pos)
	e.markRowsEscaped(obj, pos)
}

// scalarOriginAt 该使用位置是否仍可用槽来源（逃逸从逃逸点起生效）。
func (e *seqEnv) scalarOriginAt(obj *ast.Object, pos token.Pos) (*SeqStorageOrigin, bool) {
	slot, ok := e.scalarSlot(obj)
	if !ok {
		return nil, false
	}
	if slot.escaped {
		if pos == token.NoPos || slot.escapedAt == token.NoPos || slot.escapedAt <= pos {
			return nil, true
		}
	}
	return slot.origin, true
}

// copyScalars 克隆标量槽表（clone 用；始终返回可写表）。
func (e *seqEnv) copyScalars() map[*ast.Object]seqScalarSlot {
	if e == nil || len(e.scalars) == 0 {
		return map[*ast.Object]seqScalarSlot{}
	}
	out := make(map[*ast.Object]seqScalarSlot, len(e.scalars))
	for k, v := range e.scalars {
		out[k] = v
	}
	return out
}

// mergeScalars 分支合并：每条可达路径一个候选（分支未写 → 分支前的值），
// 来源全相同才保留，否则未知；逃逸取并集。
func (e *seqEnv) mergeScalars(branches []*seqEnv) {
	if e == nil || len(branches) == 0 {
		return
	}
	if e.scalars == nil {
		e.scalars = map[*ast.Object]seqScalarSlot{}
	}
	objs := map[*ast.Object]bool{}
	for _, br := range branches {
		if br == nil {
			continue
		}
		for obj := range br.scalars {
			objs[obj] = true
		}
	}
	for obj := range objs {
		cands := make([]seqScalarSlot, 0, len(branches))
		for _, br := range branches {
			if br == nil {
				continue
			}
			s, ok := br.scalars[obj]
			if !ok {
				s = e.scalars[obj]
			}
			cands = append(cands, s)
		}
		e.scalars[obj] = seqMergeScalarCandidates(cands)
	}
}

// mergePossibleScalars 循环体可能 0 次执行：循环后值与循环前值都是候选。
func (e *seqEnv) mergePossibleScalars(pre *seqEnv) {
	if e == nil || pre == nil {
		return
	}
	objs := map[*ast.Object]bool{}
	for obj := range e.scalars {
		objs[obj] = true
	}
	for obj := range pre.scalars {
		objs[obj] = true
	}
	for obj := range objs {
		current, okCur := e.scalars[obj]
		before, okPre := pre.scalars[obj]
		if !okCur {
			current = before
		}
		if !okPre {
			before = current
		}
		e.scalars[obj] = seqMergeScalarCandidates([]seqScalarSlot{current, before})
	}
}

// seqMergeScalarCandidates 候选合并：任一逃逸 → 逃逸；来源不同/缺失 → 未知。
func seqMergeScalarCandidates(cands []seqScalarSlot) seqScalarSlot {
	escaped := false
	var first *SeqStorageOrigin
	same := true
	for i, c := range cands {
		if c.escaped {
			escaped = true
		}
		if i == 0 {
			first = c.origin
			continue
		}
		if !sameSeqScalarOrigin(first, c.origin) {
			same = false
		}
	}
	if escaped {
		at := token.NoPos
		for _, c := range cands {
			if c.escaped {
				at = c.escapedAt
				break
			}
		}
		return seqScalarSlot{escaped: true, escapedAt: at}
	}
	if !same {
		return seqScalarSlot{}
	}
	return seqScalarSlot{origin: cloneSeqScalarOrigin(first)}
}

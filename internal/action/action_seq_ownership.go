package action

// B02：构造函数返回的分配与已证明只读的参数调用（所有权/字段值传递）。
//
// 只在"构造返回"或"被调函数体与全部出口足以证明不修改/不保留对象"时传递
// 当前字段状态；全局/容器/通道/未知调用/捕获一律失效。

import (
	"go/ast"

	"github.com/schaepher/codeintel/internal/domain"
)

// seqRebaseReturnedAllocation 构造调用返回的分配在调用方重新归属：分配 ID 不变
// （同一对象），但 Entry/Symbol/文件位置切到调用方（否则跨函数入口不匹配）。
func seqRebaseReturnedAllocation(req CodeSequenceRequest, call *ast.CallExpr,
	origin *SeqStorageOrigin) *SeqStorageOrigin {
	if origin == nil || origin.Kind != SeqOriginAllocation || origin.Allocation == nil || call == nil {
		return origin
	}
	if origin.Allocation.Entry == req.entryID {
		return origin
	}
	cp := cloneSeqScalarOrigin(origin)
	cp.Allocation.Entry = req.entryID
	cp.Allocation.Symbol = req.curSym
	cp.Allocation.File = req.curFile
	if req.curCtx != nil && req.curCtx.fset != nil && call.Lparen.IsValid() {
		cp.Allocation.Offset = req.curCtx.fset.Position(call.Lparen).Offset
	}
	return cp
}

// seqRebaseAssignOrigins 赋值/声明中调用返回的分配重新归属。
func seqRebaseAssignOrigins(req CodeSequenceRequest, rhs []ast.Expr, origins []*SeqStorageOrigin) {
	for i := range origins {
		if i < len(rhs) {
			if call, ok := runtimeUnparen(rhs[i]).(*ast.CallExpr); ok {
				origins[i] = seqRebaseReturnedAllocation(req, call, origins[i])
			}
		}
	}
}

// seqPreInvalidateCall 调用前失效：未知调用（不是只读证明/标准 Scan）把实参
// 对象/分配标为传出并清掉字段值——被调函数体因此读不到旧列值。
func seqPreInvalidateCall(req CodeSequenceRequest, call *ast.CallExpr) {
	if req.env == nil || call == nil {
		return
	}
	if seqIsStandardScanCandidate(req, call) {
		return
	}
	if seqReadOnlyObserveCall(req, call) {
		return
	}
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
		if id, ok2 := runtimeUnparen(sel.X).(*ast.Ident); ok2 && id.Obj != nil {
			seqInvalidateReceiver(req, id)
		}
	}
	for _, arg := range call.Args {
		seqInvalidateEscapedArg(req, arg)
	}
}

// seqIsStandardScanCandidate 该调用将是标准 Scan（接收者已有读取结果身份）。
func seqIsStandardScanCandidate(req CodeSequenceRequest, call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Scan" {
		return false
	}
	return seqScanReceiverRead(req, call) != nil
}

// seqSeedParamFieldSlots 被调函数形参持有分配时，把调用方该分配的字段槽快照
// 复制到被调环境（只读证明成立时调用方槽未被清除）。
func seqSeedParamFieldSlots(req CodeSequenceRequest, env *seqEnv, params *ast.FieldList,
	origins []*SeqStorageOrigin) {
	if req.env == nil || env == nil || params == nil {
		return
	}
	idx := 0
	for _, f := range params.List {
		n := len(f.Names)
		if n == 0 {
			n = 1
		}
		for i := 0; i < n; i++ {
			if idx >= len(origins) {
				return
			}
			o := origins[idx]
			if o != nil && o.Kind == SeqOriginAllocation && o.Allocation != nil {
				seqCopyAllocSlots(req.env, env, o.Allocation.ID)
			}
			idx++
		}
	}
}

// seqCopyAllocSlots 从调用方环境复制某分配的字段槽到目标环境。
func seqCopyAllocSlots(src, dst *seqEnv, allocID string) {
	if src == nil || dst == nil || allocID == "" {
		return
	}
	for e := src; e != nil; e = e.parent {
		for k, v := range e.fields {
			if k.alloc == allocID {
				if dst.fields == nil {
					dst.fields = map[seqFieldSlotKey]seqFieldSlot{}
				}
				dst.fields[k] = v
			}
		}
		if e.fieldsUnknown {
			break
		}
	}
}

// B07：局部变量初始化为"已证明单一返回对象"（工厂/构造返回槽）时，接口方法
// 解析到该具体类型的方法（受实例读取值影响的重分析在这里落地）。
func seqReturnSlotImplMethod(a *Actions, req CodeSequenceRequest, sel *ast.SelectorExpr) (string, bool) {
	if a == nil || sel == nil {
		return "", false
	}
	id, ok := runtimeUnparen(sel.X).(*ast.Ident)
	if !ok || id.Obj == nil || req.env == nil {
		return "", false
	}
	o := seqScalarOriginOf(req, id)
	if o == nil {
		return "", false
	}
	typeID := ""
	switch o.Kind {
	case SeqOriginStructLiteral:
		if o.StructLiteral != nil {
			typeID = o.StructLiteral.TypeID
		}
	case SeqOriginAllocation:
		if o.Allocation != nil {
			typeID = o.Allocation.TypeID
		}
	case SeqOriginStructCopy:
		if o.StructCopy != nil {
			typeID = o.StructCopy.TypeID
		}
	}
	if typeID == "" {
		return "", false
	}
	pkg := pkgOfID(domain.CanonicalID(typeID))
	typ := pkgNameOfID(typeID)
	if pkg == "" || typ == "" {
		return "", false
	}
	methodID := "symbol:go:" + pkg + ":(" + typ + ")." + sel.Sel.Name
	if _, err := a.repo.GetSymbol(domain.CanonicalID(methodID)); err != nil {
		return "", false
	}
	return methodID, true
}

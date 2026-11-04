package action

// T09b：静态类型指向接口方法、接收者具体类型已证明时收窄调用目标。

import (
	"go/ast"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// seqRefineInterfaceTarget 索引 calls 边按**静态类型**指向接口方法，而接收者
// 变量的具体类型已由构造器/返回槽证明时（U04u/T09b），把目标收窄到具体实现
// 方法（必须证明该类型实现该接口）。仅影响目标身份，不改变候选/派发语义。
func seqRefineInterfaceTarget(a *Actions, req CodeSequenceRequest, fun ast.Expr,
	tid string) string {
	if a == nil || a.repo == nil || !strings.Contains(tid, ":(") || req.env == nil {
		return tid
	}
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return tid
	}
	recv, ok := sel.X.(*ast.Ident)
	if !ok || recv.Obj == nil {
		return tid
	}
	ref, ok := seqLookupType(req.env, recv.Obj)
	if !ok || ref.typeName == "" || ref.pkgPath == "" {
		return tid
	}
	decl := seqDeclNameOf(tid) // "(I).M"
	open := strings.Index(decl, "(")
	closeP := strings.Index(decl, ")")
	if open != 0 || closeP <= open+1 {
		return tid
	}
	ifaceName := decl[open+1 : closeP]
	methodName := strings.TrimPrefix(decl[closeP+1:], ".")
	if methodName == "" {
		return tid
	}
	pkgPrefix := tid[:strings.LastIndex(tid, ":(")]
	ifaceID := pkgPrefix + ":" + ifaceName
	if pkgOfID(domain.CanonicalID(ifaceID)) == ref.pkgPath &&
		pkgNameOfID(ifaceID) == ref.typeName {
		return tid // 接收者就是接口本身
	}
	implTypeID := "symbol:go:" + ref.pkgPath + ":" + ref.typeName
	impls, err := a.InterfaceImplements(req.Ctx, ifaceID)
	if err != nil || !seqContainsString(impls, implTypeID) {
		return tid
	}
	concrete := GrpcMethodEntryID(implTypeID, methodName)
	if concrete == "" {
		return tid
	}
	if n, err := a.repo.GetSymbol(domain.CanonicalID(concrete)); err != nil || n == nil {
		return tid
	}
	return concrete
}

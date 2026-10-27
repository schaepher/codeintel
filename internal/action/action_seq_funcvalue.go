package action

// todo22 §2（第一增量）：函数值/闭包的数据流模型——值表示与环境。
//
// 设计（用户 2026-10-27 确认）：
//   - 函数字面量**只建立函数值**；只有在实际调用处才展开其函数体（未调用
//     的局部字面量、传入但未被调用的回调都不产出内部调用）。
//   - 回调按**调用点的数据流**证明执行：实参 → 形参绑定 → 形参调用处展开，
//     证据记录绑定位置/调用位置/依据（不只是一个"已证明"标签）。
//   - 确定不执行（未调用/调用前被覆盖）→ 不展开、不画箭头；
//     可能执行但证据不足（未绑定/未知改写/逃逸/多值未消歧）→ 保留
//     明确 unknown 节点与原因，不展开内部执行。
//
// 值/环境按 AST 对象身份（`*ast.Object`）区分——同名不同作用域的变量不是
// 同一个绑定；环境随语句顺序更新（赋值先求 RHS 后更新 LHS）。

import (
	"go/ast"
	"go/token"
	"strconv"
	"strings"
)

// 函数值来源（决定展开证据的 reason）。
const (
	seqOriginLiteral = "literal" // IIFE：调用处直接是函数字面量
	seqOriginArg     = "arg"     // 从调用实参绑定到形参（回调）
	seqOriginVar     = "var"     // 局部变量/包级函数的到达定义
)

// 函数值种类。
const (
	seqValueUnknown = iota
	seqValueLit     // 函数字面量
	seqValueNamed   // 包级函数/方法（canonical ID 已知）
	seqValueMulti   // 条件/循环下多个值都可能到达（未消歧）
	seqValueList    // 函数列表（[]func / 变参列表，元素按顺序）
)

// seqFileCtx 字面量所在的解析文件（跨函数传递函数值时必需：字面量的位置与
// 源码属于**定义它的文件**，不是当前正在遍历的函数所在文件）。
type seqFileCtx struct {
	fset *token.FileSet
	file *ast.File
	src  []byte
	rel  string
}

// seqValue 一个函数值（或显式的未知）。
type seqValue struct {
	kind      int
	origin    string
	lit       *ast.FuncLit
	ctx       *seqFileCtx
	env       *seqEnv // 创建字面量时的环境（闭包捕获）
	enclosing string  // 字面量所在函数的 canonical ID（调用边归属）
	pkgPath   string  // 字面量所在函数的包路径（未解析调用分类用）
	recvType  string  // 字面量所在函数的 receiver（局部方法调用具体化用）
	recvDecl  map[string]string
	symID     string // named：目标 canonical ID
	name      string // 显示名（多值候选/诊断）
	why       string // unknown 原因
	cands     []seqValue
	elems     []seqValue // list：元素（按源码/变参顺序）
	// todo22 §2.1：绑定元数据——声明对象（区分同名阴影）、调用上下文
	// （绑定发生的文件/行）与条件（在哪个分支条件下成立）。
	declObj  *ast.Object
	bindFile string
	bindLine int
	cond     string
}

func unknownSeqValue(why string) seqValue {
	return seqValue{kind: seqValueUnknown, why: why}
}

func namedSeqValue(symID string) seqValue {
	return seqValue{kind: seqValueNamed, symID: symID, name: seqValueShortName(symID)}
}

// seqValueShortName canonical ID → 短名（pkg 后的包名 + 符号名）。
func seqValueShortName(symID string) string {
	rest := strings.TrimPrefix(symID, "symbol:go:")
	if i := strings.LastIndex(rest, ":"); i >= 0 {
		return rest[i+1:]
	}
	return rest
}

// literalSeqValue 由字面量构造函数值（捕获当前文件上下文与环境）。
func literalSeqValue(req CodeSequenceRequest, ctx *seqFileCtx, env *seqEnv, lit *ast.FuncLit, origin string) seqValue {
	return seqValue{kind: seqValueLit, origin: origin, lit: lit, ctx: ctx, env: env,
		enclosing: req.curSym, pkgPath: pkgPathOf(req.curSym),
		recvType: req.curRecvType, recvDecl: req.curRecvDecl}
}

// label 展示用名字（多值候选/unknown 说明）。
func (v seqValue) label() string {
	switch v.kind {
	case seqValueLit:
		if v.ctx != nil && v.ctx.fset != nil && v.ctx.src != nil {
			if sig := seqExprText(v.ctx.fset, v.ctx.src, v.lit.Type); sig != "" {
				return sig // 形如 func(inner context.Context) error
			}
		}
		return "func(){…}"
	case seqValueNamed:
		return v.name
	case seqValueMulti:
		parts := make([]string, 0, len(v.cands))
		for _, c := range v.cands {
			parts = append(parts, c.label())
		}
		return strings.Join(parts, " | ")
	case seqValueList:
		return "list(" + strconv.Itoa(len(v.elems)) + ")"
	default:
		return "unknown"
	}
}

// equal 判断两个值是否等价（去重/分支合并用）。
func (v seqValue) equal(o seqValue) bool {
	if v.kind != o.kind {
		return false
	}
	switch v.kind {
	case seqValueLit:
		return v.lit == o.lit && v.cond == o.cond && v.declObj == o.declObj
	case seqValueNamed:
		return v.symID == o.symID && v.cond == o.cond && v.declObj == o.declObj
	case seqValueUnknown:
		return v.why == o.why
	case seqValueMulti:
		if len(v.cands) != len(o.cands) {
			return false
		}
		for i := range v.cands {
			if !v.cands[i].equal(o.cands[i]) {
				return false
			}
		}
		return true
	case seqValueList:
		if len(v.elems) != len(o.elems) {
			return false
		}
		for i := range v.elems {
			if !v.elems[i].equal(o.elems[i]) {
				return false
			}
		}
		return true
	}
	return false
}

// multiSeqValue 合并候选：去重后 0 个 → unknown，1 个 → 该值，多个 → multi。
func multiSeqValue(cands []seqValue, why string) seqValue {
	var uniq []seqValue
	for _, c := range cands {
		dup := false
		for _, u := range uniq {
			if u.equal(c) {
				dup = true
				break
			}
		}
		if !dup {
			uniq = append(uniq, c)
		}
	}
	switch len(uniq) {
	case 0:
		return unknownSeqValue(why)
	case 1:
		return uniq[0]
	}
	names := make([]string, 0, len(uniq))
	for _, u := range uniq {
		names = append(names, u.label())
	}
	return seqValue{kind: seqValueMulti, cands: uniq, name: strings.Join(names, " | "), why: why}
}

// listSeqValue 函数列表值（元素顺序即源码/变参顺序）。
func listSeqValue(elems []seqValue) seqValue {
	return seqValue{kind: seqValueList, elems: elems, name: "list(" + strconv.Itoa(len(elems)) + ")"}
}

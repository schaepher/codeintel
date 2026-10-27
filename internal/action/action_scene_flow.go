package action

// todo11-2：场景条件的**值流传播**（有界）与实现候选选择。
//
// 用户确认的 Q3/Q4：
//   - 自建"字面量来源解析器"：实参 → 变量链（同函数赋值）→ 形参（向上找
//     调用点实参），深度上限 2；非字面量即停（unknown:depth / unknown:value）
//   - **禁止按字段名相似推断同值**：只有同一变量/形参链、同一实参位置才算
//   - 接口/实现候选：若候选来自场景守卫的构造函数/赋值（同一函数内
//     `if cfg.X == "v" { x = A{} } else { x = B{} }`），按场景选候选并把
//     依据记进 evidence（reason=scene-guard:<k=v>）；无法证明 → 保持候选
//     集合 + ambiguous（不静默选第一个）

import (
	"go/ast"
	"go/token"
	"strings"
)

// maxSceneFlowDepth 值流解析深度（一层变量 + 一层形参；超出即 unknown）。
const maxSceneFlowDepth = 2

// sceneArg 实参解析结果（值 + 来源证据——todo12-3 R3）。
type sceneArg struct {
	val    string // 已知字面量；空 = 未知
	source string // 定义位置/传播依据（如 def@L20、scene-arg@L22）
	reason string // todo13-3：未知原因（如 call-may-write:rewrite@L18）
}

// resolveSceneArgLiterals 解析一次调用的实参字面量（位置对齐；解析不到为空）。
//
// 值来源只沿**当前展开路径**：函数内变量链（`x := "CN"`）+ 本次调用的入站
// 实参（incoming，按形参位置）。**绝不**去"任意调用者"里找同名实参——那会把
// 别的分支的字面量串进来（本轮实测：DispatchDyn 的 region 被 Dispatch 的
// "CN" 误判为 taken）。
func (a *Actions) resolveSceneArgLiterals(fset *token.FileSet, src []byte, call *ast.CallExpr,
	body *ast.BlockStmt, params *ast.FieldList, incoming, incomingSrc []string,
	consts sceneConstLookup, depth int) []sceneArg {
	if call == nil {
		return nil
	}
	out := make([]sceneArg, 0, len(call.Args))
	for _, arg := range call.Args {
		out = append(out, resolveLiteralExpr(fset, src, body, params, incoming, incomingSrc, consts, arg, depth))
	}
	return out
}

// resolveLiteralExpr 表达式 → (字面量值, 来源)；无法判定时值为空。
func resolveLiteralExpr(fset *token.FileSet, src []byte, body *ast.BlockStmt,
	params *ast.FieldList, incoming, incomingSrc []string,
	consts sceneConstLookup, expr ast.Expr, depth int) sceneArg {
	if depth < 0 {
		return sceneArg{}
	}
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind.String() == "STRING" {
			return sceneArg{val: strings.Trim(e.Value, "\"`"),
				source: "literal@" + fileLine("", fset.Position(e.Pos()).Line)}
		}
		return sceneArg{}
	case *ast.ParenExpr:
		return resolveLiteralExpr(fset, src, body, params, incoming, incomingSrc, consts, e.X, depth)
	case *ast.Ident:
		// todo12-3：解析调用点**之前实际可到达**的定义（无条件覆盖、
		// 条件合流、作用域遮蔽、定义顺序）；不扫别的函数补值。
		// todo13-3：取址/别名传入调用后旧定义失效；分析深度上限不得
		// 保留确定值。
		v, known, found := reachingLiteralAt(fset, src, body, expr.Pos(), e.Name, params, incoming, incomingSrc, consts)
		if known {
			src := v.src
			if src == "" {
				src = "def@L" + itoaSeq(v.line)
			}
			if paramIndex(params, e.Name) >= 0 {
				src += " → arg@L" + itoaSeq(fset.Position(expr.Pos()).Line)
			}
			return sceneArg{val: v.val, source: src}
		}
		if found {
			// 有定义但无法证明唯一值：不得回退场景键（todo12-3 R2）
			src := v.src
			if src == "" {
				src = "unknown:arg-value"
				if v.line > 0 {
					src += "@L" + itoaSeq(v.line)
				}
			}
			reason := v.reason
			if reason == "" {
				reason = "unknown:arg-value"
			}
			return sceneArg{val: sceneArgUnknown, source: src, reason: reason}
		}
		if idx := paramIndex(params, e.Name); idx >= 0 && idx < len(incoming) {
			src := "arg@" + fileLine("", fset.Position(expr.Pos()).Line)
			if idx < len(incomingSrc) && incomingSrc[idx] != "" {
				src = incomingSrc[idx] + " → " + src
			}
			return sceneArg{val: incoming[idx], source: src}
		}
		return sceneArg{}
	}
	// SelectorExpr（req.Region）/ 调用 / 其他：静态不可判定——**不猜**
	return sceneArg{}
}

// sceneArgUnknown 传播实参的"无法证明唯一值"哨兵：调用点有定义但不是
// 唯一字面量时，内层不得回退到 --scene 键当成已证明（todo12-3 R2）。
const sceneArgUnknown = "\x00unknown"

// litVal 抽象值：已知字符串字面量 / 未知（含声明存在但值不可判定）。

// litScopes 词法作用域栈（形参/局部变量 → 抽象值）。

// assign 更新最近的既有声明；没有声明则落在最内层作用域。

// declare 在当前作用域声明（:= / var 的遮蔽语义）。

// assignMerged 多分支合流：所有分支同值才保留，否则未知（保守，不猜）。

// reachingLiteralAt 调用点之前 name 的可达字面量（同函数体，作用域感知）。
func reachingLiteralAt(fset *token.FileSet, src []byte, body *ast.BlockStmt, target token.Pos,
	name string, params *ast.FieldList, incoming, incomingSrc []string, consts sceneConstLookup) (litVal, bool, bool) {
	s := reachingScopeAt(fset, src, body, target, params, incoming, incomingSrc, consts)
	if s == nil {
		return litVal{}, false, false
	}
	v, ok := s.lookup(name)
	if !ok {
		return litVal{}, false, false
	}
	if s.limited && v.known {
		// todo13-3：深度/扫描上限后无法证明仍是该定义——不静默保留确定值
		v.known = false
		v.reason = "unknown:analysis-depth-limit"
		if v.src == "" && v.line > 0 {
			v.src = "def@L" + itoaSeq(v.line)
		}
	}
	if !v.known {
		if v.reason == "" {
			v.reason = "unknown:arg-value"
		}
		return v, false, true
	}
	return v, true, true
}

// reachingScopeAt 在 target 处的可达作用域（含结构体字段键）。
func reachingScopeAt(fset *token.FileSet, src []byte, body *ast.BlockStmt, target token.Pos,
	params *ast.FieldList, incoming, incomingSrc []string, consts sceneConstLookup) *litScopes {
	if body == nil {
		return nil
	}
	s := newLitScopes(params, incoming, incomingSrc)
	s.consts = consts
	walkReachingStmts(fset, src, body.List, target, s, 0)
	return s
}

// applyReachingStmt target 之前的语句 → 更新抽象环境。

// mergeReachingClauses switch/select 分支合流（同值保留；否则未知）。

// evalLitExpr 表达式 → 抽象值（字符串字面量 / 已知变量 / 取址别名；其余未知）。

// applyCallInvalidation 调用可能通过指针改写变量：实参里的 &x 或取址别名。
// todo13-3：无法证明回调未改写时，旧字面量定义必须失效（不得静默保留）。

// invalidate 将变量置为未知（保留原因与位置——证据用）。

// paramIndex 形参名在签名中的位置（跳过 receiver）。

// lastSymName canonical ID 末段（方法形态 (T).M 取 M 之前的调用名匹配用）。

// sceneGuard 一条分支假设（cond 为真 / 为假）。

// implDef 一个到达调用点的实现定义（带守卫）。

// sceneImplChoice 接口调用的实现选择（todo11-2 / todo12-4）：
//   - 按**调用点的到达定义**收集实现赋值（无条件覆盖、if/else 合并、
//     嵌套 else-if、未初始化路径），守卫用场景判定；
//   - 已知场景能唯一确定 → 选它，reason=scene-guard:<依据>；
//   - 否则保留候选集合（调用方标 ambiguous，不静默把第一个当结论）。
//
// 只分析当前函数体（req.curBody），不扫别的函数。

// evalImplGuards 用场景判定定义守卫：任何守卫 excluded → 定义不可达；
// 全部 taken → 定义必然可达；否则 unknown。

// reachingImplsAt 调用点之前 varName 的到达实现定义（含分支守卫）。

// implState 到达定义集合（赋值替换；分支合并）。

// implTypeFromExpr 赋值右值 → 实现类型名（&T{} / T{} / NewT()）。

// resolveSceneArgFields 调用实参的**结构体字段已知值**（todo22 P1：
// 跨函数传递 req 的来源分派）。支持三种形态：
//   - 直接复合字面量：`Submit(&Req{Source: SourceB})`
//   - 局部变量：`r := &Req{Source: SourceB}` / `r.Source = SourceB`（沿
//     调用点之前的可达定义，含分支合流/取址改写失效）
//   - 当前函数形参：字段值已由上游调用传播到 req.sceneArgs（键 `param.Field`）
//
// 只传播**能证明**的字段值；字段被未知值赋值/被取址调用改写时记
// `sceneArgUnknown`（内层不得回退 --scene 键，与实参值的既有语义一致）。

// sceneArgFromLitVal 抽象值 → 实参字段值（未知保留原因，不回退场景键）。

// paramFieldArgs 结构体字段值 → 被调函数形参映射（键 `param.Field`）。

// applySceneFieldArgs 把（字段值 → 形参映射）合并进当前场景实参表。

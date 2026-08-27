package ssa

import (
	"go/constant"
	"sort"
	"strings"

	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// Q239 动态 SQL 拼接还原（design-q239.md §3.4）：fmt.Sprintf 模板 +
// %s 实参值流追溯（常量 / 嵌套 Sprintf / 跨函数参数）→ 还原 SQL 模板
// → 走统一 parseSQLStmt。深度上限 3；部分还原（可还原的占位符还原，
// 剩余保留——不误报）；追溯不到返回 ""（保持现状不解析）。

// maxSQLResolveDepth %s 实参追溯深度上限（Q2：3 层——go2o rbac 典型
// 「调用方常量 → dao 参数」2 层覆盖）。
const maxSQLResolveDepth = 3

// maxSQLCandidates 候选 SQL 上限（Q252：phi 分支笛卡尔积防爆炸——
// 3 个占位符 × 各 2 分支 = 8 组合已够；再大截断保安全）。
const maxSQLCandidates = 16

// resolveSQLString 解析 SQL 字符串实参（Q239 单值语义保持——取第一
// 候选；多值/分支展开见 resolveSQLCandidates）。
func (ext *fieldExtractor) resolveSQLString(v ssa.Value, depth int) string {
	c := ext.resolveSQLCandidates(v, depth)
	if len(c) > 0 {
		return c[0]
	}
	return ""
}

// resolveSQLCandidates 解析 SQL 字符串实参为候选集（Q252）：
//   - 字符串常量 → 单候选
//   - fmt.Sprintf 调用 → 模板 + %s 实参递归还原（部分还原：不可还原
//     的占位符保留）
//   - 函数参数 → 全部静态调用点实参候选并集
//   - phi（if/else 分支赋值）→ 每分支常量各一候选——「把所有分支的
//     条件都加进去」：还原出每个分支的 SQL，全部候选参与解析，提取
//     并集（walkEdges 的 anchor=source_id/target_id 形态）
//
// 不可还原返回 nil（调用方保持原始 SQL → 解析失败降级启发式）。
func (ext *fieldExtractor) resolveSQLCandidates(v ssa.Value, depth int) []string {
	if depth > maxSQLResolveDepth {
		return nil
	}
	if mi, ok := v.(*ssa.MakeInterface); ok {
		v = mi.X // any 参数包装解包（Call/Parameter 在包装内）
	}
	if c, ok := unwrapConst(v); ok && c.Value != nil && c.Value.Kind() == constant.String {
		return []string{constant.StringVal(c.Value)}
	}
	// Q252：phi 分支展开（if/else 赋值）——每分支候选并集
	if phi, ok := v.(*ssa.Phi); ok {
		var out []string
		seen := map[string]bool{}
		for _, e := range phi.Edges {
			for _, c := range ext.resolveSQLCandidates(e, depth+1) {
				if !seen[c] {
					seen[c] = true
					out = append(out, c)
				}
			}
		}
		return out
	}
	call, ok := v.(*ssa.Call)
	if !ok {
		// 跨函数参数：全部静态调用点实参候选并集（带缓存）
		if p, isParam := v.(*ssa.Parameter); isParam {
			return ext.resolveParamCandidates(p, depth)
		}
		return nil
	}
	fn := call.Call.StaticCallee()
	if fn == nil || fn.Name() != "Sprintf" || len(call.Call.Args) < 1 {
		return nil
	}
	tmpls := ext.resolveSQLCandidates(call.Call.Args[0], depth+1)
	if len(tmpls) == 0 {
		return nil
	}
	// %s 占位符按实参序逐个替换（%d 等数值实参不消耗 %s——字符串实参
	// 与 %s 位置对齐时正确；不齐时部分还原不误报）；变参打包的 Slice
	// 指令展开为元素（fmt.Sprintf(format, a ...any) 的 []any{...}）；
	// 多候选实参 → 笛卡尔积展开（上限 maxSQLCandidates）
	for i := 1; i < len(call.Call.Args); i++ {
		elems := []ssa.Value{call.Call.Args[i]}
		if sl, ok := call.Call.Args[i].(*ssa.Slice); ok {
			if es := sliceElemsOf(sl); len(es) > 0 {
				elems = es
			}
		}
		for _, e := range elems {
			if !tmplsHavePlaceholder(tmpls) {
				break
			}
			cands := ext.resolveSQLCandidates(e, depth+1)
			if len(cands) == 0 {
				continue // 不可还原——%s 保留（部分还原）
			}
			var next []string
			for _, t := range tmpls {
				for _, c := range cands {
					if strings.Contains(t, "%s") {
						next = append(next, strings.Replace(t, "%s", c, 1))
					} else {
						next = append(next, t)
					}
					if len(next) >= maxSQLCandidates {
						break
					}
				}
				if len(next) >= maxSQLCandidates {
					break
				}
			}
			tmpls = next
			if len(tmpls) == 0 {
				return nil
			}
		}
	}
	return tmpls
}

// tmplsHavePlaceholder 候选集里是否还有未还原的 %s。
func tmplsHavePlaceholder(tmpls []string) bool {
	for _, t := range tmpls {
		if strings.Contains(t, "%s") {
			return true
		}
	}
	return false
}

// sliceElemsOf 提取变参打包 slice 的元素（[]any{a, b} 的 Alloc/MakeSlice
// + IndexAddr + Store 序列按索引排序）。非字面量 slice 返回 nil。
func sliceElemsOf(sl *ssa.Slice) []ssa.Value {
	type idxVal struct {
		idx int
		val ssa.Value
	}
	var pairs []idxVal
	if refs := sl.X.Referrers(); refs != nil {
		for _, u := range *refs {
			ia, ok := u.(*ssa.IndexAddr)
			if !ok || ia.X != sl.X {
				continue
			}
			if c, ok := ia.Index.(*ssa.Const); ok && c.Value != nil {
				idx, _ := constant.Int64Val(c.Value)
				for _, u2 := range *ia.Referrers() {
					if st, ok := u2.(*ssa.Store); ok && st.Addr == ia {
						pairs = append(pairs, idxVal{int(idx), st.Val})
					}
				}
			}
		}
	}
	if len(pairs) == 0 {
		return nil
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].idx < pairs[j].idx })
	out := make([]ssa.Value, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, p.val)
	}
	return out
}

// paramCalls 参数所属函数的静态调用点缓存（惰性构建一次）。
type paramCalls struct {
	fn      *ssa.Function
	callers [][]ssa.Value // 每个静态调用点的实参列表
}

// resolveParamAtCalls 函数参数 → 静态调用点实参追溯（多个调用点取首个
// 可还原的；实参索引 = 参数在签名中的位置——方法调用 Args[0]=receiver
// 偏移 1）。
func (ext *fieldExtractor) resolveParamCandidates(p *ssa.Parameter, depth int) []string {
	fn := p.Parent()
	if fn == nil {
		return nil
	}
	paramIdx := 0
	for i, par := range fn.Params {
		if par == p {
			paramIdx = i
			break
		}
	}
	if paramIdx >= len(fn.Params) {
		return nil
	}
	// 惰性扫描 prog 找调用 fn 的静态调用点（每函数一次，缓存）
	key := fn
	pc, ok := ext.paramCallerCache[key]
	if !ok {
		pc = &paramCalls{fn: fn}
		for _, f := range ext.allFunctions() {
			for _, b := range f.Blocks {
				for _, instr := range b.Instrs {
					call, isCall := instr.(*ssa.Call)
					if !isCall || call.Call.StaticCallee() != fn {
						continue
					}
					pc.callers = append(pc.callers, call.Call.Args)
				}
			}
		}
		if ext.paramCallerCache == nil {
			ext.paramCallerCache = map[*ssa.Function]*paramCalls{}
		}
		ext.paramCallerCache[key] = pc
	}
	var out []string
	seen := map[string]bool{}
	for _, args := range pc.callers {
		// 方法调用 Args[0]=receiver；普通函数 Args[0]=首参
		off := 0
		if fn.Signature.Recv() != nil {
			off = 1
		}
		if paramIdx+off >= len(args) {
			continue
		}
		for _, c := range ext.resolveSQLCandidates(args[paramIdx+off], depth+1) {
			if !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
	}
	return out
}

// allFunctions 全部 SSA 函数（prog 缓存）。
func (ext *fieldExtractor) allFunctions() []*ssa.Function {
	if ext.funcCache != nil {
		return ext.funcCache
	}
	var out []*ssa.Function
	for f := range ssautil.AllFunctions(ext.prog) {
		out = append(out, f)
	}
	ext.funcCache = out
	return out
}

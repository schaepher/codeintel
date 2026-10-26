package action

// todo11-4：把 `query effects` 的函数级汇总细化为**路径级**摘要。
// todo12-1/7：目标筛选（字段/返回槽）、路径预算与完整性诊断。
//
// 用户确认的 Q6：
//   - 输出 `paths[]`：`{id, conditions, writes, remote_writes, tx, result,
//     reachable, notes}`；互斥分支各自成路径，`return` 之后的写点不进该路径
//   - 场景约束按**路径可达性**生效（被场景排除的分支 → 该路径 reachable=false
//     + 依据），不再按"场景键与字段名匹配"过滤写入
//   - 函数级汇总保留（db_writes / remote_writes / tx / result）作为兼容视图
//
// 路径枚举口径：AST 语句顺序 + 分支两侧；循环体按"0..N 次"处理（区间覆盖 +
// notes 标注，不假装只执行一次）；defer 标注"函数退出时执行"。
// todo12-7：预算/嵌套/循环/switch 截断一律进诊断（analysis.status=partial），
// 不再静默丢弃待展开路径。

import (
	"go/ast"
	"go/token"
)

// EffectPath 一条执行路径的副作用与返回。
type EffectPath struct {
	ID         string         `json:"id"` // p1, p2, ...
	Conditions []string       `json:"conditions,omitempty"`
	Writes     []DBEffect     `json:"writes"`
	Remote     []RemoteEffect `json:"remote_writes"`
	Calls      []EffectCall   `json:"calls,omitempty"` // todo12-1：路径上的调用（被调写入/依赖依据）
	Tx         *TxScope       `json:"tx,omitempty"`
	Result     *ResultEffect  `json:"result,omitempty"`
	Reachable  bool           `json:"reachable"`
	Notes      []string       `json:"notes,omitempty"`
}

// EffectCall 路径上的一次调用（todo12-1 R3/A6：保留影响目标的调用）。
type EffectCall struct {
	Function string `json:"function,omitempty"` // 被调符号（canonical ID）
	File     string `json:"file,omitempty"`     // 被调定义文件（仓库相对）
	Line     int    `json:"line,omitempty"`     // 调用点行号
	Target   bool   `json:"target,omitempty"`   // 该调用命中目标依赖（写目标字段等）
	Reason   string `json:"reason,omitempty"`
}

// dbWriteSite 一个字段写入点（源码观察；与 SSA 摘要行号可能不同——
// 摘要按 (function,access,field) 唯一，多行写会坍缩成一条）。
type dbWriteSite struct {
	field string
	line  int
	rhs   []ast.Expr // 右值表达式（依赖分析用；可能为空）
}

// localAssign 局部变量赋值（dependency 分析用——不直接输出为字段写入）。
type localAssign struct {
	name string
	line int
	rhs  []ast.Expr
}

// effectCallSite 路径上的调用表达式（映射被调符号在 build 阶段完成）。
type effectCallSite struct {
	line  int
	name  string
	args  []ast.Expr
	rhss  []ast.Expr // 赋值右侧的调用（结果赋值给局部/字段）
	assig string     // 结果接收者（局部名/字段文本）
}

// pathSpec 枚举期间的一条路径（区间 + 条件 + 返回）。
type pathSpec struct {
	conditions  []string
	writes      []dbWriteSite // AST 观察到的字段写入（路径归属用）
	locals      []localAssign // 局部变量赋值（依赖分析用）
	calls       []effectCallSite
	intervals   [][2]int
	returnLine  int
	returnVals  []string
	returnExprs []ast.Expr
	returnNil   bool
	notes       []string
	reachable   bool
}

// pathWalkStats 路径枚举的完整性统计（todo12-7）。
type pathWalkStats struct {
	dropped      int // 预算截断丢弃的终态/待续路径数
	nestTrunc    int // 嵌套过深未展开的路径数
	loops        int // 未逐次展开的循环数
	switches     int // 未细化的 switch/select 数
	unknownCalls int // 无法映射到索引符号的调用点（保守保留）
	// todo13-7：预算诊断的精确口径（已用/上限/停止位置/未展开范围/前沿）。
	budgetLimit  int
	budgetUsed   int
	stopFile     string
	stopLine     int
	stopScope    string
	frontier     int  // 未展开前沿数（不等于已证明的遗漏终态数）
	extraUnknown bool // 由前沿可产生的额外终态总数未知
}

// maxEffectPaths 默认路径数上限（分支爆炸保护；--path-budget 可调）。
const maxEffectPaths = 24

// effectPaths 枚举函数体的执行路径（分支两侧各一条；return 终止当前路径）。
//
// 实现用"待续路径"模型：每步对每条待续路径展开，`return` 直接落为终态，
// 语句列表走完仍待续的路径在顶层收尾——这样"if 后还有语句"不会被误判成
// 路径结束（曾因此产出重复/空路径）。
func (a *Actions) effectPaths(fset *token.FileSet, src []byte, body *ast.BlockStmt,
	scene Scene, args map[string]string, budget int, file string) ([]pathSpec, *pathWalkStats) {
	if budget <= 0 {
		budget = maxEffectPaths
	}
	st := &pathWalkStats{budgetLimit: budget, stopFile: file}
	base := pathSpec{reachable: true}
	out := []pathSpec{}
	pending := walkEffectPaths(fset, src, body.List, []pathSpec{base}, &out, scene, args, 0, st, budget)
	for _, p := range pending {
		if len(out) >= budget {
			st.dropped++
			continue
		}
		p.notes = append(p.notes, "路径自然结束（无显式 return）")
		out = append(out, p)
	}
	if len(out) == 0 {
		base.notes = append(base.notes, "路径自然结束（无显式 return）")
		out = append(out, base)
	}
	return out, st
}

// walkEffectPaths 语句列表 → (终态路径写入 out, 返回待续路径)。

// markPathBudgetStop 记录首次预算截断的位置/范围/前沿（todo13-7）。
// 只记第一次：后续截断由同一预算上限导致，位置不再有意义。

// applyPathInit if/switch 的初始化语句（err := Validate(...)）——两个分支
// 都共享这次赋值（todo12-8：错误来源与路径写入归属需要它）。

// collectCallSites 语句内的调用表达式（直接调用；嵌套调用也记录，build 阶段
// 按行号映射索引边）。

// sceneReason 场景判定的依据文本（路径 notes 用）。
func sceneReason(f *SceneFit) string {
	if f == nil {
		return "unknown"
	}
	if f.Reason != "" {
		return f.Reason
	}
	return f.Status
}

// pathCovers 行是否落在路径区间内（区间为闭区间）。
func pathCovers(p pathSpec, line int) bool {
	if line <= 0 {
		return false
	}
	for _, iv := range p.intervals {
		if line >= iv[0] && line <= iv[1] {
			return true
		}
	}
	return false
}

// buildParams 路径构建上下文（路径归属 + 目标筛选）。

// buildEffectPaths 把函数级汇总按路径区间归属到各路径（todo11-4/12-1/12-7/12-8）。

// pathCalls 路径上的调用：直接调用（从 statements 收集）+ 被调字段写入归属。

// effectErrorSources 每条返回槽的错误来源（todo12-8 R3）：
//   - returned：返回当前错误变量（含显式检查后返回）
//   - wrapped：返回表达式包裹了错误值
//   - swallowed：错误变量非 nil 后被 nil 赋值/最终返回 nil
//   - overwritten：返回的不是最近的错误值（被后续赋值覆盖）
//   - stale：返回的名字不是最近的错误来源
//   - nil / unknown：无错误或无法判定

// slotIndex "#1" → 1（非槽形态返回 false）。

// effectErrorSource 单槽判定（沿路径的局部错误变量赋值序列）。

// errorVarKnownNil 路径条件是否已证明错误变量为 nil。

// regexpContainsWord 标识符是否出现在表达式文本里（词边界近似）。

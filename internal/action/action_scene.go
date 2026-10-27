package action

// todo10-5：场景条件（`--scene k=v`）——静态可判定的键值约束。
//
// 作用域与判据（用户确认的 Q1/Q2）：
//   - 只做**静态可判定**匹配：条件里的字面量与场景值比对（不做值流传播的
//     完整求解）
//   - 一层传播：调用点实参是字面量、且被调函数的形参参与 `if` 条件时，
//     该条件按实参字面量判定（不跨多层；跨多层记 unverified:condition-depth）
//   - 未匹配/未知一律保留候选，只把判断结果作为依据（verdict/stop 语义与
//     todo10-3/8 一致）——静态推断不是线上事实

import (
	"go/ast"
	"strings"
)

// Scene 场景条件（`--scene region=CN,platform=ios`）。键名做大小写不敏感匹配。
type Scene struct {
	Values map[string]string
}

// 场景对单个分支/条件的判定结果。
const (
	SceneTaken    = "taken"    // 场景命中"条件为真"的分支
	SceneExcluded = "excluded" // 场景命中"条件为假"的分支（分支内容被剔除）
	SceneUnknown  = "unknown"  // 无法静态判定（保留候选）
)

// SceneFit 一次场景判定的依据（挂在节点上，渲染层据此标注）。
type SceneFit struct {
	Status   string `json:"status"`             // taken|excluded|unknown
	Key      string `json:"key,omitempty"`      // 命中的场景键（region）
	Field    string `json:"field,omitempty"`    // 条件里的字段/形参（req.Region）
	Literal  string `json:"literal,omitempty"`  // 条件里的字面量（"CN"）
	Expected string `json:"expected,omitempty"` // 场景给出的值（CN）
	Reason   string `json:"reason,omitempty"`   // scene:<k=v> / scene-arg:<param>=<lit> / unknown:*
	Source   string `json:"source,omitempty"`   // todo12-3：值来源（定义位置/传播依据）
	// todo13-3：unknown 的具体成因（如 call-may-write:rewrite@L18）——
	// 值在到达该分支前已被未知调用改写，不能沿用旧字面量。
	Cause string `json:"cause,omitempty"`
}

// NewScene 解析 `--scene` 参数：每项为 `k=v`（也接受逗号分隔的多个）。
func NewScene(pairs []string) Scene {
	sc := Scene{Values: map[string]string{}}
	for _, p := range pairs {
		for _, part := range strings.Split(p, ",") {
			k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
			if !ok {
				continue
			}
			k, v = strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
			if k != "" && v != "" {
				sc.Values[k] = v
			}
		}
	}
	return sc
}

// Empty 是否未指定场景。
func (s Scene) Empty() bool { return len(s.Values) == 0 }

// sceneSummary meta 汇总（取用/剔除/未知各多少分支）。
type sceneSummary struct {
	Applied  bool `json:"applied"`
	Taken    int  `json:"taken"`
	Excluded int  `json:"excluded"`
	Unknown  int  `json:"unknown"`
	// todo13-3：场景判定不完整（值被未知调用改写/分析深度上限）——
	// unknown 不等于“已证明两分支都可达”，需要显式 partial + 原因。
	Partial bool     `json:"partial,omitempty"`
	Causes  []string `json:"causes,omitempty"`
}

// matchCond 判定条件表达式与场景的关系。args 为"形参名 → 实参字面量"
// （一层传播；nil 表示没传）。只识别 `field == "lit"` / `field != "lit"`
// 与 `"lit" == field` 形态（必要时剥离一层括号/取反 `!`）。
func (s Scene) matchCond(cond ast.Expr, args map[string]string) SceneFit {
	return s.matchCondSrc(cond, args, nil, nil)
}

// matchCondSrc 同 matchCond，另带形参值来源（SceneFit.Source，证据用）与
// 包内常量表（todo22 §5：整型/有类型常量与内建转换参与判定）。
// 布尔短路与取反按 Go 语义处理：`a && b` 任一为假即整条为假（可排除分支），
// 全部为真才 taken；`||` 反之；`!` 翻转 taken/excluded。
func (s Scene) matchCondSrc(cond ast.Expr, args, srcs, consts map[string]string) SceneFit {
	negated := false
	for {
		switch e := cond.(type) {
		case *ast.ParenExpr:
			cond = e.X
			continue
		case *ast.UnaryExpr:
			if e.Op.String() == "!" {
				negated = !negated
				cond = e.X
				continue
			}
		}
		break
	}
	if bin, ok := cond.(*ast.BinaryExpr); ok && (bin.Op.String() == "&&" || bin.Op.String() == "||") {
		fit := s.matchBoolOp(bin, args, srcs, consts)
		return sceneNegateFit(fit, negated)
	}
	fit := s.matchCompareCond(cond, args, srcs, consts)
	return sceneNegateFit(fit, negated)
}

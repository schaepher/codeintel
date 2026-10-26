package action

import (
	"encoding/json"
	"go/ast"
	"time"

	"github.com/schaepher/codeintel/internal/domain"
)

// SeqTiming 代码级时序的阶段耗时与展开规模（todo10-9）。诊断用途——
// 只进日志 / 根节点 meta / stderr，不改变 stdout 的既有 JSON 键。
type SeqTiming struct {
	ResolveMS float64 `json:"resolve_ms"` // 符号解析（含接口方法具体化回退）
	ReadMS    float64 `json:"read_ms"`    // 源码读取 + 语法解析（含递归展开的被调函数）
	WalkMS    float64 `json:"walk_ms"`    // AST 遍历 / 调用点定位 / 接口与字段具体化
	RenderMS  float64 `json:"render_ms"`  // 渲染（cli 填；JSON 输出记 0——编码即输出，自指）
	TotalMS   float64 `json:"total_ms"`   // 端到端（含渲染）
	Nodes     int     `json:"nodes"`      // 展开规模（节点总数）
	Depth     int     `json:"depth"`      // 请求的嵌套层级
	Stop      string  `json:"stop"`       // 终止原因：none | depth | canceled
}

// SeqMeta 根节点元信息（只有根 CodeSeqNode 携带）。
type SeqMeta struct {
	Version     int            `json:"seq_version"`
	Target      string         `json:"target,omitempty"`
	Timing      *SeqTiming     `json:"timing,omitempty"`
	Scene       *sceneSummary  `json:"scene,omitempty"`       // todo10-5：场景裁剪统计
	Focus       *SeqFocusMeta  `json:"focus,omitempty"`       // todo10-2 part3：焦点过滤是否生效
	Diagnostics []SeqDiagCount `json:"diagnostics,omitempty"` // todo10-3：展开终点聚合（按 code）
	// todo12-13：索引覆盖/降级（与展开诊断分开——只有 success 不算覆盖完整）
	Coverage *SeqCoverage `json:"coverage,omitempty"`
}

// SeqCoverage 构建期覆盖/降级信息（查询携带；与 SQL 解析统计分开）。
type SeqCoverage struct {
	BuildStatus  string         `json:"build_status,omitempty"`
	BuildError   string         `json:"build_error,omitempty"`
	IndexSHA     string         `json:"index_sha,omitempty"`
	DegradeStats map[string]any `json:"degrade_stats,omitempty"`
	// todo13-13：具体失败包/错误位置（计数之外的覆盖范围）
	ErrorPackages []SeqPkgError `json:"error_packages,omitempty"`
}

// SeqPkgError 一个构建期失败包（路径 + 最多 3 条错误位置/文本）。
type SeqPkgError struct {
	Path   string   `json:"path"`
	Errors []string `json:"errors,omitempty"`
}

// SeqFocusMeta 焦点过滤结果（matched=0 时 applied=false——输出保留全量，
// 调用方据此提示"未命中"而不是把空图当结论）。
type SeqFocusMeta struct {
	Targets []string `json:"targets"`
	Matched int      `json:"matched"`
	Applied bool     `json:"applied"`
	// todo12-9 R2：依赖里有类型无法判定的写入（保守保留，但标 partial）
	Partial bool `json:"partial,omitempty"`
}

// seqTimingAcc 展开期间的累加器（req 按值传递——用指针共享）。
type seqTimingAcc struct {
	read            time.Duration // 读源码 + 语法解析（递归展开累加，互不重叠）
	depth           bool          // 有可展开而未展开的调用点（depth 截断）
	cancel          bool
	unresolved      int // 未解析到项目内符号的调用点数（诊断用）
	focusUnresolved int // todo12-9：焦点依赖里类型无法判定的写入数
}

// millis Duration → 毫秒（保留小数——单次阶段可能远小于 1ms）。
func millis(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// coverageNum degrade_stats 数值键提取（JSON 反序列化为 float64）。
func coverageNum(stats map[string]any, key string) int64 {
	if stats == nil {
		return 0
	}
	switch v := stats[key].(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	}
	return 0
}

// seqCoverage 构建期覆盖信息（build_metadata → meta.coverage）。
func seqCoverage(m *domain.BuildMeta) *SeqCoverage {
	if m == nil {
		return nil
	}
	cov := &SeqCoverage{BuildStatus: string(m.Status), BuildError: m.ErrorMsg, IndexSHA: m.CommitSHA}
	if m.DegradeStats != "" {
		switch m.DegradeStats[0] {
		case '{', '[':
			if err := json.Unmarshal([]byte(m.DegradeStats), &cov.DegradeStats); err != nil {
				cov.DegradeStats = map[string]any{"raw": m.DegradeStats}
			}
		default:
			cov.DegradeStats = map[string]any{"raw": m.DegradeStats}
		}
	}
	// todo13-13：失败包明细（degrade_stats.error_packages → 结构化）
	if raw, ok := cov.DegradeStats["error_packages"]; ok {
		if arr, ok := raw.([]any); ok {
			for _, x := range arr {
				m, ok := x.(map[string]any)
				if !ok {
					continue
				}
				pe := SeqPkgError{}
				if p, _ := m["path"].(string); p != "" {
					pe.Path = p
				}
				if es, ok := m["errors"].([]any); ok {
					for _, e := range es {
						if s, ok := e.(string); ok && s != "" {
							pe.Errors = append(pe.Errors, s)
						}
					}
				}
				if pe.Path != "" {
					cov.ErrorPackages = append(cov.ErrorPackages, pe)
				}
			}
		}
	}
	return cov
}

// countSeqNodes 展开规模（节点总数——含分支/else/循环/嵌套）。
func countSeqNodes(n *CodeSeqNode) int {
	if n == nil {
		return 0
	}
	total := 1
	for _, c := range n.Nodes {
		total += countSeqNodes(c)
	}
	for _, c := range n.Else {
		total += countSeqNodes(c)
	}
	return total
}

// SeqFilter 时序图导出过滤（S5）：按包/文件/函数/正则——命中不生成
// 节点（如 log 调用）。cli 从 config.yaml 的 seq.filter_* 读取。
type SeqFilter struct {
	Pkgs  []string // 包路径（完整或短名/末段匹配）
	Files []string // 源码文件（仓库相对路径，末段匹配）
	Fns   []string // 调用名（label 精确或末段 .Method 匹配）
	Regex []string // label 正则
}

// CodeSeqNode 代码级时序节点（树——分支/循环/嵌套调用）。
type CodeSeqNode struct {
	Kind     string         // call | branch | loop
	Label    string         // call: 调用名（obj.Method/fn）；branch: 条件；loop: 循环条件
	Actor    string         // R83：call 参与者（调用对象——s.manager/t.repo/ic；函数为函数名）
	Type     string         // R83：参与者短类型名（包最后路径段.类型名——索引实现类型）
	DeclType string         // P0-5：receiver 字段声明类型（接口——"声明接口"）
	ImplType string         // P0-5：数据流实现类型（R97-2 具体化命中时——声明接口 → 数据流实现双行）
	Args     []string       // R83：参数类型短名（消息线内容）
	Returns  []string       // R83：返回类型短名（return 线内容）
	Line     int            // 源码行号
	Nodes    []*CodeSeqNode // 子节点（branch: then 分支；loop: 循环体；call: 嵌套展开的被调函数调用）
	Else     []*CodeSeqNode // branch: else 分支（可选）
	Meta     *SeqMeta       `json:"meta,omitempty"` // 仅根节点：版本/目标/阶段耗时（todo10-9）
	// todo10-2：执行语义与返回语义
	Exec       string   `json:"exec,omitempty"`       // call: sync（缺省）|defer（退出时执行）|async（并发启动，完成顺序未知）
	InReturn   bool     `json:"in_return,omitempty"`  // call: 位于 return 表达式中（求值发生在返回前）
	Values     []string `json:"values,omitempty"`     // return: 返回表达式文本（按书写顺序）
	Slots      []string `json:"slots,omitempty"`      // return: 对应结果槽（具名结果名 / #序号）
	Terminates bool     `json:"terminates,omitempty"` // return: 终止当前路径（其后的语句不属于该路径）
	Focus      bool     `json:"focus,omitempty"`      // todo10-2：命中 --focus 目标（字段写入点/写者调用/返回）
	// todo10-3：展开终点诊断 + 节点证据（canonical ID / 文件 / 行 / 依据）
	Evidence *SeqEvidence `json:"evidence,omitempty"`
	Stop     *SeqStop     `json:"stop,omitempty"`
	// todo10-5：场景条件对分支的判定（taken/excluded/unknown + 依据）
	Scene *SceneFit `json:"scene,omitempty"`
}

// exprTypeName 类型表达式 → 短名（Ident → 名；SelectorExpr pkg.T → T）
// ——P0-5 receiver 字段声明类型提取。
func exprTypeName(t ast.Expr) string {
	if id, ok := t.(*ast.Ident); ok {
		return id.Name
	}
	if sel, ok := t.(*ast.SelectorExpr); ok {
		return sel.Sel.Name
	}
	return ""
}

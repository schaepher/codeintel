package cli

import (
	"fmt"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
)

// Q235-10 value-trace 文本渲染：分组（写入值/对象/来源/去向）+ 源码
// 片段 + 短锚点——用户一眼看懂数据流动。分组按锚点行等号左右：
//   - 写入值：等号右边变量（brands——值写入字段）
//   - 对象：等号左边路径基址（u——字段所属对象）
//   - 来源：更深层的值产生处（递归子层）
//   - 去向：dir=1 的使用链
//
// 源码片段从节点 FilePath + Line 读取（相对仓库根）；读不到显示
// （无源码）。

// vtGroup 来源侧分组标签。
type vtGroup string

const (
	vtWriteValue vtGroup = "写入值"
	vtObject     vtGroup = "对象"
	vtSource     vtGroup = "来源"
	vtUsage      vtGroup = "去向"
)







// vtNode 渲染用节点（分组后的扁平列表：来源树按 depth 排序 + 去向）。
type vtNode struct {
	row   *domain.TraceRow
	group vtGroup
}

// vtRender 渲染 value-trace 行（去掉锚点自身后的来源/去向树）。
// 返回四种格式的统一中间结构 + 格式选择输出。
func vtRender(rows []*domain.TraceRow, repoDir, format string) string {
	if len(rows) == 0 {
		return "无数据流（节点不存在或无数据流边）"
	}
	anchor := rows[0]
	anchorLine := newSourceLineCache(repoDir).line(anchor.FilePath, anchor.Line)
	leftBase, rightVar := splitAssign(anchorLine)

	var sources, usages []*domain.TraceRow
	for _, r := range rows {
		if r.Depth == 0 {
			continue
		}
		if r.Dir == 0 {
			sources = append(sources, r)
		} else {
			usages = append(usages, r)
		}
	}
	sortTraceRows(sources)
	sortTraceRows(usages)

	anchorLabel := anchor.Name
	if anchor.Kind == domain.KindFieldAccess {
		if anchor.Access == "read" {
			anchorLabel += " [读]"
		} else {
			anchorLabel += " [写]"
		}
	}
	head := fmt.Sprintf("值流: %s:%d   %s", anchorLabel, anchor.Line, anchorLine)

	switch format {
	case "tree":
		return renderTree(head, sources, usages, anchor, leftBase, rightVar, repoDir)
	case "mermaid":
		return renderMermaid(head, anchor, sources, usages, leftBase, rightVar)
	default:
		return renderText(head, sources, usages, leftBase, rightVar, repoDir)
	}
}

// sortTraceRows 来源按 depth 升序、去向按 depth 升序（ID 稳定）。
func sortTraceRows(rs []*domain.TraceRow) {
	for i := 1; i < len(rs); i++ {
		for j := i; j > 0; j-- {
			if rs[j].Depth < rs[j-1].Depth ||
				(rs[j].Depth == rs[j-1].Depth && string(rs[j].ID) < string(rs[j-1].ID)) {
				rs[j], rs[j-1] = rs[j-1], rs[j]
			} else {
				break
			}
		}
	}
}

// renderText 文本格式（缩进分组）。
// Q235-12 来源树：depth=1 写入值/对象为顶层组；depth>=2 按 ParentID
// 归入对应顶层组的子来源层；方法调用赋值（u := svc.GetOrm()）的
// 接收者 svc 补充进子来源。
func renderText(head string, sources, usages []*domain.TraceRow,
	leftBase, rightVar, repoDir string) string {
	cache := newSourceLineCache(repoDir)
	var sb strings.Builder
	sb.WriteString(head + "\n")
	// 顶层组（depth=1）：写入值/对象；子来源（depth>=2 + receiver）按 parent 归组
	top := []*domain.TraceRow{}
	child := map[string][]string{} // parentID → 子来源行文本
	// 先收集 depth=1（顶层）
	for _, r := range sources {
		if r.Depth == 1 {
			top = append(top, r)
		}
	}
	// parent key：id|dir（与 mermaid 一致）
	pkey := func(r *domain.TraceRow) string { return string(r.ID) + "|" + fmt.Sprint(r.Dir) }
	// depth>=2 归组：parent 若是 depth=1 顶层 → 其子组；否则归到最近顶层
	childLines := func(r *domain.TraceRow) string {
		return fmt.Sprintf("%s:%d   %s", r.Name, r.Line, cache.line(r.FilePath, r.Line))
	}
	_ = childLines
	for _, r := range sources {
		if r.Depth <= 1 {
			continue
		}
		// 顶层节点 id（不含 dir）→ 找对应的顶层
		topKey := string(r.ParentID)
		belong := ""
		for _, t := range top {
			if string(t.ID) == topKey {
				belong = pkey(t)
				break
			}
		}
		if belong == "" {
			belong = "W"
		}
		child[belong] = append(child[belong], childLines(r))
	}
	// 渲染顶层组
	_ = pkey
	for _, t := range top {
		g := classifySource(t, leftBase, rightVar)
		sb.WriteString("  " + string(g) + " ←\n")
		sb.WriteString(fmt.Sprintf("    %s:%d   %s\n", t.Name, t.Line, cache.line(t.FilePath, t.Line)))
		// 子来源层：receiver + depth>=2 按 parent
		sub := []string{}
		if recv, defLine, defSrc := receiverSource(cache, t.FilePath, t.Line); recv != "" && defSrc != "" {
			sub = append(sub, fmt.Sprintf("%s:%d   %s", recv, defLine, defSrc))
		}
		sub = append(sub, child[pkey(t)]...)
		if len(sub) > 0 {
			sb.WriteString("      来源 ←\n")
			for _, line := range sub {
				sb.WriteString("        " + line + "\n")
			}
		}
	}
	if len(usages) > 0 {
		sb.WriteString("  去向 →\n")
		for _, r := range usages {
			sb.WriteString(fmt.Sprintf("    %s:%d   %s\n", r.Name, r.Line,
				cache.line(r.FilePath, r.Line)))
		}
	}
	return sb.String()
}

// renderTree ASCII 树形（├─/└─/│）。
func renderTree(head string, sources, usages []*domain.TraceRow, anchor *domain.TraceRow,
	leftBase, rightVar, repoDir string) string {
	cache := newSourceLineCache(repoDir)
	var sb strings.Builder
	sb.WriteString(head + "\n")
	// 来源组：写入值/对象为顶层分支，来源为子分支
	groups := []vtGroup{}
	var groupRows = map[vtGroup][]*domain.TraceRow{}
	for _, r := range sources {
		g := classifySource(r, leftBase, rightVar)
		if groupRows[g] == nil {
			groups = append(groups, g)
		}
		groupRows[g] = append(groupRows[g], r)
	}
	for gi, g := range groups {
		lastG := gi == len(groups)-1 && len(usages) == 0
		branch := "├─"
		if lastG {
			branch = "└─"
		}
		sb.WriteString(branch + " " + string(g) + "\n")
		rs := groupRows[g]
		childPrefix := "│  "
		if lastG {
			childPrefix = "   "
		}
		for _, r := range rs {
			sb.WriteString(childPrefix + "└─ " + fmt.Sprintf("%s:%d   %s\n", r.Name, r.Line,
				cache.line(r.FilePath, r.Line)))
		}
	}
	if len(usages) > 0 {
		sb.WriteString("└─ 去向\n")
		for _, r := range usages {
			sb.WriteString("   └─ " + fmt.Sprintf("%s:%d   %s\n", r.Name, r.Line,
				cache.line(r.FilePath, r.Line)))
		}
	}
	return sb.String()
}

// renderMermaid flowchart（LR 方向；节点用序号防特殊字符）。
// Q235-11 父子链：depth=1 连锚点 W；depth>1 连 ParentID 对应节点
// （GetBrands 构造 → brands → 锚点写——连线体现真实数据流，非星形）。
func renderMermaid(head string, anchor *domain.TraceRow, sources, usages []*domain.TraceRow,
	leftBase, rightVar string) string {
	var sb strings.Builder
	sb.WriteString("flowchart LR\n")
	anchorLabel := anchor.Name
	if anchor.Kind == domain.KindFieldAccess {
		if anchor.Access == "read" {
			anchorLabel += " [读]"
		} else {
			anchorLabel += " [写]"
		}
	}
	sb.WriteString(fmt.Sprintf("  W[\"%s:%d\"]\n", anchorLabel, anchor.Line))
	// 节点编号：id → 序号（来源 S、去向 U）
	num := map[string]string{string(anchor.ID): "W"}
	idx := 1
	writeNode := func(r *domain.TraceRow, prefix string) string {
		label := fmt.Sprintf("%s:%d", r.Name, r.Line)
		if r.Dir == 0 && r.Depth == 1 {
			if g := classifySource(r, leftBase, rightVar); g == vtWriteValue || g == vtObject {
				label = string(g) + " " + label
			}
		}
		if r.FuncID != anchor.FuncID {
			if fn := shortFuncName(r.FuncID); fn != "" {
				label += " (" + fn + ")"
			}
		}
		key := string(r.ID) + "|" + fmt.Sprint(r.Dir)
		if n, ok := num[key]; ok {
			return n
		}
		n := fmt.Sprintf("%s%d", prefix, idx)
		idx++
		num[key] = n
		sb.WriteString(fmt.Sprintf("  %s[\"%s\"]\n", n, label))
		return n
	}
	// 连线（先声明节点，再连线——保证 parent 已编号）
	edge := func(child, parent string) {
		sb.WriteString(fmt.Sprintf("  %s --- %s\n", child, parent))
	}
	for _, r := range sources {
		g := classifySource(r, leftBase, rightVar)
		n := writeNode(r, "S")
		// 分组名作为节点标签的一部分（depth=1 的写入值/对象）
		if r.Depth == 1 && (g == vtWriteValue || g == vtObject) {
			// 标签已有 name——分组名通过节点名区分
		}
		p := num[string(r.ParentID)+"|0"]
		if r.Depth <= 1 || p == "" {
			p = "W"
		}
		edge(n, p)
	}
	for _, r := range usages {
		n := writeNode(r, "U")
		p := num[string(r.ParentID)+"|1"]
		if r.Depth <= 1 || p == "" {
			p = "W"
		}
		edge(n, p)
	}
	return sb.String()
}

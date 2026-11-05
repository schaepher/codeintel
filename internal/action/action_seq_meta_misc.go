package action

// 展开规模与导出过滤（从 action_seq_meta.go 拆出）。

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

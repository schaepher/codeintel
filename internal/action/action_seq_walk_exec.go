package action

// T09b：defer/go 调度体内子调用继承执行时机。

// seqPropagateExec 调度体（defer/go）内实际执行的调用继承执行时机标签：
// 内部 Cleanup/Save 等调用不能被渲染成同步执行（T09b：Cleanup 标 defer）。
func seqPropagateExec(n *CodeSeqNode, exec string) {
	if n == nil || exec == "" {
		return
	}
	if n.Exec == "" {
		n.Exec = exec
	}
	for _, c := range n.Nodes {
		seqPropagateExec(c, exec)
	}
	for _, c := range n.Else {
		seqPropagateExec(c, exec)
	}
}

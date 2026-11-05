package action

// go/defer 调度体的调用节点构造（从 action_seq_walk.go 拆出）。

import (
	"go/ast"
	"go/token"
)

// newScheduledCallNode go/defer 调度体专用调用节点构造（scheduled=true）。
func newScheduledCallNode(a *Actions, req CodeSequenceRequest, fset *token.FileSet, src []byte,
	tgts *seqTargets, depth int, recvType string, recvDecl map[string]string, f *ast.File,
	curPkg, curFile string, maskFields bool) func(call *ast.CallExpr) *CodeSeqNode {
	sched := req
	sched.scheduled = true
	sched.schedFieldMask = maskFields
	return newSeqCallNode(a, sched, fset, src, tgts, depth, recvType, recvDecl, f, curPkg, curFile)
}

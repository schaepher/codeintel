package action

// U04bU：资源改写事件的可达边界（终止分支判定）。
//
// 从 action_seq_workflow_resource_builder.go 拆出（行数治理）。

import "go/ast"

// seqBlockTerminates 块是否必然以 return 结束（任一路径执行到 return 即终止）。
func seqBlockTerminates(body *ast.BlockStmt) bool {
	if body == nil {
		return false
	}
	for _, st := range body.List {
		if seqStmtTerminates(st) {
			return true
		}
	}
	return false
}

// seqStmtTerminates 语句是否必然终止（return；if 需两分支都终止）。
func seqStmtTerminates(st ast.Stmt) bool {
	switch s := st.(type) {
	case *ast.ReturnStmt:
		return true
	case *ast.BlockStmt:
		return seqBlockTerminates(s)
	case *ast.IfStmt:
		switch seqStaticCond(s.Cond) {
		case 1:
			return seqBlockTerminates(s.Body)
		case -1:
			return s.Else != nil && seqStmtTerminates(s.Else)
		default:
			return s.Else != nil && seqBlockTerminates(s.Body) && seqStmtTerminates(s.Else)
		}
	}
	return false
}

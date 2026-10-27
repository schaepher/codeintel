package action

// todo22 §2.2/§2.3：if/else 的值环境处理（从 walkStmt 拆出——行数治理）。
//
// 两个分支各走一份环境副本（同名变量在不同分支的赋值互不覆盖），事后合并：
// 值相同 → 单值；不同 → 多候选（未消歧，不把最后一条分支当结论）。没有
// else 且条件未知时，"条件为假、什么都不做"也是一条路径（值为分支前的值）；
// 场景明确 taken 时不补这条路径。

import (
	"go/ast"
	"go/token"
)

// fillSeqIfBranches 填充 if 节点 then/else 子步骤并合并值环境；返回是否
// 走了 else 分支。
func fillSeqIfBranches(a *Actions, req CodeSequenceRequest, fset *token.FileSet, src []byte,
	s *ast.IfStmt, fit *SceneFit, node *CodeSeqNode, tgts *seqTargets, depth int,
	recvType string, recvDecl map[string]string, f *ast.File, curPkg, curFile string) bool {
	var branches []*seqEnv
	condText := seqExprText(fset, src, s.Cond)
	if fit.Status != SceneExcluded {
		thenReq := req
		thenReq.branchCond = condText
		if req.env != nil {
			thenReq.env = req.env.clone()
		}
		node.Nodes = walkStmts(a, thenReq, fset, src, s.Body.List, tgts, depth,
			recvType, recvDecl, f, curPkg, curFile)
		if thenReq.env != nil {
			branches = append(branches, thenReq.env)
		}
	}
	sawElse := false
	switch e := s.Else.(type) {
	case *ast.BlockStmt:
		if fit.Status != SceneTaken {
			sawElse = true
			elseReq := req
			elseReq.branchCond = "!(" + condText + ")"
			if req.env != nil {
				elseReq.env = req.env.clone()
			}
			node.Else = walkStmts(a, elseReq, fset, src, e.List, tgts, depth,
				recvType, recvDecl, f, curPkg, curFile)
			if elseReq.env != nil {
				branches = append(branches, elseReq.env)
			}
		}
	case *ast.IfStmt:
		if fit.Status != SceneTaken {
			sawElse = true
			elseReq := req
			elseReq.branchCond = "!(" + condText + ")"
			if req.env != nil {
				elseReq.env = req.env.clone()
			}
			node.Else = walkStmt(a, elseReq, fset, src, e, tgts, depth,
				recvType, recvDecl, f, curPkg, curFile)
			if elseReq.env != nil {
				branches = append(branches, elseReq.env)
			}
		}
	}
	if req.env == nil || len(branches) == 0 {
		return sawElse
	}
	if !sawElse && fit.Status != SceneTaken {
		// 条件未知且没有 else：另一条路径是"什么都不做"，值为分支前的值。
		branches = append(branches, req.env.clone())
	}
	req.env.mergeBranches(branches)
	return sawElse
}

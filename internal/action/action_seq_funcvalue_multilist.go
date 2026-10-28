package action

// todo25 §2.4：条件分支下的函数列表（append/覆盖）range 展开。
//
// 语义：分支合并后得到"多候选列表"时，**所有候选共有的前缀**按确定执行；
// 各候选的差异尾部作为互斥分支展开（不把互斥候选拼成一条顺序，也不因为
// 条件覆盖丢掉确定会执行的元素）。列表/迭代预算与单列表路径共用同一常量。

import (
	"go/ast"
	"go/token"
	"strconv"
	"strings"
)

// seqRangeMultiListsValue range 对象是多候选函数列表（全部候选项都是确定
// 列表）时返回候选；任一候选不是列表/有预算诊断 → false（保持原诊断路径）。
func seqRangeMultiListsValue(a *Actions, req CodeSequenceRequest, x ast.Expr) ([]seqValue, bool) {
	if req.env == nil {
		return nil, false
	}
	v := captureSeqClosureValue(a, req, req.curCtx, x)
	if v.kind != seqValueMulti || len(v.cands) < 2 {
		return nil, false
	}
	out := make([]seqValue, 0, len(v.cands))
	for _, cand := range v.cands {
		if cand.kind != seqValueList || cand.why != "" {
			return nil, false
		}
		if !seqListHoldsFuncValues(a, req, x, cand) {
			return nil, false
		}
		out = append(out, cand)
	}
	return out, true
}

// seqRangeCommonPrefix 所有候选列表按位置相同的公共前缀长度。
func seqRangeCommonPrefix(lists []seqValue) int {
	if len(lists) == 0 {
		return 0
	}
	n := len(lists[0].elems)
	for _, l := range lists[1:] {
		if len(l.elems) < n {
			n = len(l.elems)
		}
	}
	for i := 0; i < n; i++ {
		base := lists[0].elems[i]
		for _, l := range lists[1:] {
			if !sameSeqValue(base, l.elems[i]) {
				return i
			}
		}
	}
	return n
}

// seqRangeOverMultiFuncList 多候选列表的 range：公共前缀确定执行，差异尾部
// 按候选分支展开（分支节点标 ambiguous，不声称互斥分支都执行）。
func seqRangeOverMultiFuncList(a *Actions, req CodeSequenceRequest, fset *token.FileSet, src []byte,
	s *ast.RangeStmt, lists []seqValue, tgts *seqTargets, depth int, recvType string,
	recvDecl map[string]string, f *ast.File, curPkg, curFile string) *CodeSeqNode {
	node := &CodeSeqNode{Kind: "loop", Label: "range " + seqExprText(fset, src, s.X),
		Line: fset.Position(s.Pos()).Line}
	for _, l := range lists {
		if len(l.elems) > seqMaxRangeIters {
			node.Stop = &SeqStop{Code: SeqStopFuncValueUnbound,
				Detail: "函数列表迭代超过预算 " + strconv.Itoa(seqMaxRangeIters) + "（todo22 §2.4）"}
			return node
		}
	}
	common := seqRangeCommonPrefix(lists)
	if common > seqMaxRangeIters {
		node.Stop = &SeqStop{Code: SeqStopFuncValueUnbound,
			Detail: "函数列表公共前缀超过迭代预算 " + strconv.Itoa(seqMaxRangeIters) + "（todo22 §2.4）"}
		return node
	}
	node.Evidence = &SeqEvidence{Source: SeqEvidenceClosure,
		CallFile: curFile, CallLine: node.Line, Reason: seqReasonMulti,
		Ambiguous: true, Candidates: seqListCandidateLabels(lists)}
	// 公共前缀：所有候选都执行（顺序一致），按确定路径展开一次。
	for i := 0; i < common; i++ {
		seqRangeBindElem(req.env, s, lists[0].elems[i])
		node.Nodes = append(node.Nodes, walkStmts(a, req, fset, src, s.Body.List, tgts,
			depth, recvType, recvDecl, f, curPkg, curFile)...)
	}
	// 差异尾部：每个候选一条互斥分支；空尾部（条件未追加）不产生分支。
	baseEnv := req.env.clone()
	var branchEnvs []*seqEnv
	count := 0
	for _, l := range lists {
		if len(l.elems) <= common {
			continue
		}
		count++
	}
	if count == 0 {
		return node
	}
	branchEnvs = append(branchEnvs, baseEnv)
	for i, l := range lists {
		tail := l.elems[common:]
		if len(tail) == 0 {
			continue
		}
		brReq := req
		if req.env != nil {
			brReq.env = baseEnv.clone()
		}
		br := &CodeSeqNode{Kind: "branch", Line: node.Line,
			Label: "range 列表候选 " + strconv.Itoa(i+1) + "/" + strconv.Itoa(len(lists)) + "（条件分支）",
			Evidence: &SeqEvidence{Source: SeqEvidenceClosure,
				CallFile: curFile, CallLine: node.Line, Reason: seqReasonMulti,
				Ambiguous: true, Candidates: seqListCandidateLabels(lists)}}
		for _, elem := range tail {
			seqRangeBindElem(brReq.env, s, elem)
			br.Nodes = append(br.Nodes, walkStmts(a, brReq, fset, src, s.Body.List, tgts,
				depth, recvType, recvDecl, f, curPkg, curFile)...)
		}
		node.Nodes = append(node.Nodes, br)
		if brReq.env != nil {
			branchEnvs = append(branchEnvs, brReq.env)
		}
	}
	if req.env != nil && len(branchEnvs) > 1 {
		req.env.mergeBranches(branchEnvs)
	}
	return node
}

// seqRangeBindElem range 迭代变量绑定（值 = 当前元素；索引 = 与执行无关）。
func seqRangeBindElem(env *seqEnv, s *ast.RangeStmt, elem seqValue) {
	if env == nil {
		return
	}
	if id, ok := s.Value.(*ast.Ident); ok && id.Obj != nil {
		env.set(id.Obj, elem)
	}
	if id, ok := s.Key.(*ast.Ident); ok && id.Obj != nil {
		env.set(id.Obj, unknownSeqValue("range 索引（与函数值执行无关）"))
	}
}

// seqListCandidateLabels 多候选列表的展示标签（诊断/证据用）。
func seqListCandidateLabels(lists []seqValue) []string {
	out := make([]string, 0, len(lists))
	for _, l := range lists {
		names := make([]string, 0, len(l.elems))
		for _, e := range l.elems {
			names = append(names, e.label())
		}
		out = append(out, "["+strings.Join(names, ", ")+"]")
	}
	return out
}

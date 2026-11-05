package action

// B07：实例读取值重新分析（从 action_seq_workflow_instance.go 拆出）。

import (
	"go/ast"
	"strings"
)

// B07：实例读取值重新分析（工厂/接口/回调前沿）。

// seqWFNeedsReanalysis 实例树里是否有"受读取值影响的同仓未展开前沿"。
func seqWFNeedsReanalysis(tree *CodeSeqNode) bool {
	var need bool
	var walk func(nodes []*CodeSeqNode)
	walk = func(nodes []*CodeSeqNode) {
		for _, n := range nodes {
			if n == nil || need {
				continue
			}
			if n.Kind == "call" && n.Evidence != nil {
				code := ""
				if n.Stop != nil {
					code = n.Stop.Code
				}
				sym := n.Evidence.SymbolID
				if (code == SeqStopFuncValueUnbound || n.Evidence.Reason == "localvar-init-dataflow" ||
					code == SeqStopIndexGap || n.Evidence.Ambiguous) && len(n.Nodes) == 0 &&
					sym != "" && !strings.HasPrefix(sym, "symbol:go:database/sql:") &&
					!strings.HasPrefix(sym, "symbol:go:net/http:") &&
					!strings.HasPrefix(sym, "symbol:go:github.com/xormplus/") {
					need = true
				}
			}
			walk(n.Nodes)
			walk(n.Else)
		}
	}
	walk(tree.Nodes)
	return need
}

// seqWFScanTargetByRead 目标树里读取身份 → Scan 目标列到变量名。
func seqWFScanTargetByRead(tree *CodeSeqNode, readID string) map[string]string {
	out := map[string]string{}
	var walk func(nodes []*CodeSeqNode)
	walk = func(nodes []*CodeSeqNode) {
		for _, n := range nodes {
			if n == nil {
				continue
			}
			if n.Evidence != nil && n.Evidence.Scan != nil && n.Evidence.Scan.Read != nil &&
				n.Evidence.Scan.Read.ID == readID {
				for _, t := range n.Evidence.Scan.Targets {
					if t.Column != "" && t.Target != "" {
						out[t.Column] = t.Target
					}
				}
			}
			walk(n.Nodes)
			walk(n.Else)
		}
	}
	walk(tree.Nodes)
	return out
}

// seqWFInstanceSeeds 读取值 → 变量名 → 字面量来源。
func seqWFInstanceSeeds(target *CodeSeqNode, conns []SeqWorkflowConnection, idxs []int) map[string]string {
	seeds := map[string]string{}
	for _, i := range idxs {
		if i < 0 || i >= len(conns) {
			continue
		}
		targets := seqWFScanTargetByRead(target, conns[i].ReadID)
		for _, v := range conns[i].ReadValues {
			name := targets[v.Column]
			if name == "" || v.Origin == nil || v.Origin.Kind != SeqOriginLiteral {
				continue
			}
			seeds[name] = v.Origin.Literal
		}
	}
	return seeds
}

// seqWFMergeBranchConditions 用原树同标签分支的 condition 覆盖重分析树
// （保留 read_column 来源身份，不把种子字面量当成新证据）。
func seqWFMergeBranchConditions(dst, src *CodeSeqNode) {
	if dst == nil || src == nil {
		return
	}
	byLabel := map[string]*SeqCondition{}
	var collect func(nodes []*CodeSeqNode)
	collect = func(nodes []*CodeSeqNode) {
		for _, n := range nodes {
			if n == nil {
				continue
			}
			if n.Kind == "branch" && n.Condition != nil {
				if _, ok := byLabel[n.Label]; !ok {
					byLabel[n.Label] = n.Condition
				}
			}
			collect(n.Nodes)
			collect(n.Else)
		}
	}
	collect(src.Nodes)
	var apply func(nodes []*CodeSeqNode)
	apply = func(nodes []*CodeSeqNode) {
		for _, n := range nodes {
			if n == nil {
				continue
			}
			if n.Kind == "branch" {
				if c, ok := byLabel[n.Label]; ok {
					n.Condition = c
				}
			}
			apply(n.Nodes)
			apply(n.Else)
		}
	}
	apply(dst.Nodes)
}

// seqWFMergeGuards 合并原树与重分析树的 Scan 成功守卫。
func seqWFMergeGuards(a, b map[string]bool) map[string]bool {
	out := map[string]bool{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

// seqSeedVarLits 实例重分析：把读取值作为局部变量的字面量来源（按 Obj 隔离）。
func seqSeedVarLits(req CodeSequenceRequest, env *seqEnv, body *ast.BlockStmt) {
	if env == nil || body == nil {
		return
	}
	ast.Inspect(body, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok || id.Obj == nil {
			return true
		}
		lit, want := req.seedVarLits[id.Name]
		if !want {
			return true
		}
		pos := seqDeclPosOf(id.Obj.Decl)
		if !pos.IsValid() || pos < body.Pos() || pos > body.End() {
			return true
		}
		env.setScalar(id.Obj, &SeqStorageOrigin{Kind: SeqOriginLiteral,
			ScalarType: "string", Literal: lit})
		return true
	})
}

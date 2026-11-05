package cli

// 渲染内容检查（从 query_sequence_render.go 拆出）。

import "github.com/schaepher/codeintel/internal/action"

// hasRenderableSeqNode 递归检查节点树是否有可渲染内容（R100：基础类型
// actor 的 call 消息渲染时被跳过——空分支检查须排除它们，否则 alt/end
// 之间无中间行的空块；return/continue/break 线可渲染）。
func hasRenderableSeqNode(nodes []*action.CodeSeqNode) bool {
	for _, n := range nodes {
		switch n.Kind {
		case "call":
			// 基础类型 actor（int32(x) 转换）消息被跳过；嵌套展开仍可渲染
			if !seqBaseTypeActor(n.Actor) || hasRenderableSeqNode(n.Nodes) {
				return true
			}
		case "branch":
			if hasRenderableSeqNode(n.Nodes) || hasRenderableSeqNode(n.Else) {
				return true
			}
		case "loop":
			if hasRenderableSeqNode(n.Nodes) {
				return true
			}
		default: // return/continue/break
			return true
		}
	}
	return false
}

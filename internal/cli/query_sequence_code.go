package cli

// R81 `query sequence --code <符号>`——代码级时序图（源码 AST 转时序）。
// R95：查询逻辑迁 action（Actions.CodeSequence）；cli 只做参数解析
// （--depth/停止包配置加载）与输出渲染（mermaid/缩进文本/JSON）。

import (
	"fmt"
	"os"

	"github.com/schaepher/codeintel/internal/action"
)

// cmdQuerySequenceCode 实现 `query sequence --code <符号> [--depth N] [--format mermaid] [--json]`。
// depth：嵌套层级（1 = 只本函数；>1 递归展开被调函数内部）。
func cmdQuerySequenceCode(acts *action.Actions, abs, target string, depth int, mermaid bool, jsonOut bool) int {
	if depth <= 0 {
		depth = 1
	}
	root, err := acts.CodeSequence(action.CodeSequenceRequest{
		Target: target, RepoAbs: abs, Depth: depth, StopPackages: loadSeqStopPkgs()})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if root == nil {
		fmt.Fprintf(os.Stderr, "error: 代码级时序不可用（符号解析失败或源码缺失——可尝试不带 --code 用索引调用链）\n")
		return 1
	}
	if jsonOut {
		encodeJSON(root)
		return 0
	}
	if mermaid {
		fmt.Print(renderCodeSeqMermaid(root))
		return 0
	}
	// 默认文本：缩进树（源码顺序 + 分支/循环标注）
	fmt.Printf("== %s 代码级时序 ==\n", root.Label)
	writeSeqText(root.Nodes, 1)
	return 0
}

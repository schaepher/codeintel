package cli

// todo22 §6：`query symbol --include-source` 的取数与渲染（从 query_symbol.go
// 拆出——行数治理）。取源码必须发生在任何输出之前：失败即非零退出，绝不
// 输出部分成功 JSON。

import (
	"fmt"
	"os"

	"github.com/schaepher/codeintel/internal/action"
)

// fetchSymbolSource 按 canonical ID 精确取声明源码；ok=false 表示已打印错误。
func fetchSymbolSource(acts *action.Actions, opts outputOpts, id string) (*action.SymbolSource, bool) {
	src, err := acts.SymbolSource(action.SymbolSourceRequest{RepoAbs: opts.repoAbs, SymbolID: id})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return nil, false
	}
	return src, true
}

// printSymbolSource 文本模式的源码块（JSON 模式把 struct 放进 source 键）。
func printSymbolSource(src *action.SymbolSource) {
	fmt.Printf("源码:       %s:%d-%d\n", src.File, src.LineStart, src.LineEnd)
	if src.Kind != "" {
		fmt.Printf("种类:       %s\n", src.Kind)
	}
	if src.Location != "" && src.Location != "repository" {
		fmt.Printf("位置来源:   %s\n", src.Location)
	}
	if src.SelectionLineStart > 0 {
		fmt.Printf("选中项:     第 %d-%d 行\n", src.SelectionLineStart, src.SelectionLineEnd)
	}
	if src.IndexStatus != "" {
		fmt.Printf("索引状态:   %s\n", src.IndexStatus)
	}
	fmt.Println(src.Text)
}

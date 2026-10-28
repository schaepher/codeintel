package cli

// todo22 §6：`query symbol --include-source` 的取数与渲染（从 query_symbol.go
// 拆出——行数治理）。取源码必须发生在任何输出之前：失败即非零退出，绝不
// 输出部分成功 JSON。

import (
	"fmt"
	"os"
	"strings"

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

// symbolSourceUnindexedJSON 未索引声明的 CLI 契约：顶层身份元数据 + source
// 明细（T13：stdout 无半份成功 JSON；字段顺序即契约顺序）。
type symbolSourceUnindexedJSON struct {
	File        string               `json:"file"`
	ID          string               `json:"id"`
	IndexStatus string               `json:"index_status,omitempty"`
	Kind        string               `json:"kind"`
	Line        int                  `json:"line"`
	Name        string               `json:"name"`
	Package     string               `json:"package,omitempty"`
	Source      *action.SymbolSource `json:"source"`
}

// symbolSourceName canonical ID → 声明名（未索引路径的顶层元数据）。
func symbolSourceName(id string) string {
	if i := strings.LastIndex(id, ":"); i >= 0 && i < len(id)-1 {
		return id[i+1:]
	}
	return id
}

// symbolSourceLine 顶层行号：分组声明取所选项（selection）而不是整组起始行。
func symbolSourceLine(src *action.SymbolSource) int {
	if src.SelectionLineStart > 0 {
		return src.SelectionLineStart
	}
	return src.LineStart
}

// printSymbolSource 文本模式的源码块（JSON 模式把 struct 放进 source 键）。
func printSymbolSource(src *action.SymbolSource) {
	fmt.Printf("源码:       %s:%d-%d\n", src.File, src.LineStart, src.LineEnd)
	if src.Kind != "" {
		fmt.Printf("种类:       %s\n", src.Kind)
	}
	if src.Package != "" {
		fmt.Printf("包:         %s\n", src.Package)
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
	if len(src.Imports) > 0 {
		// todo24 §11.4：声明所在文件的 imports（文件上下文，不表示该声明使用）
		parts := make([]string, 0, len(src.Imports))
		for _, im := range src.Imports {
			if im.Alias != "" {
				parts = append(parts, im.Alias+" "+im.Path)
				continue
			}
			parts = append(parts, im.Path)
		}
		fmt.Printf("导入:       %s\n", strings.Join(parts, ", "))
	}
	fmt.Println(src.Text)
}

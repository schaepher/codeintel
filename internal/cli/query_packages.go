package cli

// R77 `query packages`——包结构（wiki 包结构节转命令）：包路径 +
// doc_comment（去 Copyright）+ 无说明时的包内代码事实（结构体/方法/
// 函数签名表格）。R9x：聚合逻辑迁 action（Actions.PackagesData）；
// cli 只做参数转发与输出格式化。

import (
	"fmt"
	"os"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
)

// cmdQueryPackages 实现 `query packages [--json|--compact]`。
func cmdQueryPackages(acts *action.Actions, opts outputOpts) int {
	pkgs, err := acts.PackagesData()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if opts.json {
		encodeJSON(pkgs)
		return 0
	}
	if len(pkgs) == 0 {
		fmt.Println("（无包节点——可能未重建索引）")
		return 0
	}
	for _, p := range pkgs {
		fmt.Printf("## %s\n\n", p.Path)
		if p.Doc != "" {
			fmt.Println(p.Doc + "\n")
			continue
		}
		fmt.Println("（无包级说明——代码事实）")
		if len(p.Structs) > 0 {
			fmt.Println("结构体：" + strings.Join(p.Structs, "、"))
		}
		if len(p.Methods) > 0 {
			fmt.Println("方法：")
			for _, m := range p.Methods {
				fmt.Println("  - " + m)
			}
		}
		if len(p.Funcs) > 0 {
			fmt.Println("函数：")
			for _, fn := range p.Funcs {
				fmt.Println("  - " + fn)
			}
		}
		fmt.Println()
	}
	fmt.Printf("共 %d 个包\n", len(pkgs))
	return 0
}

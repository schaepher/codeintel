package cli

// R35 `codeintel query cli-routes`——urfave/cli/v2 命令树（待办 3：
// 不依赖文件路径——构建期识别 cli_command 节点，查询层直接读）。
// R100：查询逻辑迁 action（Actions.CliRoutes——裸 SQL 收口仓储）；
// cli 只留参数解析与输出渲染。JSON 契约：命令名/用法/action/位置 +
// 子命令树。

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
)

// cmdCLIRoutes 实现 `codeintel query cli-routes [--repo <path>] [--json]`
func cmdCLIRoutes(acts *action.Actions, f queryFlags) int {
	res, err := acts.CliRoutes()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if f.json {
		b, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Println(string(b))
		return 0
	}
	var walk func(cmds []action.CliCommandEntry, depth int)
	walk = func(cmds []action.CliCommandEntry, depth int) {
		for _, c := range cmds {
			fmt.Printf("%s%s", strings.Repeat("  ", depth), c.Name)
			if c.Usage != "" {
				fmt.Printf("  %s", c.Usage)
			}
			if c.Action != "" {
				fmt.Printf("  → %s", c.Action)
			}
			fmt.Println()
			walk(c.Subcommands, depth+1)
		}
	}
	walk(res.Commands, 0)
	if len(res.Commands) == 0 {
		fmt.Println("未识别到 urfave/cli/v2 命令树")
	}
	return 0
}

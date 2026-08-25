package cli

// R60 `codeintel config default`——输出默认全局配置（内置模板：全部
// 选项 + 默认值 + 注释）到 stdout；Makefile install 首次安装时判断
// ~/.codeintel/config.yaml 不存在 → 重定向写入（go:embed 模板同源，
// 不依赖仓库文件）。

import (
	"fmt"
	"os"
)

// cmdConfig 实现 `codeintel config <default>`。
func cmdConfig(args []string) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Println("用法: codeintel config default\n  输出默认全局配置（~/.codeintel/config.yaml 模板——全部选项 + 默认值 + 注释）到 stdout；首次安装时 Makefile install 自动重定向写入")
		return 0
	}
	switch args[0] {
	case "default":
		fmt.Print(configExample)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "error: 未知 config 子命令 %q（支持 default）\n", args[0])
		return 2
	}
}

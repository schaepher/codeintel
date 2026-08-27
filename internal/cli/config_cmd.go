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
		fmt.Println("用法: codeintel config default|merge\n  default: 输出默认全局配置（模板——全部选项 + 默认值 + 注释）到 stdout；首次安装时 Makefile install 自动重定向写入\n  merge: 检查现有配置缺失的配置项并从模板补默认值（保留已有值/注释——S7）")
		return 0
	}
	switch args[0] {
	case "default":
		fmt.Print(configExample)
		return 0
	case "merge":
		// S7：检查缺失配置项并补默认值（保留用户值/注释）
		return cmdConfigMerge()
	default:
		fmt.Fprintf(os.Stderr, "error: 未知 config 子命令 %q（支持 default|merge）\n", args[0])
		return 2
	}
}

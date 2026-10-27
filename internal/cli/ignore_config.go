package cli

// 仓库级忽略目录配置（`<repo>/codeintel.yaml`）：`codeintel config
// ignore-dir add|rm|list` 管理；init/update/reindex 与所有读取路径经
// buildRepo（action.DiscoverRepository）自动加载（无对应命令行选项）。
// 文件在 .codeintel/ 之外，clean 不会删除。
//
// P1-03：读/归一/写实现在 action（构建流程与变更检测共用同一口径），
// 本文件只保留命令参数解析与输出。

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/action"
)

// ignoreConfigPath 仓库级配置文件路径（实现见 action.IgnoreConfigPath）。
func ignoreConfigPath(repoAbs string) string { return action.IgnoreConfigPath(repoAbs) }

// loadIgnoreDirs 读仓库级配置的 ignore_dirs（实现见 action.LoadIgnoreDirs）。
func loadIgnoreDirs(repoAbs string) []string { return action.LoadIgnoreDirs(repoAbs) }

// saveIgnoreDirs 写仓库级配置（实现见 action.SaveIgnoreDirs）。
func saveIgnoreDirs(repoAbs string, dirs []string) error {
	return action.SaveIgnoreDirs(repoAbs, dirs)
}

// normalizeIgnoreDir 归一忽略目录（实现见 action.NormalizeIgnoreDir）。
func normalizeIgnoreDir(dir string) (string, error) { return action.NormalizeIgnoreDir(dir) }

// cmdConfigIgnoreDir 实现 `codeintel config ignore-dir add|rm|list [<dir>...] [--repo <path>]`。
func cmdConfigIgnoreDir(args []string) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Println("用法: codeintel config ignore-dir add|rm|list [<dir>...] [--repo <path>]\n" +
			"  管理仓库级忽略目录（<repo>/codeintel.yaml 的 ignore_dirs）——\n" +
			"  init/update/reindex 自动应用，不再需要命令行 --ignore-dir\n" +
			"  add: 追加（去重、排序）；rm: 删除；list: 列出")
		return 0
	}
	sub := args[0]
	rest := args[1:]
	repoPath := "."
	var positional []string
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		switch {
		case a == "--repo" && i+1 < len(rest):
			repoPath = rest[i+1]
			i++
		case strings.HasPrefix(a, "--repo="):
			repoPath = strings.TrimPrefix(a, "--repo=")
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(os.Stderr, "error: 未知参数 %q\n", a)
			return 2
		default:
			positional = append(positional, a)
		}
	}
	repoPath = ResolveRepoRef(repoPath)
	abs, err := filepath.Abs(repoPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: resolve repo path: %v\n", err)
		return 1
	}
	dirs := loadIgnoreDirs(abs)
	switch sub {
	case "list":
		if len(dirs) == 0 {
			fmt.Println("（无忽略目录）")
			return 0
		}
		for _, d := range dirs {
			fmt.Println(d)
		}
		return 0
	case "add":
		if len(positional) == 0 {
			fmt.Fprintln(os.Stderr, "error: 用法: codeintel config ignore-dir add <dir> [--repo <path>]")
			return 2
		}
		for _, p := range positional {
			d, err := normalizeIgnoreDir(p)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return 1
			}
			if !containsString(dirs, d) {
				dirs = append(dirs, d)
			}
		}
		sort.Strings(dirs)
		if err := saveIgnoreDirs(abs, dirs); err != nil {
			fmt.Fprintf(os.Stderr, "error: 写入 %s: %v\n", ignoreConfigPath(abs), err)
			return 1
		}
		fmt.Printf("已配置忽略目录（%s）：%s\n", ignoreConfigPath(abs), strings.Join(dirs, ", "))
		return 0
	case "rm", "remove":
		if len(positional) == 0 {
			fmt.Fprintln(os.Stderr, "error: 用法: codeintel config ignore-dir rm <dir> [--repo <path>]")
			return 2
		}
		for _, p := range positional {
			d, err := normalizeIgnoreDir(p)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return 1
			}
			dirs = removeString(dirs, d)
		}
		if err := saveIgnoreDirs(abs, dirs); err != nil {
			fmt.Fprintf(os.Stderr, "error: 写入 %s: %v\n", ignoreConfigPath(abs), err)
			return 1
		}
		if len(dirs) == 0 {
			fmt.Println("已清空忽略目录（删除 codeintel.yaml）")
		} else {
			fmt.Printf("已配置忽略目录（%s）：%s\n", ignoreConfigPath(abs), strings.Join(dirs, ", "))
		}
		return 0
	default:
		fmt.Fprintf(os.Stderr, "error: 未知 ignore-dir 子命令 %q（支持 add|rm|list）\n", sub)
		return 2
	}
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func removeString(list []string, s string) []string {
	out := list[:0:0]
	for _, x := range list {
		if x != s {
			out = append(out, x)
		}
	}
	return out
}

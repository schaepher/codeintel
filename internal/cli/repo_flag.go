package cli

// parseRepoFlag —— rule/precompute 等命令共用的 --repo 解析。
// P1-03：原定义在 rule.go，迁移规则逻辑到 action 时抽出（避免
// 命令实现文件承担跨命令共用参数解析）。

import "strings"

// parseRepoFlag 提取 --repo <path>（或 --repo=path）并返回剩余参数。
// Q237：未指定 --repo 默认当前工作目录（"."）；Q238：指定值经注册表
// 短名/后缀/module 解析。
func parseRepoFlag(args []string) (string, []string, error) {
	repo := "."
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--repo" && i+1 < len(args):
			repo = ResolveRepoRef(args[i+1])
			i++
		case strings.HasPrefix(a, "--repo="):
			repo = ResolveRepoRef(strings.TrimPrefix(a, "--repo="))
		case a == "--json":
			rest = append(rest, a)
		default:
			rest = append(rest, a)
		}
	}
	return repo, rest, nil
}
